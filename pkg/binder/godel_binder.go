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

package binder

import (
	"context"
	"fmt"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"

	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	"github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/binder/cache"
	cachedebugger "github.com/kubewharf/godel-scheduler/pkg/binder/cache/debugger"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/binder/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/binder/queue"
	binderutils "github.com/kubewharf/godel-scheduler/pkg/binder/utils"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/nonnativeresource"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	"github.com/kubewharf/godel-scheduler/pkg/util/parallelize"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
	status "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
	katalystinformers "github.com/kubewharf/katalyst-api/pkg/client/informers/externalversions"
)

const (
	MaxPreemptionBackoffPeriodInSeconds = 600
	MaxRetryAttempts                    = 3 // TODO: 5 will cause a timeout in UT (30s)
)

// Binder watches for assumed pods from multiple schedulers,
// and binds pods if no conflicts, otherwise, rejects the pods.
type Binder struct {
	// SchedulerName here is the higher level scheduler name, which is used to select pods
	// that godel schedulers should be responsible for and filter out irrelevant pods.
	SchedulerName *string
	// Close this to shut down the scheduler.
	StopEverything <-chan struct{}

	// It is expected that changes made via BinderCache will be observed
	// by NodeLister and Algorithm.
	BinderCache godelcache.BinderCache
	// BinderQueue holds pods which have nominated nodes and
	// need to resolve conflicts
	BinderQueue queue.BinderQueue

	// NextPod should be a function that blocks until the next pod
	// is available. We don't use a channel for this, because scheduling
	// a pod may take some amount of time, and we don't want pods to get
	// stale while they sit in a channel.
	NextUnit func() *framework.QueuedUnitInfo
	// Error is called if there is an error. It is passed the pod in
	// question, and the error
	Error func(*framework.QueuedPodInfo, error)

	handle     handle.BinderFrameworkHandle
	recorder   events.EventRecorder
	reconciler *BinderTasksReconciler

	podLister corelisters.PodLister
	pgLister  v1alpha1.PodGroupLister

	// node partition doesn't make any effect for now, remove it from binder
	// TODO: figure out if we need this and add back if necessary
	// it is useful for scheduler since it may affect scheduling decisions,
	// for binder, we may implement a plugin to double confirm the result if necessary.
	// schedulerInfo *SchedulerInfo
}

// New returns a Binder
func New(
	client clientset.Interface,
	crdClient godelclient.Interface,
	informerFactory informers.SharedInformerFactory,
	crdInformerFactory crdinformers.SharedInformerFactory,
	katalystCrdInformerFactory katalystinformers.SharedInformerFactory,
	stopCh <-chan struct{},
	recorder events.EventRecorder,
	schedulerName *string,
	volumeBindingTimeoutSeconds int64,
	opts ...Option,
) (*Binder, error) {
	// register metrics before everything
	metrics.Register()

	stopEverything := stopCh
	if stopEverything == nil {
		stopEverything = wait.NeverStop
	}

	options := renderOptions(opts...)

	cacheHandler := commoncache.MakeCacheHandlerWrapper().
		Period(10 * time.Second).PodAssumedTTL(5 * time.Minute).StopCh(stopEverything).
		ComponentName("godel-binder").Obj()
	binderCache := godelcache.New(cacheHandler)

	binderQueue := queue.NewBinderQueue(
		DefaultUnitQueueSortFunc(),
		crdInformerFactory.Scheduling().V1alpha1().PodGroups().Lister(),
		informerFactory.Scheduling().V1().PriorityClasses().Lister(),
		binderCache,
	)

	binder := &Binder{
		SchedulerName:  schedulerName,
		StopEverything: stopEverything,

		BinderCache: binderCache,
		BinderQueue: binderQueue,

		NextUnit: queue.MakeNextUnitFunc(binderQueue),
		Error:    MakeDefaultErrorFunc(client, informerFactory.Core().V1().Pods().Lister(), binderQueue, binderCache),

		handle: NewFrameworkHandle(
			client, crdClient,
			informerFactory, crdInformerFactory,
			options,
			binderCache, volumeBindingTimeoutSeconds,
		),
		recorder:   recorder,
		reconciler: NewBinderTaskReconciler(client),

		podLister: informerFactory.Core().V1().Pods().Lister(),
		pgLister:  crdInformerFactory.Scheduling().V1alpha1().PodGroups().Lister(),
	}

	// Setup cache debugger.
	debugger := cachedebugger.New(
		informerFactory.Core().V1().Nodes().Lister(),
		informerFactory.Core().V1().Pods().Lister(),
		binderCache,
		binderQueue,
	)
	debugger.ListenForSignal(stopEverything)

	// Add all event handlers
	addAllEventHandlers(binder, informerFactory, crdInformerFactory, katalystCrdInformerFactory)

	return binder, nil
}

// Run begins watching and scheduling. It waits for cache to be synced, then starts scheduling and blocked until the context is done.
func (binder *Binder) Run(ctx context.Context) {
	binder.BinderQueue.Run()
	binder.reconciler.Run()
	resolveConflicts := func(ctx context.Context) {
		defer func() {
			if rc := recover(); rc != nil {
				klog.InfoS("Recovered in main workflow", "recover", rc)
			}
		}()
		for {
			if quit := binder.CheckAndBindUnit(ctx); quit {
				return
			}
		}
	}
	go wait.UntilWithContext(ctx, resolveConflicts, 0)

	<-ctx.Done()
}

