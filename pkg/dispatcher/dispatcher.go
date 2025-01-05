/*
Copyright 2023 The Godel Scheduler Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dispatcher

import (
	"context"
	"fmt"
	"k8s.io/klog/v2"
	"time"

	scheduling "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	crdclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	nodeinformer "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions/node/v1alpha1"
	schedulinginformer "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions/scheduling/v1alpha1"
	nodelister "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/node/v1alpha1"
	schedulinglister "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	"github.com/kubewharf/godel-scheduler/pkg/dispatcher/internal/queue"
	"github.com/kubewharf/godel-scheduler/pkg/dispatcher/internal/store"
	"github.com/kubewharf/godel-scheduler/pkg/dispatcher/metrics"
	nodeshuffler "github.com/kubewharf/godel-scheduler/pkg/dispatcher/node-shuffler"
	"github.com/kubewharf/godel-scheduler/pkg/dispatcher/reconciler"
	schemaintainer "github.com/kubewharf/godel-scheduler/pkg/dispatcher/scheduler-maintainer"
	"github.com/kubewharf/godel-scheduler/pkg/features"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
	unitutil "github.com/kubewharf/godel-scheduler/pkg/util/unit"
	v1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	coreinformers "k8s.io/client-go/informers/core/v1"
	schedinformers "k8s.io/client-go/informers/scheduling/v1"
	"k8s.io/client-go/kubernetes"
	listerv1 "k8s.io/client-go/listers/core/v1"
	schedulingv1 "k8s.io/client-go/listers/scheduling/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/events"
)

const (
	DispatcherTag = "dispatcher"
)

type Dispatcher struct {
	StopEverything <-chan struct{}
	client         kubernetes.Interface
	podLister      listerv1.PodLister

	// 离线 job 批调度相关
	// TODO: move to policy manager
	// UnitManager contains pending pods belonging to scheduling units.
	UnitInfos queue.UnitInfos

	// FIFOPendingPodsQueue is used to store pods that will be dispatched
	// in a FIFO manner
	// TODO: figure out if we really need this queue
	// TODO: move to policy manager if necessary
	FIFOPendingPodsQueue queue.PendingQueue

	// SortedPodsQueue stores pending pods that have already be sorted
	// based on their configured ordering policy. When dispatching pods to
	// scheduler, dispatcher will pop pods from this queue.
	// "SortedPodsQueue 存储已经根据其配置的排序策略进行排序的待处理 Pod。在将 Pod 分配给调度器时，调度器将从此队列中弹出 Pod。"
	SortedPodsQueue queue.SortedQueue

	DispatchInfo store.DispatchInfo

	OwnerInfos store.OwnerInfo

	SchedulerLister     schedulinglister.SchedulerLister
	NodeLister          listerv1.NodeLister
	NMNodeLister        nodelister.NMNodeLister
	PodGroupLister      schedulinglister.PodGroupLister
	PriorityClassLister schedulingv1.PriorityClassLister

	maintainer *schemaintainer.SchedulerMaintainer
	shuffler   *nodeshuffler.NodeShuffler

	reconciler *reconciler.PodStateReconciler

	// SchedulerName here is the higher level scheduler name, which is used to select pods
	// that godel schedulers should be responsible for and filter out irrelevant pods.
	// SchedulerName 这里是更高层次的调度器名称，用于选择 godel 调度器应该负责的 pods，并过滤掉不相关的 pods。
	SchedulerName string

	recorder events.EventRecorder
}

func New(
	stopCh <-chan struct{},
	client kubernetes.Interface,
	crdClient crdclient.Interface,
	podInformer coreinformers.PodInformer,
	nodeInformer coreinformers.NodeInformer,
	schedulerInformer schedulinginformer.SchedulerInformer,
	nmNodeInformer nodeinformer.NMNodeInformer,
	podGroupInformer schedulinginformer.PodGroupInformer,
	priorityClassInformer schedinformers.PriorityClassInformer,
	schedulerName string,
	recorder events.EventRecorder,
) *Dispatcher {
	metrics.Register()

	// Scheduler Maintainer: 主要负责对 Scheduler 实例状态进行维护，包括 Scheduler 实例健康状况、负载情况、Partition 节点数等.
	// Node Shuffler: 主要负责基于 Scheduler 实例个数，对集群节点进行 Partition 分片。
	maintainer := schemaintainer.NewSchedulerMaintainer(crdClient, schedulerInformer.Lister())
	shuffler := nodeshuffler.NewNodeShuffler(client, crdClient, nodeInformer.Lister(), nmNodeInformer.Lister(), schedulerInformer.Lister(), maintainer)

	// Dispatcher 主要负责应用排队，应用分发，节点分区等工作
	// 它主要由几个部分构成：Sorting Policy Manager、Dispatching Policy Manager、Node Shuffler、Scheduler Maintainer 和 Reconciler。
	dispatcher := &Dispatcher{
		StopEverything:       stopCh,
		client:               client,
		podLister:            podInformer.Lister(),
		UnitInfos:            queue.NewUnitInfos(recorder),
		OwnerInfos:           store.NewOwnerInfo(),
		FIFOPendingPodsQueue: queue.NewPendingFIFO(metrics.NewPendingPodsRecorder("pending")),
		SortedPodsQueue:      queue.NewSortedFIFO(metrics.NewPendingPodsRecorder("ready")),
		DispatchInfo:         store.NewDispatchInfo(),
		SchedulerLister:      schedulerInformer.Lister(),

		maintainer:    maintainer,
		shuffler:      shuffler,
		SchedulerName: schedulerName,

		NodeLister:          nodeInformer.Lister(),
		NMNodeLister:        nmNodeInformer.Lister(),
		PodGroupLister:      podGroupInformer.Lister(),
		PriorityClassLister: priorityClassInformer.Lister(),

		recorder: recorder,
	}

	// 主要负责周期性的检查 Pod、Node、Scheduler、SchedulingUnit 等状态，修正错误状态，查漏补缺。
	reconciler := reconciler.NewPodStateReconciler(client, podInformer.Lister(), nodeInformer.Lister(),
		schedulerInformer.Lister(), nmNodeInformer.Lister(), schedulerName, dispatcher.DispatchInfo, maintainer)
	dispatcher.reconciler = reconciler

	// 为 dispatcher 添加各种事件处理函数
	AddAllEventHandlers(dispatcher, podInformer, schedulerInformer, nodeInformer, nmNodeInformer, podGroupInformer)
	go func() {
		<-dispatcher.StopEverything
		dispatcher.FIFOPendingPodsQueue.Close()
		dispatcher.SortedPodsQueue.Close()
	}()
	return dispatcher
}

// todo: event handlers and sort pods
func (d *Dispatcher) Run(ctx context.Context) {
	// TODO: move to policy manager
	go d.UnitInfos.Run(d.StopEverything) // 判断 podGroup 中的 pods 是否可以发送，如果可以发送，则添加到 ready queue 中

	// TODO: sending sorted pods to scheduler in parallel if necessary
	// TODO: adaptive worker threads count
	go wait.UntilWithContext(ctx, d.sortedLoop, 0) // 遍历 SortedPodsQueue，并起 goroutine dispatch

	/*
		go wait.UntilWithContext(ctx, d.dispatchLoop, 0)
		go wait.UntilWithContext(ctx, d.bindLoop, 0)
	*/

	go d.maintainer.Run(d.StopEverything) // 主要负责对 Scheduler 实例状态进行维护，包括 Scheduler 实例健康状况、负载情况、Partition 节点数等

	if utilfeature.DefaultFeatureGate.Enabled(features.DispatcherNodeShuffle) {
		go d.shuffler.Run(d.StopEverything) // 更新节点和 scheudler 的对应关系，并平衡 Scheduler 实例上节点数量
	}

	go wait.UntilWithContext(ctx, d.pendingLoop, 0)         // 将 FIFOPendingPodsQueue 的 pod 放到 sorted queue 中
	go wait.UntilWithContext(ctx, d.pendingUnitPodsLoop, 0) // 将 UnitInfos 的 Pod 添加到 FIFOPendingPodsQueue(或policy manager) 中。

	go d.reconciler.Run(d.StopEverything) // 主要负责周期性的检查 Pod、Node、Scheduler、SchedulingUnit 等状态，修正错误状态，查漏补缺。
}

