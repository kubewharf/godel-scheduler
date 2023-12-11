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

package controller

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	schedv1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	pgclientset "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	schedinformer "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions/scheduling/v1alpha1"
	pglister "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corelister "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	frameworkruntime "github.com/kubewharf/godel-scheduler/pkg/binder/framework/runtime"
	"github.com/kubewharf/godel-scheduler/pkg/binder/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	cmdutil "github.com/kubewharf/godel-scheduler/pkg/util/cmd"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	unitutil "github.com/kubewharf/godel-scheduler/pkg/util/unit"
)

const (
	PodGroupWorkers = 4

	AddPodGroupEventFmt    = "AddPodGroupEvent"
	UpdatePodGroupEventFmt = "UpdatePodGroupEvent"
	DeletePodGroupEventFmt = "DeletePodGroupEvent"

	AddPodEventFmt    = "AddPodEvent(%v)"
	UpdatePodEventFmt = "UpdatePodEvent(%v)"
	DeletePodEventFmt = "DeletePodEvent(%v)"

	EventReason = "PGController"
)

// PodGroupController is a controller that process pod groups using provided Handler interface
type PodGroupController struct {
	eventRecorder   record.EventRecorder
	pgQueue         workqueue.RateLimitingInterface
	pgLister        pglister.PodGroupLister
	podLister       corelister.PodLister
	pgListerSynced  cache.InformerSynced
	podListerSynced cache.InformerSynced
	pgClient        pgclientset.Interface
}

// SetupPodGroupController returns a new *PodGroupController
func SetupPodGroupController(
	ctx context.Context,
	client kubernetes.Interface,
	pgClient pgclientset.Interface,
	pgInformer schedinformer.PodGroupInformer,
) {
	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&corev1.EventSinkImpl{Interface: client.CoreV1().Events(v1.NamespaceAll)})
	podInformer := cmdutil.NewLabelIndexedPodInformer(client, 0)

	ctrl := &PodGroupController{
		eventRecorder: broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "PodGroupController"}),
		// TODO: default controller rate limiter is not that suitable for pod group queue
		pgQueue: workqueue.NewNamedRateLimitingQueue(
			workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 300*time.Second),
			"PodGroup",
		),
	}

	pgInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { ctrl.pgTrigger(obj, AddPodGroupEventFmt) },
		UpdateFunc: func(_, obj interface{}) { ctrl.pgTrigger(obj, UpdatePodGroupEventFmt) },
		DeleteFunc: func(obj interface{}) { ctrl.pgTrigger(obj, DeletePodGroupEventFmt) },
	})

	podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { ctrl.podTrigger(obj, AddPodEventFmt) },
		UpdateFunc: func(_, obj interface{}) { ctrl.podTrigger(obj, UpdatePodEventFmt) },
		DeleteFunc: func(obj interface{}) { ctrl.podTrigger(obj, DeletePodEventFmt) },
	})
	ctrl.pgLister = pgInformer.Lister()
	ctrl.podLister = podInformer.Lister()
	ctrl.pgListerSynced = pgInformer.Informer().HasSynced
	ctrl.podListerSynced = podInformer.Informer().HasSynced
	ctrl.pgClient = pgClient

	go podInformer.Informer().Run(ctx.Done())
	go ctrl.Run(PodGroupWorkers, ctx.Done())
}

// Run starts listening on channel events
func (ctrl *PodGroupController) Run(workers int, stopCh <-chan struct{}) {
	defer ctrl.pgQueue.ShutDown()

	klog.InfoS("Starting PodGroup controller")
	defer klog.InfoS("Shutting PodGroup controller")

	if !cache.WaitForCacheSync(stopCh, ctrl.pgListerSynced, ctrl.podListerSynced) {
		klog.InfoS("Failed sync caches")
		return
	}
	klog.InfoS("Finished syncing Pod Group Controller")
	for i := 0; i < workers; i++ {
		go wait.Until(ctrl.worker, time.Second, stopCh)
	}

	<-stopCh
}