func (binder *Binder) CheckAndBindUnit(ctx context.Context) bool {
	unit := binder.NextUnit()
	if err := ValidateUnit(unit); err != nil {
		klog.InfoS("Failed to validate unit", "unit", unit.GetKey(), "err", err)
		return false
	}
	klog.V(4).InfoS("Got unit from queue. Workflow started", "unit", unit.GetKey())

	// check timeout for unit (pod group)
	if binder.UnitTimeout(unit) {
		klog.InfoS("Unit timed out", "unitKey", unit.GetKey())
		binder.RejectTimeOutUnit(unit)
		metrics.ObserveRejectUnit(unit, metrics.BindingTimeout)
		return false
	}
	// TODO: check if sum of assumed tasks and new tasks equals to all member of unit info

	// binder unit initialization
	var unitInfo *bindingUnitInfo
	stages := []struct {
		stageName        string
		stageDescription string
		stageFunc        func() error
	}{
		// stage0: initialization
		{
			stageName:        "initialization",
			stageDescription: "initialize binding unitInfo",
			stageFunc: func() error {
				// TODO: work in parallel when constructing running unit info
				unitInfo = binder.InitializeUnit(unit)
				return nil
			},
		},
		// stage1: cross node checks for preemption
		{
			stageName:        "checkPreemption",
			stageDescription: "cross node checks: preemption",
			stageFunc:        func() error { return binder.CheckCrossNodePreemptionForUnit(ctx, unitInfo) },
		},
		// stage2: cross node checks for topology
		{
			stageName:        "checkCrossNodeTopology",
			stageDescription: "cross node checks: topology",
			stageFunc:        func() error { return binder.CheckCrossNodeTopologyForUnit(ctx, unitInfo) },
		},
		// stage3: same node checks for conflicts (node level conflicts will be assumed in parallel)
		{
			stageName:        "checkSameNodeConflicts",
			stageDescription: "same node checks",
			stageFunc:        func() error { return binder.CheckSameNodeConflictsForUnit(ctx, unitInfo) },
		},
		// stage4: assuming operations
		{
			stageName:        "assumeTask",
			stageDescription: "mark victims and assume tasks",
			stageFunc:        func() error { return binder.MarkVictimsAndAssumeTasks(ctx, unitInfo) },
		},
		// stage5: api operations
		{
			stageName:        "apiCall",
			stageDescription: "delete victims and bind tasks",
			stageFunc: func() error {
				// delete victims and bind pods, re-enqueue or reject tasks (if necessary)
				go binder.DeleteVictimsAndBindTasks(ctx, unitInfo)
				return nil
			},
		},
	}

	for i, stage := range stages {
		klog.V(4).InfoS("Started to check stage for unit", "stageIndex", i, "stageName", stage.stageName, "unitKey", unit.GetKey())
		if err := stage.stageFunc(); err != nil {
			klog.InfoS("Failed to check stage for unit", "stageIndex", i, "stageName", stage.stageName, "unitKey", unit.GetKey(), "err", err)
		}

		klog.V(4).InfoS("Finish to check stage for unit", "stageIndex", i, "stageName", stage.stageName, "unitKey", unit.GetKey())

		if unitInfo.IsUnitFailed() {
			err := fmt.Errorf("unit fails after %v", stage.stageName)
			unitInfo.MoveAllTasksToFailedList(err)
			binder.FailAndRejectAllTasks(unitInfo, err)

			metrics.ObserveRejectUnit(unit, stage.stageName)
			metrics.ObserveUnitBindingAttempts(unit, 0, unitInfo.allMember-len(unitInfo.ignoredTasks))
			return false
		}
	}

	return false
}

func (binder *Binder) initializeTask(unitInfo *bindingUnitInfo, runningUnitInfo *runningUnitInfo) (returnErr error) {
	podTrace := runningUnitInfo.getSchedulingTrace()
	initializeTraceContext := podTrace.NewTraceContext(tracing.RootSpan, tracing.BinderInitializeTaskSpan)
	initializeTraceContext.WithFields(tracing.WithNominatedNodeField(runningUnitInfo.queuedPodInfo.NominatedNode.Marshall()))
	defer func() {
		tracing.AsyncFinishTraceContext(initializeTraceContext, time.Now())
	}()

	defer func() {
		if returnErr != nil {
			initializeTraceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
			initializeTraceContext.WithFields(tracing.WithErrorField(returnErr))
			return
		}
		initializeTraceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))
	}()

	queuedPod := runningUnitInfo.queuedPodInfo
	framework, err := binder.handle.GetFrameworkForPod(queuedPod.Pod)
	if err != nil {
		returnErr = fmt.Errorf("fail to get framework for pod: %v/%v, error: %v", queuedPod.Pod.Namespace, queuedPod.Pod.Name, err)
		return
	}
	runningUnitInfo.Framework = framework
	initializeTraceContext.WithFields(tracing.WithPlugins(framework.ListPlugins())...)

	state, err := framework.InitCycleState(queuedPod.Pod)
	if err != nil {
		returnErr = fmt.Errorf("fail to init cycle state for pod: %v/%v, error: %v", queuedPod.Pod.Namespace, queuedPod.Pod.Name, err)
		return
	}
	runningUnitInfo.State = state

	suggestedNode := utils.GetNodeNameFromPod(queuedPod.Pod)
	if len(suggestedNode) == 0 {
		returnErr = fmt.Errorf("suggested node for pod: %v/%v is empty", queuedPod.Pod.Namespace, queuedPod.Pod.Name)
		return
	}
	runningUnitInfo.suggestedNode = suggestedNode

	// check if the queued pod is a preemptor
	var victims []*v1.Pod
	if queuedPod.NominatedNode != nil {
		// pod is a preemptor, check victims and store related info in unit info
		victims, err = binder.getVictimsToPreempt(queuedPod.NominatedNode.VictimPods)
		if err != nil {
			returnErr = fmt.Errorf("fail to get victims for pod: %v/%v, error: %v", queuedPod.Pod.Namespace, queuedPod.Pod.Name, err)
			return
		}
		// check if victims are selected by other preemptors in this unit
		// this should not happen, but just in case
		var duplicateVictim bool
		for _, victim := range victims {
			if unitInfo.HasVictim(victim.UID) {
				returnErr = fmt.Errorf("fail to check victims for pod: %v/%v, victim: %v/%v is selected by another preemptor in this unit", queuedPod.Pod.Namespace, queuedPod.Pod.Name, victim.Namespace, victim.Name)
				duplicateVictim = true
				break
			}
		}

		if duplicateVictim {
			// TODO: FIXME should not add failed task again.
			// unitInfo.AddFailedTask(runningUnitInfo, err, metrics.InitializationFailure, false)
			return returnErr
		}
	}
	// add victims to running unit info
	if len(victims) > 0 {
		runningUnitInfo.victims = append(runningUnitInfo.victims, victims...)
		if runningUnitInfo.queuedPodInfo.InitialPreemptAttemptTimestamp.IsZero() {
			runningUnitInfo.queuedPodInfo.InitialPreemptAttemptTimestamp = time.Now()
		}
	}
	runningUnitInfo.clonePodForRunningUnit()
	return nil
}