// pendingUnitPodsLoop adds pods belonging to dispatchable units to the policy
// manager or the FIFOPendingPodsQueue.
func (d *Dispatcher) pendingUnitPodsLoop(ctx context.Context) {
	workFunc := func() bool {
		podInfo, err := d.UnitInfos.Pop()
		if err != nil {
			klog.InfoS("The pending unit pods loop failed", "err", err)
			return true
		}
		klog.V(5).InfoS("DEBUG: popped pod from unit infos ready queue", "pod", podInfo.PodKey)
		d.FIFOPendingPodsQueue.AddPodInfo(podInfo)
		return false
	}

	for {
		if quit := workFunc(); quit {
			klog.InfoS("Shut down the pending unit pods loop worker")
			return
		}
	}
}

func (d *Dispatcher) pendingLoop(ctx context.Context) {
	podInfos, err := d.FIFOPendingPodsQueue.Pop()
	if err != nil {
		klog.InfoS("BestEffort pending queue pop failed", "err", err)
		return
	}

	for _, podInfo := range podInfos {
		parentSpanContext := podInfo.SpanContext
		podProperty := podInfo.GetPodProperty()
		span, _ := tracing.StartSpanForPodWithParentSpan(
			podInfo.PodKey,
			"dispatcher::movePodToSortedQueue",
			parentSpanContext,
			tracing.WithDispatcherOption(),
			tracing.WithResult(tracing.ResultSuccess),
			podProperty.ConvertToTracingTags(),
		)
		if parentSpanContext == nil || parentSpanContext.IsEmpty() {
			parentSpanContext = span.RootSpanContext()
		}

		podInfo.SpanContext = parentSpanContext
		metrics.PodPendingLatencyObserve(podProperty, string(podInfo.PodResourceType), helper.SinceInSeconds(podInfo.Timestamp))
		d.SortedPodsQueue.AddPodInfo(podInfo)
		span.Finish()
	}
}