func (ctrl *PodGroupController) pgAdded(pg *schedv1alpha1.PodGroup, event string) {
	if unitutil.PodGroupFinalState(pg.Status.Phase) {
		return
	}
	if key := unitutil.GetPodGroupKey(pg); len(key) > 0 {
		klog.V(4).InfoS("Triggered PodGroup Controller", "key", key, "event", event)
		ctrl.pgQueue.Add(key)
	}
}

// pgTrigger reacts to a PodGroup creation/update
func (ctrl *PodGroupController) pgTrigger(obj interface{}, eventType string) {
	pg, ok := obj.(*schedv1alpha1.PodGroup)
	if !ok {
		klog.InfoS("Failed to convert obj to *v1alpha1.PodGroup", "object", obj)
		return
	}
	ctrl.pgAdded(pg, eventType)
}

// podTrigger reacts to a Pod creation/update/deletion
func (ctrl *PodGroupController) podTrigger(obj interface{}, eventType string) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.InfoS("Failed to convert obj to *v1.Pod", "object", obj, "err", err)
		return
	}
	event := fmt.Sprintf(eventType, podutil.GetPodKey(pod))

	pgName := unitutil.GetPodGroupName(pod)
	if len(pgName) == 0 {
		return
	}
	pg, err := ctrl.pgLister.PodGroups(pod.Namespace).Get(pgName)
	if err != nil {
		// Ignore error. Pod Group might be deleted.
		if apierrs.IsNotFound(err) {
			klog.InfoS("Failed to find pod group", "err", err)
			return
		}
		klog.InfoS("Failed to get pod group while pod trigger", "pod", podutil.GetPodKey(pod), "event", event, "err", err)
		return
	}
	ctrl.pgAdded(pg, event)
}

func (ctrl *PodGroupController) worker() {
	for ctrl.processNextWorkItem() {
	}
}

// processNextWorkItem deals with one key off the queue.  It returns false when it's time to quit.
func (ctrl *PodGroupController) processNextWorkItem() bool {
	keyObj, quit := ctrl.pgQueue.Get()
	if quit {
		return false
	}
	defer ctrl.pgQueue.Done(keyObj)

	klog.V(5).InfoS("Trying to process item", "item", keyObj)
	key, ok := keyObj.(string)
	if !ok {
		ctrl.pgQueue.Forget(keyObj)
		runtime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", keyObj))
		return true
	}
	if reEnqueue := ctrl.syncHandler(key); reEnqueue {
		ctrl.pgQueue.AddRateLimited(key)
	}
	return true
}

type Msg int

const (
	CreatedUnsatisfied   Msg = iota // created < min
	ScheduledUnsatisfied            // created >= min && scheduled < min
	ScheduledSatisfied              // created >= min && scheduled >= min || PodRunning || PodSucceeded || PodFailed
	TimeoutSatisfied                // PodGroup timeout since create timestamp
)