func (binder *Binder) InitializeUnit(unit *framework.QueuedUnitInfo) *bindingUnitInfo {
	// new binding unit info struct
	unitInfo := NewBindingUnitInfo(unit)
	// get and set everScheduled for unit info
	unitInfo.everScheduled = binder.BinderCache.GetUnitSchedulingStatus(unitInfo.unitKey) == status.ScheduledStatus

	// split pods(tasks) into two different slices (assumed and new),
	// and get victims and group them by nodes if there are any
	// NOTE: don't return even if we already know unit is failed, just for the following error handling logic in main workflow
	for _, queuedPod := range unit.GetPods() {
		if queuedPod.NewlyAssumedButStillInHandling {
			// ever assumed
			unitInfo.AddAssumedTask(queuedPod)
			continue
		}
		// newly coming pod

		// here, we haven't remove victims, so if one preemptor failed, we don't need to fail all other tasks on the same node
		// once we remove victims from node info, we need to do that (one preemptor failed, fail all other tasks on the same node)

		// check if pod is being deleted or is assumed (shouldn't happen) before
		if binder.skipCheckingPod(queuedPod.Pod) {
			unitInfo.AddIgnoredTasks(queuedPod)
			continue
		}
		runningUnitInfo := newRunningUnitInfo(queuedPod)

		err := binder.initializeTask(unitInfo, runningUnitInfo)
		if err != nil {
			unitInfo.AddFailedTask(runningUnitInfo, err, metrics.InitializationFailure, false)
			continue
		}

		// add running unit info to unit info
		unitInfo.AddNewTask(runningUnitInfo)
	}
	return unitInfo
}

func (binder *Binder) CheckCrossNodeTopologyForUnit(ctx context.Context, unitInfo *bindingUnitInfo) error {
	commonState := framework.NewCycleState()
	// TODO
	// Step 1: PrepareCommonState

	for _, newTask := range unitInfo.GetNewTasks() {
		nodeName := newTask.suggestedNode
		nodeInfo := binder.BinderCache.GetNodeInfo(nodeName)
		if nodeInfo == nil {
			unitInfo.AddFailedTask(newTask,
				fmt.Errorf("fail to get node in CheckCrossNodeTopologyForUnit for pod: %v, error: nodeInfo %v doesn't exist", podutil.GetPodKey(newTask.queuedPodInfo.Pod), nodeName),
				metrics.InternalErrorFailure, false)

			if unitInfo.IsUnitFailed() {
				err := fmt.Errorf("unit checks fail at CheckCrossNodeTopologyForUnit, err: nodeInfo %v doesn't exist", nodeName)
				unitInfo.MoveAllTasksToFailedList(err)
				return err
			}
			continue
		}

		status := CheckTopologyPhase(ctx, newTask, commonState, nodeInfo)
		if !status.IsSuccess() {
			unitInfo.AddFailedTask(newTask,
				fmt.Errorf("fail to check topology in CheckCrossNodeTopologyForUnit for pod: %v, error: %v", podutil.GetPodKey(newTask.queuedPodInfo.Pod), status.AsError().Error()),
				metrics.CheckTopologyFailure, false)
		} else {
			// TODO
			// Step 2: ApplyCommonState
			//
			// We don't need to perform the corresponding rollback operation for ApplyCommonState when the whole unit scheduling fails.
			// This is because the latest copy of the state is obtained each time via UpdateSnapshot.
		}

		if unitInfo.IsUnitFailed() {
			err := fmt.Errorf("unit checks fail at CheckCrossNodeTopologyForUnit")
			unitInfo.MoveAllTasksToFailedList(err)
			return err
		}
	}

	return nil
}

func checkCrossNodePreemptionForAssumedTask(podLister corelisters.PodLister, assumedTask *framework.QueuedPodInfo) (complete bool, inProgress bool, returnErr error) {
	pod := assumedTask.Pod
	podTrace := tracing.NewSchedulingTrace(pod, assumedTask.GetPodProperty().ConvertToTracingTags(), tracing.WithBinderOption())
	checkPreemptionTraceContext := podTrace.NewTraceContext(tracing.RootSpan, tracing.BinderCheckPreemptionSpan)
	checkPreemptionTraceContext.WithFields(tracing.WithMessageField("check preemption for assumed task"))
	checkPreemptionTraceContext.WithFields(tracing.WithNominatedNodeField(assumedTask.NominatedNode.Marshall()))

	// check if all the victims are cleaned up
	complete = IsPreemptionComplete(podLister, assumedTask.NominatedNode.VictimPods)
	defer tracing.AsyncFinishTraceContext(checkPreemptionTraceContext, time.Now())

	if !complete {
		checkPreemptionTraceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))

		// check if timed out
		if assumedTask.InitialPreemptAttemptTimestamp.IsZero() {
			assumedTask.InitialPreemptAttemptTimestamp = time.Now()
		}

		ddl := assumedTask.InitialPreemptAttemptTimestamp.Add(MaxPreemptionBackoffPeriodInSeconds * time.Second)
		if time.Now().After(ddl) {
			// add failed task
			// TODO: add more fine-grained control logic for removing tasks from unit info
			// for now, we only remove failed task/preemptor itself from unit info,
			// maybe we need to fail more assumed tasks since they may be affected by this failed preemptor
			returnErr = fmt.Errorf("victims of this pod: %v/%v can not been cleaned up within timeout duration (%v seconds)",
				pod.Namespace, pod.Name, MaxPreemptionBackoffPeriodInSeconds)
			checkPreemptionTraceContext.WithFields(tracing.WithErrorField(returnErr))

			// TODO: revisit this later
			// since we decide to (only) remove this failed preemptor from assumed tasks,
			// this failed preemptor will not affect the return result (allPass -> if all assumed task finishes the preemption)
			inProgress = false
			return
		}

		inProgress = true
		checkPreemptionTraceContext.WithFields(tracing.WithReasonField(fmt.Sprintf("preemption is still in progress, duration: %v seconds",
			metrics.SinceInSeconds(assumedTask.InitialPreemptAttemptTimestamp))))

	} else {
		checkPreemptionTraceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))
	}
	return
}