func (d *Dispatcher) dispatchingPod(ctx context.Context, podInfo *queue.QueuedPodInfo, originalQueue queue.SortedQueue) {
	metrics.DispatcherGoroutinesInc()
	defer metrics.DispatcherGoroutinesDec()

	start := time.Now()

	namespace, name, err := cache.SplitMetaNamespaceKey(podInfo.PodKey)
	if err != nil {
		klog.InfoS("Failed to split the Meta Namespace Key", "pod", podInfo.PodKey, "err", err)
		originalQueue.AddPodInfo(podInfo)
		return
	}

	pod, err := d.podLister.Pods(namespace).Get(name)
	if apierrs.IsNotFound(err) || pod.DeletionTimestamp != nil ||
		!podutil.PendingPodOfGodel(pod, d.SchedulerName) {
		// podInfo was deleted before or is being deleted, or is not in pending state now
		// return directly without re-enqueuing the podInfo
		return
	}

	// get pod labels, which is used in metrics
	podProperty := podInfo.GetPodProperty()
	span, _ := tracing.StartSpanForPodWithParentSpan(
		podInfo.PodKey,
		"dispatcher::dispatchingPod",
		podInfo.SpanContext,
		tracing.WithDispatcherOption(),
		podProperty.ConvertToTracingTags(),
	)
	podInfo.SpanContext = span.RootSpanContext()

	defer func() {
		go span.Finish()
	}()

	// 为 pod 选择一个 scheduler
	schedulerName, err := d.selectScheduler(pod)
	if err != nil {
		klog.InfoS("Failed to select the scheduler", "err", err)
		metrics.PodDispatchingFailure(helper.SinceInSeconds(start))
		podInfo.Timestamp = time.Now()
		originalQueue.AddPodInfo(podInfo)
		span.WithTags(tracing.WithResultTag(tracing.ResultFailure))
		return
	}

	metrics.PodDispatched(helper.SinceInSeconds(start))

	start = time.Now()

	// patch pod
	// "godel.bytedance.com/selected-scheduler": schedulerName
	// "godel.bytedance.com/pod-state": "dispatched"
	if err := d.sendPodToScheduler(pod, podInfo, schedulerName); err != nil {
		// TODO: need to parse error in order to avoid pod ping-pong because of "resource too old" error
		klog.InfoS("Failed to send pod to the scheduler", "err", err)
		metrics.ObservePodUpdatingAttemptAndLatency(podProperty, metrics.FailureResult, helper.SinceInSeconds(start))
		span.WithTags(tracing.WithResultTag(tracing.ResultFailure))
		// Avoid re-adding pods that have been deleted to the queue.
		// more details: https://github.com/kubewharf/godel-scheduler/merge_requests/738
		if !apierrs.IsNotFound(err) {
			podInfo.Timestamp = time.Now()
			originalQueue.AddPodInfo(podInfo)
		}
		return
	}

	metrics.ObservePodUpdatingAttemptAndLatency(podProperty, metrics.SuccessResult, helper.SinceInSeconds(start))
	metrics.DispatchedPodsInc(podProperty, schedulerName)
	metrics.ObservePodDispatchingLatency(helper.SinceInSeconds(podInfo.InitialAddedTimestamp))
	span.WithTags(tracing.WithResultTag(tracing.ResultSuccess))
}