func (ctrl *PodGroupController) syncHandler(key string) bool {
	klog.V(4).InfoS("Started to handle PodGroup", "podGroupKey", key)
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		klog.InfoS("Invalid PodGroup key", "podGroupKey", key, "err", err)
		// if something is wrong here, it will always be invalid key, re-enqueue won't make any difference,
		// so, don't enqueue this key again.
		return false
	}
	pg, err := ctrl.pgLister.PodGroups(namespace).Get(name)
	if apierrs.IsNotFound(err) {
		klog.V(4).InfoS("Deleted PodGroup", "podGroupKey", key, "err", err)
		return false
	}
	if err != nil {
		klog.InfoS("Failed to retrieve pod group from informer local store", "podGroupKey", key, "err", err)
		return true
	}
	// Quick check.
	if unitutil.PodGroupFinalState(pg.Status.Phase) {
		klog.V(4).InfoS("PodGroup has already ever reached the final state, shouldn't change any more", "podGroupKey", key, "phase", pg.Status.Phase)
		return false
	}

	pgCopy := pg.DeepCopy()

	var msg Msg
	var eventMsg string
	var pods []*v1.Pod
	{
		pods, err = GetAllPods(ctrl.podLister, namespace, name)
		if err != nil {
			klog.InfoS("Failed to list pods for podGroup", "podGroupKey", key, "err", err)
			return true
		}

		if len(pods) > 0 {
			fillOccupiedObj(pgCopy, pods[0])
		}

		// Normally, we decide which state to move to based on the pod's binding count.
		// There is a special case:
		//	   If there is a pod Running/Succeeded/Failed, it is forced to the `Scheduled` state.
		// 	   And we use `overWriteScheduled` to identify this special case.
		var created, scheduled int32
		var overWriteScheduled bool
		var uninitialized, pending, dispatched, assumed int32
		created = int32(len(pods))
		for _, pod := range pods {
			{
				if podutil.BoundPod(pod) {
					scheduled++
				}
				if !overWriteScheduled &&
					(pod.Status.Phase == v1.PodRunning || pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed) {
					overWriteScheduled = true
				}
			}
			{
				switch podutil.GetPodState(pod.Annotations) {
				case podutil.PodNotInit:
					uninitialized++
				case podutil.PodPending:
					pending++
				case podutil.PodDispatched:
					dispatched++
				case podutil.PodAssumed:
					assumed++
				}
			}
		}

		minMember := pgCopy.Spec.MinMember
		if created < minMember {
			msg = CreatedUnsatisfied
		} else {
			if scheduled < minMember {
				msg = ScheduledUnsatisfied
			} else {
				msg = ScheduledSatisfied
			}
		}
		if overWriteScheduled {
			msg = ScheduledSatisfied
		}

		eventMsg = fmt.Sprintf("overWriteScheduled=%v;created=%v,scheduled=%v;uninitialized=%v,pending=%v,dispatched=%v,assumed=%v", overWriteScheduled, created, scheduled, uninitialized, pending, dispatched, assumed)

		// TODO: Interpretability enhancement
		klog.V(4).InfoS("Analyzed pod states for PodGroup", "podGroupKey", key, "minMember", minMember, "eventMsg", eventMsg)

		debugModeOn := pgCopy.Annotations != nil && pgCopy.Annotations[util.DebugModeAnnotationKey] == util.DebugModeOn
		if debugModeOn {
			var createdPods, scheduledPods []string
			for _, pod := range pods {
				podKey := podutil.GetPodKey(pod)
				createdPods = append(createdPods, podKey)
				if podutil.BoundPod(pod) {
					scheduledPods = append(scheduledPods, podKey)
				}
			}
			klog.InfoS("DEBUG: PodGroup statistics", "podGroupKey", key, "created", created, "scheduled", scheduled, "createdPods", createdPods, "scheduledPods", scheduledPods)
		}
	}

	Step := func(pgCopy *schedv1alpha1.PodGroup, m Msg) schedv1alpha1.PodGroupPhase {
		curPhase := pgCopy.Status.Phase
		if curPhase == "" {
			curPhase = schedv1alpha1.PodGroupPending
			updatePodGroupCondition(pgCopy, curPhase, "")
		}
		{
			// TODO: remove this compatible adaptation logic
			if curPhase == schedv1alpha1.PodGroupRunning || curPhase == schedv1alpha1.PodGroupFinished || curPhase == schedv1alpha1.PodGroupFailed {
				// Treat it as scheduled because there is at least one pod that is bound to the node.
				phaseBefore := curPhase
				curPhase = schedv1alpha1.PodGroupScheduled
				updatePodGroupCondition(pgCopy, curPhase, fmt.Sprintf("Change %v to scheduled for compatibility", phaseBefore))
			}
		}

		switch curPhase {
		case schedv1alpha1.PodGroupPending:
			switch m {
			case ScheduledUnsatisfied:
				// TODO: If allowed to move from PreScheduling to Pending, should the ScheduleStartTime be refreshed when moving back to PreScheduling?
				// if pgCopy.Status.ScheduleStartTime == nil {
				// 	pgCopy.Status.ScheduleStartTime = &metav1.Time{Time: time.Now()}
				// }
				pgCopy.Status.ScheduleStartTime = &metav1.Time{Time: time.Now()}
				updatePodGroupCondition(pgCopy, schedv1alpha1.PodGroupPreScheduling, fmt.Sprintf("More than %v pods has been created but not fully scheduled", pgCopy.Spec.MinMember))
				return schedv1alpha1.PodGroupPreScheduling
			case ScheduledSatisfied:
				updatePodGroupCondition(pgCopy, schedv1alpha1.PodGroupScheduled, fmt.Sprintf("More than %v pods has been scheduled", pgCopy.Spec.MinMember))
				return schedv1alpha1.PodGroupScheduled
			}
		case schedv1alpha1.PodGroupPreScheduling:
			switch m {
			case CreatedUnsatisfied:
				updatePodGroupCondition(pgCopy, schedv1alpha1.PodGroupPending, fmt.Sprintf("Less than %v pods has been created", pgCopy.Spec.MinMember))
				return schedv1alpha1.PodGroupPending
			case ScheduledSatisfied:
				updatePodGroupCondition(pgCopy, schedv1alpha1.PodGroupScheduled, fmt.Sprintf("More than %v pods has been scheduled", pgCopy.Spec.MinMember))
				return schedv1alpha1.PodGroupScheduled
			}
		case schedv1alpha1.PodGroupScheduled, schedv1alpha1.PodGroupTimeout:
		default:
			klog.InfoS("Got unexpected phase for PodGroup", "phase", curPhase, "podGroupKey", key)
		}
		return curPhase
	}

	nextPhase := Step(pgCopy, msg)
	// PopulatePodGroupFinalOp must be called at the end.
	if !unitutil.PodGroupFinalState(nextPhase) && podGroupTimeout(pgCopy) && unitutil.PopulatePodGroupFinalOp(pgCopy, "pg-controller") {
		updatePodGroupCondition(pgCopy, schedv1alpha1.PodGroupTimeout, fmt.Sprintf("Can't schedule pods for %s before timeout", key))
		nextPhase = schedv1alpha1.PodGroupTimeout
	}

	pgCopy.Status.Phase = nextPhase
	updated, err := ctrl.updatePodGroup(pg, pgCopy, eventMsg)
	if updated && err == nil {
		if len(pods) > 0 && nextPhase == schedv1alpha1.PodGroupScheduled {
			scheduleStartTime := GetPodGroupScheduleStartTime(pg)
			if !scheduleStartTime.IsZero() {
				metricsLabels := api.ExtractPodProperty(pods[0]).ConvertToMetricsLabels()
				metrics.PodGroupE2ELatencyObserve(metricsLabels, helper.SinceInSeconds(scheduleStartTime.Time))
			}
		}
	}

	return !(err == nil && unitutil.PodGroupFinalState(nextPhase))
}