func (binder *Binder) CheckCrossNodePreemptionForUnit(ctx context.Context, unitInfo *bindingUnitInfo) error {
	// For AssumedTasks
	{
		// TODO: work in parallel
		var AnyOneStillInProgress bool
		for _, assumedTask := range unitInfo.GetAssumedTasks() {
			// if assumed tasks is not a preemptor, do not check anything since we have already done the check before
			if assumedTask.NominatedNode == nil {
				continue
			}

			complete, inProgress, err := checkCrossNodePreemptionForAssumedTask(binder.podLister, assumedTask)
			if err != nil {
				unitInfo.AddFailedTask(&runningUnitInfo{queuedPodInfo: assumedTask}, err, metrics.PreemptionTimeout, true)
			}

			if inProgress {
				AnyOneStillInProgress = true
			}

			if complete {
				metrics.PreemptVictimPodsCycleLatencyObserve(assumedTask.GetPodProperty(),
					metrics.SuccessResult, metrics.SinceInSeconds(assumedTask.InitialPreemptAttemptTimestamp))
			}

			if unitInfo.IsUnitFailed() {
				err := fmt.Errorf("unit checks fail at check cross node preemption for assumed tasks")
				unitInfo.MoveAllTasksToFailedList(err)
				return err
			}
		}

		if AnyOneStillInProgress {
			unitInfo.MoveAllAssumedTasksToWaitingList()
		} else {
			unitInfo.MoveAllAssumedTasksToReadyList()
		}
	}

	// For NewTasks
	{
		if unitInfo.NoVictim() {
			// no need for preemption
			return nil
		}
		commonState := framework.NewCycleState()
		for _, newTask := range unitInfo.GetNewTasks() {
			status := CheckPreemptionPhase(ctx, newTask, commonState)
			if status != nil {
				unitInfo.AddFailedTask(newTask,
					fmt.Errorf("fail to check preemption for pod: %v/%v, error: %v", newTask.queuedPodInfo.Pod.Namespace, newTask.queuedPodInfo.Pod.Name, status.AsError().Error()),
					metrics.CheckPreemptionFailure, false)
			}

			if unitInfo.IsUnitFailed() {
				err := fmt.Errorf("unit checks fail at check cross node preemption for new tasks")
				unitInfo.MoveAllTasksToFailedList(err)
				return err
			}
		}
	}

	return nil
}

func (binder *Binder) CheckSameNodeConflictsForUnit(ctx context.Context, unitInfo *bindingUnitInfo) error {
	// check conflicts in parallel
	// errCh := parallelize.NewErrorChannel()
	nodeList := unitInfo.GetNodeListOfNewTasks()
	piece := len(nodeList)
	if piece == 0 {
		return nil
	}

	parallelizeCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	checkTasks := func(i int) {
		defer func() {
			if unitInfo.IsUnitFailed() {
				cancel()
			}
		}()

		nodeName := nodeList[i]
		nodeInfo := binder.BinderCache.GetNodeInfo(nodeName)
		if nodeInfo == nil {
			// fail all tasks on that node
			unitInfo.MoveAllNewTasksOnNodeToFailedList(nodeName, fmt.Errorf("fail to get node info for node: %v", nodeName))
			return
		}

		clonedNodeInfo := nodeInfo.Clone()
		// remove all victims
		for _, victim := range unitInfo.GetVictimsOfNewTasksOnNode(nodeName) {
			if err := clonedNodeInfo.RemovePod(victim, true); err != nil {
				// TODO: only fail the preemptor this victim is selected for
				// fail all tasks on that node
				unitInfo.MoveAllNewTasksOnNodeToFailedList(nodeName, fmt.Errorf("fail to remove pod: %v/%v from node info (%v), error: %v", victim.Namespace, victim.Name, nodeName, err))
				return
			}
		}

		// check conflicts for new tasks on this node
		for _, task := range unitInfo.GetNewTasksOnNode(nodeName) {
			status := CheckConflictPhase(ctx, task, clonedNodeInfo)
			if status.IsSuccess() {
				// check successfully, add this new task to cloned node info for following checks
				assignMicroTopology(task.queuedPodInfo.Pod, clonedNodeInfo, task.State)
				clonedNodeInfo.AddPod(task.queuedPodInfo.Pod)
				cleanMicroTopology(task.queuedPodInfo.Pod)
				continue
			}

			if len(task.victims) > 0 {
				// if new task is a preemptor, fail all tasks on this node
				unitInfo.MoveAllNewTasksOnNodeToFailedList(nodeName, fmt.Errorf("fail to check conflict for pod: %v/%v, error: %v", task.queuedPodInfo.Pod.Namespace, task.queuedPodInfo.Pod.Name, status.AsError()))
				return
			} else {
				// new task if not a preemptor, just fail itself
				unitInfo.AddFailedTask(task, fmt.Errorf("fail to check conflict for pod: %v/%v, error: %v", task.queuedPodInfo.Pod.Namespace, task.queuedPodInfo.Pod.Name, status.AsError()),
					metrics.CheckConflictsEvaluation, false)
			}

			if unitInfo.IsUnitFailed() {
				return
			}
		}

		// all failed new tasks have already been moved to Failed list
		if unitInfo.NewTasksOnNodeNeedPreemption(nodeName) {
			unitInfo.MoveAllNewTasksOnNodeToWaitingList(nodeName)
		} else {
			unitInfo.MoveAllNewTasksOnNodeToReadyList(nodeName)
		}
	}

	parallelize.Until(parallelizeCtx, piece, checkTasks)
	return parallelizeCtx.Err()
}