func (d *Dispatcher) sortedLoop(ctx context.Context) {
	for {
		if podInfo, _ := d.SortedPodsQueue.PopPodInfo(); podInfo != nil {
			parentSpanContext := podInfo.SpanContext
			podProperty := podInfo.GetPodProperty()
			traceContext, _ := tracing.StartSpanForPodWithParentSpan(
				podInfo.PodKey,
				"dispatcher::popPodFromSortedQueue",
				parentSpanContext,
				tracing.WithDispatcherOption(),
				podProperty.ConvertToTracingTags(),
			)
			podInfo.SpanContext = traceContext.RootSpanContext()

			metrics.PodPendingLatencyObserve(podProperty, string(podInfo.PodResourceType), helper.SinceInSeconds(podInfo.Timestamp))
			// TODO(zhangrenyu): make sure it won't impact the performance if remove goroutine
			go d.dispatchingPod(ctx, podInfo, d.SortedPodsQueue)
			traceContext.Finish()
		}
	}
}

func (d *Dispatcher) dispatchLoop(ctx context.Context) {
	// TODO: figure out what we can do if schedulers go down
}

func (d *Dispatcher) bindLoop(ctx context.Context) {
	// TODO: figure out what we can do if binders go down
}

func (d *Dispatcher) getAssignedSchedulerFromPods(pg *scheduling.PodGroup) (string, error) {
	// construct selector

	selector := labels.Set(map[string]string{
		podutil.PodGroupNameAnnotationKey: pg.Name,
	}).AsSelector()
	pods, err := d.podLister.Pods(pg.Namespace).List(selector)
	if err != nil {
		return "", err
	}

	for _, p := range pods {
		if p.Annotations != nil && p.Annotations[podutil.SchedulerAnnotationKey] != "" {
			return p.Annotations[podutil.SchedulerAnnotationKey], nil
		}
	}
	return "", nil
}

func (d *Dispatcher) getAssignedScheduler(pg *scheduling.PodGroup) (string, error) {
	// 从 UnitInfos 中获取 podGroup 的 scheduler
	cachedSched := d.UnitInfos.GetAssignedSchedulerForPodGroupUnit(pg)
	if cachedSched != "" {
		return cachedSched, nil
	}

	// in case of master/backup switch for HA, we will try to get the assigned
	// scheduler name from dispatched/assumed pods.
	// list podGroup 下的 pods，从 pod 的 annotation 中获取 scheduler 的名字
	schedName, err := d.getAssignedSchedulerFromPods(pg)
	if err != nil {
		return "", err
	}
	if schedName != "" {
		// update the dispatch info cache
		if err := d.UnitInfos.AssignSchedulerToPodGroupUnit(pg, schedName, false); err != nil {
			return schedName, err
		}
	}

	return schedName, nil
}

// selectSchedulerForUnit selects a secheduler for the podgroup, if the
// dispatcher has already assigned a scheduler to the podgroup, then returns
// the existing one.
// selectSchedulerForUnit 为 podgroup 选择一个 scheudler，如果调度程序已为 podgroup 分配了 scheduler，则返回现有的 scheduler。
func (d *Dispatcher) selectSchedulerForUnit(pg *scheduling.PodGroup, pod *v1.Pod, podOwner string) (string, error) {
	schedName, err := d.getAssignedScheduler(pg)
	if err != nil {
		return "", err
	}

	// scheduler name 不为空 && scheduler exist && scheduler is active，返回 scheduler name
	if schedName != "" && d.maintainer.SchedulerExist(schedName) && d.maintainer.IsSchedulerInActiveQueue(schedName) {
		d.DispatchInfo.AddPodInAdvance(pod, schedName)
		if utilfeature.DefaultFeatureGate.Enabled(features.SupportRescheduling) {
			d.OwnerInfos.AddDispatchedUnboundPod(pod, schedName)
		}
		return schedName, nil
	}

	forceUpdate := false
	if len(schedName) != 0 {
		// previous scheduler name for this unit is not empty, but that scheduler is inactive or deleted
		// we need to reset the scheduler name for this unit forcefully
		forceUpdate = true
	}
	klog.V(4).InfoS("Selected a new scheduler for the podGroup", "podGroup", klog.KObj(pg))
	// select a scheduler for the first dispatchable pod of the PodGroup.  PodGroup 的第一个 dispatchable pod 选择 scheduelr。
	schedName, err = d.pickScheduler(pod)
	if err != nil {
		return "", err
	}
	// store the assigned scheduler in the unit info cache.  将指定的 scheduler 保存在 unit 缓存中，ui.scheduler
	err = d.UnitInfos.AssignSchedulerToPodGroupUnit(pg, schedName, forceUpdate)
	return schedName, err
}