func podGroupTimeout(pgCopy *schedv1alpha1.PodGroup) bool {
	var timeoutDuration time.Duration
	if pgCopy.Spec.ScheduleTimeoutSeconds != nil {
		timeoutDuration = time.Duration(*pgCopy.Spec.ScheduleTimeoutSeconds) * time.Second
	} else {
		timeoutDuration = frameworkruntime.DefaultGangTimeout
	}

	if time.Since(pgCopy.CreationTimestamp.Time) > timeoutDuration {
		klog.V(5).InfoS("Pod group timeout", "podGroupKey", unitutil.GetPodGroupKey(pgCopy), "timeout period", timeoutDuration)
		return true
	}
	klog.V(5).InfoS("Pod group is not timeout", "podGroupKey", unitutil.GetPodGroupKey(pgCopy))
	return false
}

// TODO: add more details about pod states (dispatched / scheduled / etc...)
func updatePodGroupCondition(pgCopy *schedv1alpha1.PodGroup, phase schedv1alpha1.PodGroupPhase, msg string) {
	condition := &schedv1alpha1.PodGroupCondition{
		Phase:              phase,
		Status:             v1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             string(phase),
		Message:            msg,
	}

	// Update condition to the new condition.
	// It only update existing condition to avoid keep appending new conditions
	// TODO: revisit this since we may transition back and forth between several states， checking time is not that explicit
	for i, c := range pgCopy.Status.Conditions {
		if c.Phase == condition.Phase {
			pgCopy.Status.Conditions[i] = *condition
			return
		}
	}
	pgCopy.Status.Conditions = append(pgCopy.Status.Conditions, *condition)
}