func assignMicroTopology(pod *v1.Pod, nodeInfo framework.NodeInfo, state *framework.CycleState) {
	topo := nonnativeresource.AssignMicroTopology(nodeInfo, pod, state)
	if topo != "" {
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		pod.Annotations[podutil.MicroTopologyKey] = topo
		klog.V(4).InfoS("Get assigned micro-topology", "pod", podutil.GetPodKey(pod), "node", nodeInfo.GetNodeName(), "topo", topo)
	}
}

func cleanMicroTopology(pod *v1.Pod) {
	delete(pod.Annotations, podutil.MicroTopologyKey)
}

func (binder *Binder) MarkVictimsAndAssumeTasks(ctx context.Context, unitInfo *bindingUnitInfo) error {
	binder.markVictimsOfNewTasks(unitInfo)
	if unitInfo.IsUnitFailed() {
		return fmt.Errorf("unit failed at MarkVictimsAndAssumeTasks")
	}

	err := binder.assumeNewTasks(unitInfo)
	if err != nil {
		// this should be idempotent
		binder.forgetNewAssumedTasks(unitInfo)
		unitInfo.MoveAllNewTasksInReadyAndWaitingListToFailedList(err)
	}

	if unitInfo.IsUnitFailed() {
		return fmt.Errorf("unit failed at MarkVictimsAndAssumeTasks")
	}
	return nil
}

func (binder *Binder) markVictimsOfNewTasks(unitInfo *bindingUnitInfo) {
	// victims of assumed tasks should have been marked before, so don't need to mark again.
	// Note: we don't make it work in parallel because:
	// 1. MarkPodToDelete won't take too much time;
	// 2. cache has lock;
	// 3. golang will consume much CPU time to create and gc go routines;
	failedNodeMap := make(map[string]error)
	// TODO: quick return
	for _, cr := range unitInfo.GetWaitingTasks() {
		// is cr.assumed is false, runningUnit must be new task
		if !cr.assumed && len(cr.runningUnit.victims) > 0 && failedNodeMap[cr.runningUnit.suggestedNode] == nil {
			for _, victim := range cr.runningUnit.victims {
				if err := binder.BinderCache.MarkPodToDelete(victim, cr.runningUnit.queuedPodInfo.Pod); err != nil {
					markErr := fmt.Errorf("fail to mark victim: %v/%v for pod: %v/%v, error: %v", victim.Namespace, victim.Name,
						cr.runningUnit.queuedPodInfo.Pod.Namespace, cr.runningUnit.queuedPodInfo.Pod.Name, err)
					failedNodeMap[cr.runningUnit.suggestedNode] = markErr
					// all victims must be in the same node, so break here
					break
				}
			}
		}
	}

	// try to delete the markers from cache based on failed nodes
	for _, cr := range unitInfo.GetWaitingTasks() {
		if !cr.assumed && failedNodeMap[cr.runningUnit.suggestedNode] != nil {
			for _, victim := range cr.runningUnit.victims {
				// this should be idempotent
				// do not need to add error handling logic here, since cache will also remove pod marker periodically
				binder.BinderCache.RemoveDeletePodMarker(victim, cr.runningUnit.queuedPodInfo.Pod)
			}
		}
	}

	// move failed tasks to failed list
	for nodeName, err := range failedNodeMap {
		unitInfo.MoveAllNewTasksOnNodeFromWaitingToFailedList(nodeName, err)
	}
}

func (binder *Binder) assumeNewTasks(unitInfo *bindingUnitInfo) error {
	// TODO: fine-grained control logic
	// new preemptor: fail all new tasks on the same node
	// new non-preemptor: fail itself
	// for now, in most cases, all member = min member, so, returning directly when running into an error is ok
	// revisit later
	for _, cr := range unitInfo.GetReadyAndWaitingTasks() {
		if cr.assumed {
			continue
		}

		podTrace := cr.runningUnit.getSchedulingTrace()
		traceContext := podTrace.NewTraceContext(tracing.RootSpan, tracing.BinderAssumeTaskSpan)

		err := func(cr *checkResult) error {
			if cr.runningUnit.clonedPod == nil {
				cr.runningUnit.clonedPod = cr.runningUnit.queuedPodInfo.Pod.DeepCopy()
			}

			// Assume volumes first before assuming the pod.
			// If all volumes are completely bound, then allBound is true and volume binding will be skipped.
			// Otherwise, binding of volumes is started after the pod is assumed, but before pod binding.
			// TODO: figure out how to roll back assumed volumes
			allBound, err := binder.handle.VolumeBinder().AssumePodVolumes(cr.runningUnit.clonedPod, cr.runningUnit.suggestedNode)
			if err != nil {
				return fmt.Errorf("fail to assume pod volumes for pod: %v/%v, error: %v", cr.runningUnit.clonedPod.Namespace, cr.runningUnit.clonedPod.Name, err)
			}

			cr.runningUnit.clonedPod.Spec.NodeName = cr.runningUnit.suggestedNode
			podInfo := framework.MakeCachePodInfoWrapper().Pod(cr.runningUnit.clonedPod).CycleState(cr.runningUnit.State).Obj()
			if err := binder.BinderCache.AssumePod(podInfo); err != nil {
				return fmt.Errorf("fail to assume pod: %v/%v, error: %v", cr.runningUnit.clonedPod.Namespace, cr.runningUnit.clonedPod.Name, err)
			}

			// set NewlyAssumedButStillInHandling to true, it will not affect the result even if we can bind these pods directly
			cr.runningUnit.queuedPodInfo.NewlyAssumedButStillInHandling = true
			cr.runningUnit.queuedPodInfo.AllVolumeBound = allBound
			cr.runningUnit.queuedPodInfo.ReservedPod = cr.runningUnit.clonedPod
			return nil
		}(cr)
		if err != nil {
			traceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
			traceContext.WithFields(tracing.WithErrorField(err))
			tracing.AsyncFinishTraceContext(traceContext, time.Now())
			return err
		}

		traceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))
		tracing.AsyncFinishTraceContext(traceContext, time.Now())
	}
	return nil
}