// 为 pod 选择一个 scheduler
// 1. 获取 podOwner 和 podGroup Name
// 2. podGroup Name 为空，直接按照 loadBalancing 策略选择节点数最少的 scheduler
// 3. 若 podGroup Name 不为空，则
func (d *Dispatcher) selectScheduler(pod *v1.Pod) (string, error) {
	podOwner := podutil.GetPodOwner(pod)
	pgName := unitutil.GetPodGroupName(pod)
	// get the scheduler name for pods belonging to an unit
	if pgName != "" {
		// if we fail to get the podgroup for the pod, we will not dispatch it.
		pg, err := d.PodGroupLister.PodGroups(pod.Namespace).Get(pgName)
		if err != nil {
			klog.ErrorS(err, "Failed to get the associated pod group for the pod", "pod", klog.KObj(pod))
			return "", err
		}
		// get the assigned scheduler, if any.
		return d.selectSchedulerForUnit(pg, pod, podOwner)
	}
	// get the scheduler name for pods not belonging to any unit
	return d.pickScheduler(pod)
}

func (d *Dispatcher) pickScheduler(pod *v1.Pod) (string, error) {
	if utilfeature.DefaultFeatureGate.Enabled(features.SupportRescheduling) {
		return d.selectSchedulerBasedOnOwner(pod)
	}
	return d.loadBalancing(pod)
}

func (d *Dispatcher) selectSchedulerBasedOnOwner(pod *v1.Pod) (string, error) {
	schedulerName := d.OwnerInfos.SelectSchedulerAndSetDispatchedUnboundPod(pod)
	if schedulerName != "" {
		d.DispatchInfo.AddPodInAdvance(pod, schedulerName)
		return schedulerName, nil
	}

	schedulerName, err := d.loadBalancing(pod)
	if err == nil && schedulerName != "" {
		gotSchedulerName := d.OwnerInfos.SetDispatchedUnboundPod(pod, schedulerName)
		if gotSchedulerName != schedulerName {
			d.DispatchInfo.UpdatePodInAdvance(pod, gotSchedulerName)
		}
		return gotSchedulerName, nil
	}

	return schedulerName, err
}

func (d *Dispatcher) loadBalancing(pod *v1.Pod) (string, error) {
	if schedulerName := d.DispatchInfo.GetMostIdleSchedulerAndAddPodInAdvance(pod); len(schedulerName) == 0 {
		return "", fmt.Errorf("no scheduler registered")
	} else {
		return schedulerName, nil
	}
}

func (d *Dispatcher) sendPodToScheduler(pod *v1.Pod, podInfo *queue.QueuedPodInfo, schedulerName string) (err error) {
	podCopy := pod.DeepCopy()
	if podCopy.Annotations == nil {
		podCopy.Annotations = make(map[string]string)
	}
	// "godel.bytedance.com/selected-scheduler": schedulerName
	// "godel.bytedance.com/pod-state": "dispatched"
	podCopy.Annotations[podutil.SchedulerAnnotationKey] = schedulerName
	podCopy.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodDispatched)
	if _, ok := podCopy.Annotations[podutil.InitialHandledTimestampAnnotationKey]; !ok {
		podCopy.Annotations[podutil.InitialHandledTimestampAnnotationKey] = podInfo.InitialAddedTimestamp.Format(helper.TimestampLayout)
	}
	if podCopy.Annotations[podutil.TraceContext] == "" {
		tracing.SetSpanContextForPod(podCopy, podInfo.SpanContext)
	}

	klog.V(2).InfoS("Started to send the pod to scheduler", "pod", klog.KObj(pod), "schedulerName", schedulerName)
	err = util.PatchPod(d.client, pod, podCopy)
	if err != nil {
		klog.ErrorS(err, "Fail to patch pod", "pod", klog.KObj(pod))
		// remove this pod from dispatched store if the api call fails
		d.DispatchInfo.RemovePod(podCopy)
		if utilfeature.DefaultFeatureGate.Enabled(features.SupportRescheduling) {
			d.deletePodFromOwnerInfo(podCopy)
		}
	}
	return err
}