func (ctrl *PodGroupController) updatePodGroup(old, new *schedv1alpha1.PodGroup, eventMsg string) (updated bool, err error) {
	// We may update annotation as well, so compare the whole PodGroup object.
	if !reflect.DeepEqual(old, new) {
		// Only update PodGroup when the Annotation has been modified.
		// ATTENTION: If ApiServer has watch-lag enabled, even if the Annotation of the object has not changed its ResourceVersion will be
		// increased, so the latest object should always be used.
		if !reflect.DeepEqual(old.Annotations, new.Annotations) {
			latest, err := ctrl.pgClient.SchedulingV1alpha1().PodGroups(new.Namespace).Update(context.TODO(), new, metav1.UpdateOptions{})
			if err != nil {
				klog.ErrorS(err, "Failed to update pod group annotation",
					"podGroupKey", unitutil.GetPodGroupKey(new))
				return false, err
			}
			latest.Status = *new.Status.DeepCopy()
			new = latest
		}

		if old.Status.Phase == new.Status.Phase {
			klog.V(4).InfoS("Skipped update podGroup to new status cause phase unchanged",
				"podGroupKey", unitutil.GetPodGroupKey(new),
				"status", old.Status.Phase,
				"eventMsg", eventMsg,
			)
			return false, nil
		}
		_, err := ctrl.pgClient.SchedulingV1alpha1().PodGroups(new.Namespace).UpdateStatus(context.TODO(), new, metav1.UpdateOptions{})
		if err != nil {
			klog.ErrorS(err, "Failed to update pod group to new status",
				"podGroupKey", unitutil.GetPodGroupKey(new),
				"oldStatus", old.Status.Phase,
				"newStatus", new.Status.Phase,
				"eventMsg", eventMsg,
			)
			return false, err
		}

		if ctrl.eventRecorder != nil {
			ctrl.eventRecorder.Eventf(new, v1.EventTypeNormal, EventReason, "Update PodGroup in controller from %v to %v, eventMsg: %v", old.Status.Phase, new.Status.Phase, eventMsg)
		}

		klog.V(4).InfoS("Updated podGroup to new status successfully",
			"podGroupKey", unitutil.GetPodGroupKey(new),
			"oldStatus", old.Status.Phase,
			"newStatus", new.Status.Phase,
			"eventMsg", eventMsg,
		)
		return true, nil
	}
	klog.V(4).InfoS("Pod group status does not change", "key", unitutil.GetPodGroupKey(new))
	return false, nil
}

// fillOccupiedObj Add owner's information. This is used to fastly identify the owner of a pod group.
func fillOccupiedObj(pg *schedv1alpha1.PodGroup, pod *v1.Pod) {
	var refs []string
	for _, ownerRef := range pod.OwnerReferences {
		refs = append(refs, fmt.Sprintf("%s/%s/%s", ownerRef.Kind, pod.Namespace, ownerRef.Name))
	}
	if len(refs) != 0 {
		sort.Strings(refs)
		pg.Status.OccupiedBy = strings.Join(refs, ",")
	}
}