func (binder *Binder) forgetNewAssumedTasks(unitInfo *bindingUnitInfo) {
	for _, cr := range unitInfo.GetReadyAndWaitingTasks() {
		// only forget new tasks which have been assumed
		if cr.assumed || !cr.runningUnit.queuedPodInfo.NewlyAssumedButStillInHandling {
			continue
		}
		if cr.runningUnit.queuedPodInfo.ReservedPod != nil {
			// this should be idempotent
			podInfo := framework.MakeCachePodInfoWrapper().Pod(cr.runningUnit.queuedPodInfo.ReservedPod).Obj()
			if err := binder.BinderCache.ForgetPod(podInfo); err == nil {
				cr.runningUnit.queuedPodInfo.NewlyAssumedButStillInHandling = false
				cr.runningUnit.queuedPodInfo.AllVolumeBound = false
				cr.runningUnit.queuedPodInfo.ReservedPod = nil
			}
		}
	}
}

// all tasks (in ready and waiting list) are assumed (new tasks are also added to cache) when coming here
func (binder *Binder) DeleteVictimsAndBindTasks(ctx context.Context, unitInfo *bindingUnitInfo) error {
	queuedUnitInfo := unitInfo.queuedUnitInfo
	klog.V(4).InfoS("Deleted victims and bind tasks for unit", "unitKey", queuedUnitInfo.GetKey())
	defer func() {
		attemptedTasks := unitInfo.allMember - len(unitInfo.ignoredTasks)
		if unitInfo.IsUnitFailed() {
			metrics.ObserveUnitBindingAttempts(queuedUnitInfo, 0, attemptedTasks)
			return
		}

		successfulTasks := len(unitInfo.readyTasks)
		// TODO: make sure whether waiting tasks should be counted as failed tasks
		failedTasks := attemptedTasks - successfulTasks
		metrics.ObserveUnitBindingAttempts(queuedUnitInfo, successfulTasks, failedTasks)
		metrics.BinderUnitE2ELatencyObserve(queuedUnitInfo.GetUnitProperty(), metrics.SinceInSeconds(queuedUnitInfo.InitialAttemptTimestamp))
	}()

	// delete victims
	nodeToErr := binder.deleteVictimsOfNewTasks(ctx, unitInfo)
	if len(nodeToErr) > 0 {
		for nodeName, err := range nodeToErr {
			unitInfo.MoveAllNewTasksOnNodeFromWaitingToFailedList(nodeName, err)
		}
	}

	if unitInfo.IsUnitFailed() {
		err := fmt.Errorf("unit fails after deleteVictimsOfNewTasks, at DeleteVictimsAndBindTasks")
		unitInfo.MoveAllTasksToFailedList(err)
		binder.FailAndRejectAllTasks(unitInfo, err)
		return err
	}

	// bind
	if unitInfo.IsAbleToBindReadyTasks() {
		// Lock the podgroup to prevent it from going into a timeout state.
		if unitInfo.queuedUnitInfo.Type() == framework.PodGroupUnitType {
			if err := binderutils.LockPodGroupStatus(binder.handle.CRDClientSet(), binder.pgLister, unitInfo.queuedUnitInfo.ScheduleUnit, "binder"); err != nil {
				klog.ErrorS(err, "Failed to lock pod group object for unit", "unitKey", unitInfo.queuedUnitInfo.GetKey())
				unitInfo.MoveAllTasksToFailedList(err)
				binder.FailAndRejectAllTasks(unitInfo, err)
				return err
			}
		}

		failedTasks := binder.bindTasks(ctx, unitInfo)
		unitInfo.MoveTasksFromReadyToFailedList(failedTasks)
		if unitInfo.IsUnitFailed() {
			err := fmt.Errorf("unit fails after bindTasks")
			unitInfo.MoveAllTasksToFailedList(err)
			binder.FailAndRejectAllTasks(unitInfo, err)
			return err
		}
		// Currently, we only used PodGroupUnit scheduled result, so we don't need to update SinglePodUnit.
		if unitInfo.queuedUnitInfo.Type() == framework.PodGroupUnitType {
			binder.BinderCache.SetUnitSchedulingStatus(unitInfo.queuedUnitInfo.GetKey(), status.ScheduledStatus)
		}
	} else {
		// can not bind tasks directly, move all ready tasks to waiting list
		unitInfo.MoveAllTasksFromReadyToWaitingList()
	}

	// unit doesn't fail until now, we need to reject all failed tasks and re-enqueue all waiting tasks if there are any
	// re-enqueue waiting tasks
	// for now, all waiting tasks must have been assumed (new tasks are assumed at MarkVictimsAndAssumeTasks)
	binder.ReEnqueueWaitingTasks(unitInfo)
	// reject failed tasks
	binder.RejectFailedTasks(unitInfo)
	return nil
}

func (binder *Binder) ReEnqueueWaitingTasks(unitInfo *bindingUnitInfo) {
	if len(unitInfo.GetWaitingTasks()) > 0 {
		unit := unitInfo.queuedUnitInfo
		unit.ResetPods()

		for _, cr := range unitInfo.GetWaitingTasks() {
			queuedPodInfo := cr.runningUnit.queuedPodInfo
			if !cr.assumed && (!queuedPodInfo.NewlyAssumedButStillInHandling || queuedPodInfo.ReservedPod == nil) {
				klog.ErrorS(nil, "Task in waiting list was not assumed, this should not happen, there MUST be something wrong with binder workflow")
				klog.FlushAndExit(klog.ExitFlushTimeout, 1)
			}
			unit.AddPod(queuedPodInfo)
			// reset queue span
			queuedPodInfo.QueueSpan = tracing.NewSpanInfo(queuedPodInfo.GetPodProperty().ConvertToTracingTags())
		}

		binder.BinderQueue.AddUnitPreemptor(unit)
	}
}

// TODO: emit events ?
func (binder *Binder) FailAndRejectAllTasks(unitInfo *bindingUnitInfo, err error) {
	// move all tasks to failed list
	unitInfo.MoveAllTasksToFailedList(err)

	binder.RejectFailedTasks(unitInfo)
}

// TODO: figure out what can we do if fail to reject tasks
func (binder *Binder) RejectFailedTasks(unitInfo *bindingUnitInfo) {
	if len(unitInfo.failedTasks) == 0 {
		return
	}

	// reject all failed tasks
	// TODO: figure out if we need to print the error message
	// since we will try to delete all markers and forget all pods,
	// some of them may have already been reset, so some error message may be misleading
	for _, cr := range unitInfo.GetFailedTasks() {
		// reset cache
		if len(cr.runningUnit.victims) > 0 {
			// delete all pod markers
			// TODO: fine-grained control
			for _, victim := range cr.runningUnit.victims {
				binder.BinderCache.RemoveDeletePodMarker(victim, cr.runningUnit.queuedPodInfo.Pod)
			}
		}
		if cr.assumed || cr.runningUnit.queuedPodInfo.NewlyAssumedButStillInHandling {
			if cr.runningUnit.queuedPodInfo.ReservedPod != nil {
				podInfo := framework.MakeCachePodInfoWrapper().Pod(cr.runningUnit.queuedPodInfo.ReservedPod).Obj()
				binder.BinderCache.ForgetPod(podInfo)
			}
		}
		// reject pod
		binder.Error(cr.runningUnit.queuedPodInfo, cr.err)
		if err := binderutils.CleanupPodAnnotations(binder.handle.ClientSet(), cr.runningUnit.queuedPodInfo.Pod); err != nil {
			klog.InfoS("Failed to clean up pod annotations", "pod", klog.KObj(cr.runningUnit.queuedPodInfo.Pod), "err", err)
			binder.reconciler.AddFailedTask(&APICallFailedTask{
				reason: RejectFailed,
				qpi:    cr.runningUnit.queuedPodInfo,
			})
		}
		// TODO: emit event for each pod ? will that increase the API burden and be throttled, um-mm ?
		binder.recorder.Eventf(cr.runningUnit.queuedPodInfo.Pod, nil, v1.EventTypeWarning, "FailedTasks", "Rejecting", helper.TruncateMessage(cr.err.Error()))
	}

	if unitInfo.queuedUnitInfo.Type() == framework.PodGroupUnitType {
		// send event to pod group object
		pg, err := binder.pgLister.PodGroups(unitInfo.queuedUnitInfo.GetNamespace()).Get(unitInfo.queuedUnitInfo.GetName())
		if err != nil {
			klog.InfoS("Failed to get pod group object for unit", "unitKey", unitInfo.queuedUnitInfo.GetKey(),
				"podGroup", klog.KRef(unitInfo.queuedUnitInfo.GetNamespace(), unitInfo.queuedUnitInfo.GetName()),
				"err", err)
			return
		}

		errMes := fmt.Errorf("reject failed tasks for unit: %v, detailed failed reasons are attached to tasks(pods); unit checking result is: all member: %v, mim member: %v, ready: %v, waiting: %v, failed tasks: %v",
			unitInfo.queuedUnitInfo.GetKey(), unitInfo.allMember, unitInfo.minMember, len(unitInfo.GetReadyTasks()), len(unitInfo.GetWaitingTasks()), len(unitInfo.GetFailedTasks()))
		binder.recorder.Eventf(pg, nil, v1.EventTypeWarning, "FailedTasks", "Rejecting", helper.TruncateMessage(errMes.Error()))
	}
}

func (binder *Binder) RejectTimeOutUnit(unit *framework.QueuedUnitInfo) {
	// try to delete pod marker and forget pod
	// it doesn't matter if these operation fails, binder cache has TTL mechanism to clean up these info from cache
	for _, qp := range unit.GetPods() {
		err := fmt.Errorf("unit timeout")
		binder.Error(qp, err)
		if err := binderutils.CleanupPodAnnotations(binder.handle.ClientSet(), qp.Pod); err != nil {
			klog.InfoS("Failed to clean up pod annotations", "pod", klog.KObj(qp.Pod), "err", err)
			binder.reconciler.AddFailedTask(&APICallFailedTask{
				reason: RejectFailed,
				qpi:    qp,
			})
		}
		binder.recorder.Eventf(qp.Pod, nil, v1.EventTypeWarning, "FailedTasks", "Rejecting", err.Error())
	}
}

// ------------------------------ Internal Functions ------------------------------

func (binder *Binder) UnitTimeout(unit *framework.QueuedUnitInfo) bool {
	timeout := unit.GetTimeoutPeriod()
	if timeout != 0 {
		// use creation time stamp to check if unit times out
		// TODO: revisit later
		deadline := unit.GetCreationTimestamp().Add(time.Duration(timeout) * time.Second)
		if time.Now().After(deadline) && binder.BinderCache.GetUnitSchedulingStatus(unit.GetKey()) != status.ScheduledStatus {
			return true
		}
	}
	return false
}

func (binder *Binder) skipCheckingPod(pod *v1.Pod) bool {
	// check if pod is being deleted
	if pod == nil || pod.DeletionTimestamp != nil {
		return true
	}

	// check if pod is assumed
	isAssumed, err := binder.BinderCache.IsAssumedPod(pod)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to check whether pod %s/%s is assumed: %v", pod.Namespace, pod.Name, err))
		return false
	}
	return isAssumed || podutil.BoundPod(pod)
}

func (binder *Binder) getVictimsToPreempt(victimPods framework.VictimPods) ([]*v1.Pod, error) {
	victimPodsToPreempt := []*v1.Pod{}
	for _, victimPod := range victimPods {
		// TODO: retry logic ?
		vp, err := binder.podLister.Pods(victimPod.Namespace).Get(victimPod.Name)
		if errors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("fail to get victim pod: %v/%v with error: %v", victimPod.Namespace, victimPod.Name, err)
		}
		if flag, err := binder.BinderCache.IsPodMarkedToDelete(vp); err != nil || flag {
			return nil, fmt.Errorf("victim pod does not exist in cache or is marked to be deleted: %v/%v with error: %v",
				victimPod.Namespace,
				victimPod.Name,
				err)
		}
		victimPodsToPreempt = append(victimPodsToPreempt, vp)
	}

	return victimPodsToPreempt, nil
}

func (binder *Binder) bindTasks(ctx context.Context, unitInfo *bindingUnitInfo) map[types.UID]error {
	failedTaskToError := make(map[types.UID]error)
	var failedTasksLock sync.Mutex

	taskList := unitInfo.GetReadyTasks()
	newCtx, _ := context.WithCancel(ctx)
	pieces := len(taskList)

	bindTask := func(task *runningUnitInfo) bool {
		// this should not happen, print error message just in case
		if task == nil || task.queuedPodInfo == nil || task.queuedPodInfo.ReservedPod == nil {
			klog.ErrorS(nil, "Empty queued pod info or reserved pod in running unit, this should not happen",
				"queuedPodInfo", task.queuedPodInfo, "unit", unitInfo.queuedUnitInfo.UnitKey)
			return false
		}

		if err := util.Retry(MaxRetryAttempts, time.Second, func() error {
			// reservedPod was set while assuming pod , so in assumedPodInfo,
			// it will not be nil.
			subCtx, cancel := context.WithCancel(newCtx)
			defer cancel()
			defer func() {
				// TODO: actually we don't need this if everything is ok
				binder.BinderCache.FinishBinding(task.queuedPodInfo.ReservedPod)
			}()

			if task.Framework == nil {
				// ATTENTION: This is an in-handling pod.
				// Considering that NewlyAssumedButStillInHandling is true, error should not occur here.
				fwk, _ := binder.handle.GetFrameworkForPod(task.queuedPodInfo.Pod)
				task.Framework = fwk
			}

			if status := BindPhase(subCtx, binder.handle, task); !status.IsSuccess() {
				return status.AsError()
			}

			// emit bind event
			binder.recorder.Eventf(task.queuedPodInfo.ReservedPod, nil, v1.EventTypeNormal, "Bind", "Binding", "Successfully assigned %v to %v", podutil.GetPodKey(task.queuedPodInfo.ReservedPod), task.suggestedNode)

			return nil
		}); err != nil {
			failedTasksLock.Lock()
			failedTaskToError[task.queuedPodInfo.Pod.UID] = err
			failedTasksLock.Unlock()
			return false
		}
		return true
	}

	parallelize.Until(newCtx, pieces, func(i int) {
		task := taskList[i].runningUnit
		success := bindTask(task)
		if success {
			metrics.ObservePodBinderE2ELatency(task.queuedPodInfo)
			metrics.ObservePodGodelE2E(task.queuedPodInfo)
		}
	})
	return failedTaskToError
}

func deleteVictimsForTask(cli clientset.Interface, task *runningUnitInfo) (returnErr error) {
	podTrace := task.getSchedulingTrace()
	traceContext := podTrace.NewTraceContext(tracing.RootSpan, tracing.BinderDeleteVictimsSpan)
	traceContext.WithFields(tracing.WithNominatedNodeField(task.queuedPodInfo.NominatedNode.Marshall()))

	defer func() {
		tracing.AsyncFinishTraceContext(traceContext, time.Now())
	}()

	defer func() {
		if returnErr != nil {
			traceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
			traceContext.WithFields(tracing.WithErrorField(returnErr))
			return
		}
		tracing.WithResultTag(tracing.ResultSuccess)
	}()

	for _, victim := range task.victims {
		err := util.Retry(MaxRetryAttempts, time.Second, func() error {
			if err := util.DeletePod(cli, victim); err != nil && !errors.IsNotFound(err) {
				// if error is not found (victim is deleted), don't return error
				return err
			}
			return nil
		})

		metrics.IncPreemptingAttempts(task.queuedPodInfo.GetPodProperty(), err == nil)
		if err != nil {
			returnErr = fmt.Errorf("fail to delete victims for pod: %v/%v, error: %v", task.queuedPodInfo.Pod.Namespace, task.queuedPodInfo.Pod.Name, err)
			return
		}
	}
	return nil
}

func (binder *Binder) deleteVictimsOfNewTasks(ctx context.Context, unitInfo *bindingUnitInfo) map[string]error {
	newPreemptors := make([]*runningUnitInfo, 0)
	for _, cr := range unitInfo.GetWaitingTasks() {
		if !cr.assumed && len(cr.runningUnit.victims) > 0 {
			newPreemptors = append(newPreemptors, cr.runningUnit)
		}
	}
	if len(newPreemptors) <= 0 {
		return nil
	}

	newCtx, _ := context.WithCancel(ctx)
	pieces := len(newPreemptors)
	var failedNodeLock sync.Mutex
	failedNodeMap := make(map[string]error)

	deleteVictims := func(i int) {
		preemptor := newPreemptors[i]
		// delete victims
		err := deleteVictimsForTask(binder.handle.ClientSet(), preemptor)
		if err != nil {
			failedNodeLock.Lock()
			failedNodeMap[preemptor.suggestedNode] = err
			failedNodeLock.Unlock()
			return
		}
	}

	parallelize.Until(newCtx, pieces, deleteVictims)
	return failedNodeMap
}

func ValidateUnit(unit *framework.QueuedUnitInfo) error {
	if unit == nil {
		return fmt.Errorf("empty unit")
	} else if len(unit.GetPods()) == 0 {
		return fmt.Errorf("no pods in this unit")
	}
	if _, err := unit.GetMinMember(); err != nil {
		return err
	}
	return nil
}

func IsPreemptionComplete(podLister corelisters.PodLister, victimPods framework.VictimPods) bool {
	for _, victimPod := range victimPods {
		gotPod, err := podLister.Pods(victimPod.Namespace).Get(victimPod.Name)
		if err == nil {
			if gotPod.UID == types.UID(victimPod.UID) {
				return false
			}
		} else if !errors.IsNotFound(err) {
			return false
		}
	}

	return true
}
