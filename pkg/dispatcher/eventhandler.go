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
	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	scheduling "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	nodeinformer "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions/node/v1alpha1"
	schedulinginformer "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/dispatcher/internal/queue"
	"github.com/kubewharf/godel-scheduler/pkg/dispatcher/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/features"
	frwkutils "github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

func generateUnitKeyFromPod(pod *v1.Pod) string {
	if pod.Annotations != nil && len(pod.Annotations[podutil.PodGroupNameAnnotationKey]) > 0 {
		return pod.Namespace + "/" + pod.Annotations[podutil.PodGroupNameAnnotationKey]
	}
	return ""
}

func (d *Dispatcher) addPodToPendingOrSortedQueue(obj interface{}) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.InfoS("Failed to convert the object to *v1.Pod", "object", obj, "err", err)
		return
	}

	podInfo, err := queue.NewQueuedPodInfo(pod)
	if err != nil {
		klog.InfoS("Error occurred during NewQueuedPodInfo for pod", "pod", klog.KObj(pod), "err", err)
		return
	}

	podProperty := podInfo.GetPodProperty()
	traceContext, _ := tracing.StartSpanForPod(
		podutil.GetPodKey(pod),
		"dispatcher::addPodToQueue",
		tracing.WithDispatcherOption(),
		podProperty.ConvertToTracingTags(),
	)
	podInfo.SpanContext = traceContext.RootSpanContext()

	defer func() {
		go traceContext.Finish()
	}()

	klog.V(3).InfoS("Detected an Add event for pending pod", "podResourceType", podInfo.PodResourceType, "pod", klog.KObj(pod))
	metrics.DispatcherIncomingPodsInc(podProperty)

	// if the pod is associated to a unit, which is not ready, add
	// it to the corresponding pending unit.
	if frwkutils.PodBelongToUnit(pod) {
		klog.V(5).InfoS("DEBUG: added unsorted pod to unit infos", "pod", klog.KObj(pod))
		d.UnitInfos.AddUnSortedPodInfo(generateUnitKeyFromPod(pod), podInfo)
		return
	}
	d.FIFOPendingPodsQueue.AddPodInfo(podInfo)
}

func (d *Dispatcher) updatePodInPendingOrSortedQueue(old, new interface{}) {
	oldPod, ok := old.(*v1.Pod)
	if !ok {
		klog.InfoS("Failed to convert the oldObject to *v1.Pod", "oldObject", old)
		return
	}
	newPod, ok := new.(*v1.Pod)
	if !ok {
		klog.InfoS("Failed to convert the newObject to *v1.Pod", "newObject", new)
		return
	}

	newPodInfo, err := queue.NewQueuedPodInfo(newPod)
	if err != nil {
		klog.InfoS("Error occurred for NewQueuedPodInfo for pod", "pod", klog.KObj(newPod), "err", err)
		return
	}

	parentSpanContext := tracing.GetSpanContextFromPod(newPod)
	podProperty := newPodInfo.GetPodProperty()
	traceContext, _ := tracing.StartSpanForPodWithParentSpan(
		podutil.GetPodKey(newPod),
		"dispatcher::updatePodInPendingOrSortedQueue",
		parentSpanContext,
		tracing.WithDispatcherOption(),
		podProperty.ConvertToTracingTags(),
	)

	defer func() {
		go traceContext.Finish()
	}()
	klog.V(3).InfoS("Detected an Update event for the pending pod", "podResourceType", newPodInfo.PodResourceType, "pod", klog.KObj(newPod))

	oldPodInfo := &queue.QueuedPodInfo{
		PodKey:          podutil.GetPodKey(oldPod),
		PodResourceType: newPodInfo.PodResourceType,
		SpanContext:     newPodInfo.SpanContext,
		PodProperty:     podProperty,
	}

	// if the pod has been inserted into the Sorted Queue, remove the
	// pod from it
	if exist := d.SortedPodsQueue.PodInfoExist(newPodInfo); exist {
		d.SortedPodsQueue.UpdatePodInfo(newPodInfo)
		return
	}

	// always try to delete the old pod from all queues
	// the delete operation must be idempotent
	d.FIFOPendingPodsQueue.RemovePodInfo(oldPodInfo)
	klog.V(5).InfoS("DEBUG: removed unsorted pod from unit info first", "pod", klog.KObj(oldPod))
	d.UnitInfos.DeleteUnSortedPodInfo(generateUnitKeyFromPod(oldPod), oldPodInfo)

	if frwkutils.PodBelongToUnit(newPod) {
		klog.V(5).InfoS("DEBUG: added unsorted pod back to unit info", "pod", klog.KObj(newPod))
		d.UnitInfos.AddUnSortedPodInfo(generateUnitKeyFromPod(newPod), newPodInfo)
		return
	}

	d.FIFOPendingPodsQueue.AddPodInfo(newPodInfo)
}

func (d *Dispatcher) deletePodFromPendingOrSortedQueue(obj interface{}) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.InfoS("Failed to delete pod from pending or sorted queue", "err", err)
		return
	}

	podInfo, err := queue.NewQueuedPodInfo(pod)
	if err != nil {
		klog.InfoS("Error occurred for NewQueuedPodInfo for pod", "pod", klog.KObj(pod), "err", err)
		return
	}
	klog.V(3).InfoS("Detected a Delete event for the pending pod", "podResourceType", podInfo.PodResourceType, "pod", klog.KObj(pod))

	// if the pod has been inserted into the Sorted Queue, remove the
	// pod from it
	if exist := d.SortedPodsQueue.PodInfoExist(podInfo); exist {
		d.SortedPodsQueue.RemovePodInfo(podInfo)
		return
	}

	// if the pod is associated with a unit, which is not ready, remove it
	// from the corresponding pending unit.
	if frwkutils.PodBelongToUnit(pod) {
		d.UnitInfos.DeleteUnSortedPodInfo(generateUnitKeyFromPod(pod), podInfo)
		return
	}

	d.FIFOPendingPodsQueue.RemovePodInfo(podInfo)
}

func (d *Dispatcher) addPodToDispatchedInfo(obj interface{}) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.InfoS("Failed to add pod to dispatched", "err", err)
		return
	}
	klog.V(3).InfoS("Detected an Add event for the dispatched pod", "pod", klog.KObj(pod))

	d.DispatchInfo.AddPod(pod)

	if podutil.DispatchedPodOfGodel(pod, d.SchedulerName, d.TakeOverDefaultScheduler) {
		schedulerName := pod.Annotations[podutil.SchedulerAnnotationKey]
		metrics.PodsInPartitionSizeInc(schedulerName, string(podutil.PodDispatched))
	}
}

func (d *Dispatcher) deletePodFromDispatchedInfo(obj interface{}) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.InfoS("Failed to delete the dispatched pod", "err", err)
		return
	}

	klog.V(3).InfoS("Detected a Delete event for the dispatched pod", "pod", klog.KObj(pod))
	d.DispatchInfo.RemovePod(pod)

	if podutil.DispatchedPodOfGodel(pod, d.SchedulerName, d.TakeOverDefaultScheduler) {
		schedulerName := pod.Annotations[podutil.SchedulerAnnotationKey]
		metrics.PodsInPartitionSizeDec(schedulerName, string(podutil.PodDispatched))
	}
}

func AddAllEventHandlers(
	dispatcher *Dispatcher,
	podInformer coreinformers.PodInformer,
	schedulerInformer schedulinginformer.SchedulerInformer,
	nodeInformer coreinformers.NodeInformer,
	nmNodeInformer nodeinformer.NMNodeInformer,
	podGroupInformer schedulinginformer.PodGroupInformer,
) {
	// pending pods queue
	podInformer.Informer().AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: func(obj interface{}) bool {
				switch t := obj.(type) {
				case *v1.Pod:
					return podutil.PendingPodOfGodel(t, dispatcher.SchedulerName, dispatcher.TakeOverDefaultScheduler)
				case cache.DeletedFinalStateUnknown:
					if pod, ok := t.Obj.(*v1.Pod); ok {
						return podutil.PendingPodOfGodel(pod, dispatcher.SchedulerName, dispatcher.TakeOverDefaultScheduler)
					}
					klog.InfoS("Failed to convert object to *v1.Pod", "object", obj, "component", dispatcher)
					return false
				default:
					klog.InfoS("Failed to handle object", "component", dispatcher, "object", obj)
					return false
				}
			},
			Handler: cache.ResourceEventHandlerFuncs{
				AddFunc:    dispatcher.addPodToPendingOrSortedQueue,
				UpdateFunc: dispatcher.updatePodInPendingOrSortedQueue,
				DeleteFunc: dispatcher.deletePodFromPendingOrSortedQueue,
			},
		},
	)

	// dispatched pods queue
	podInformer.Informer().AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: func(obj interface{}) bool {
				switch t := obj.(type) {
				case *v1.Pod:
					return podutil.DispatchedPodOfGodel(t, dispatcher.SchedulerName, dispatcher.TakeOverDefaultScheduler)
				case cache.DeletedFinalStateUnknown:
					if pod, ok := t.Obj.(*v1.Pod); ok {
						return podutil.DispatchedPodOfGodel(pod, dispatcher.SchedulerName, dispatcher.TakeOverDefaultScheduler)
					}
					klog.InfoS("Failed to convert object to *v1.Pod", "object", obj, "component", dispatcher)
					return false
				default:
					klog.InfoS("Failed to handle object", "component", dispatcher, "object", obj)
					return false
				}
			},
			Handler: cache.ResourceEventHandlerFuncs{
				AddFunc:    dispatcher.addPodToDispatchedInfo,
				DeleteFunc: dispatcher.deletePodFromDispatchedInfo,
			},
		},
	)

	// abnormal state pod queue
	podInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    dispatcher.addPodToAbnormalQueue,
			UpdateFunc: dispatcher.updatePodInAbnormalQueue,
		},
	)

	schedulerInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    dispatcher.addScheduler,
			UpdateFunc: dispatcher.updateScheduler,
			DeleteFunc: dispatcher.deleteScheduler,
		},
	)

	// unit infos
	podInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    dispatcher.addPodToUnitInfos,
			DeleteFunc: dispatcher.deletePodFromUnitInfos,
		},
	)
	// unit infos
	podGroupInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    dispatcher.addPodGroupToUnitInfos,
			UpdateFunc: dispatcher.updatePodGroupInUnitInfos,
			DeleteFunc: dispatcher.deletePodGroupFromUnitInfos,
		},
	)

	if utilfeature.DefaultFeatureGate.Enabled(features.DispatcherNodeShuffle) {
		nodeInformer.Informer().AddEventHandler(
			cache.ResourceEventHandlerFuncs{
				AddFunc:    dispatcher.addNode,
				UpdateFunc: dispatcher.updateNode,
				DeleteFunc: dispatcher.deleteNode,
			},
		)

		nmNodeInformer.Informer().AddEventHandler(
			cache.ResourceEventHandlerFuncs{
				AddFunc:    dispatcher.addNMNode,
				UpdateFunc: dispatcher.updateNMNode,
				DeleteFunc: dispatcher.deleteNMNode,
			},
		)
	}
}

// addScheduler adds the info of a scheduler instance to the dispatcher cache.
func (d *Dispatcher) addScheduler(obj interface{}) {
	scheduler, ok := obj.(*scheduling.Scheduler)
	if !ok {
		klog.InfoS("Failed to convert object to *scheduling.Scheduler", "object", obj)
		return
	}

	klog.V(3).InfoS("Started to add scheduler", "schedulerName", scheduler.Name)

	d.DispatchInfo.AddScheduler(scheduler.Name)
	d.maintainer.AddScheduler(scheduler)
}

func (d *Dispatcher) updateScheduler(oldObj, newObj interface{}) {
	oldScheduler, ok := oldObj.(*scheduling.Scheduler)
	if !ok {
		klog.InfoS("Failed to convert oldObj to *scheduling.Scheduler", "oldObject", oldObj)
		return
	}
	newScheduler, ok := newObj.(*scheduling.Scheduler)
	if !ok {
		klog.InfoS("Failed to convert newObj to *scheduling.Scheduler", "newObject", newObj)
		return
	}

	d.maintainer.UpdateScheduler(oldScheduler, newScheduler)

	klog.V(3).InfoS("Updated scheduler", "schedulerName", oldScheduler.Name)
}

func (d *Dispatcher) deleteScheduler(obj interface{}) {
	var scheduler *scheduling.Scheduler
	switch t := obj.(type) {
	case *scheduling.Scheduler:
		scheduler = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		scheduler, ok = t.Obj.(*scheduling.Scheduler)
		if !ok {
			klog.InfoS("Failed to convert to *scheduling.Scheduler", "object", t.Obj)
			return
		}
	default:
		klog.InfoS("Failed to convert to *scheduling.Scheduler", "object", t)
		return
	}

	klog.V(3).InfoS("Started to delete scheduler", "schedulerName", scheduler.Name)

	d.DispatchInfo.DeleteScheduler(scheduler.Name)
	d.maintainer.DeleteScheduler(scheduler)
	d.reconciler.DeleteScheduler(scheduler)
}

func (d *Dispatcher) addPodToUnitInfos(obj interface{}) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.InfoS("Failed to add pod to unit info", "err", err)
		return
	}
	klog.V(5).InfoS("Added pod to unit infos", "pod", klog.KObj(pod))
	d.UnitInfos.AddPod(generateUnitKeyFromPod(pod), podutil.GetPodKey(pod))
}

func (d *Dispatcher) deletePodFromUnitInfos(obj interface{}) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.InfoS("Failed to delete pod from unit info", "err", err)
		return
	}
	d.UnitInfos.DeletePod(generateUnitKeyFromPod(pod), podutil.GetPodKey(pod))
}

func (d *Dispatcher) addPodGroupToUnitInfos(obj interface{}) {
	pg, ok := obj.(*scheduling.PodGroup)
	if !ok {
		klog.InfoS("Failed to convert obj to *scheduling.PodGroup", "object", obj)
		return
	}

	klog.V(4).InfoS("Started to handle Add event for PodGroup",
		"podGroup", klog.KObj(pg))
	d.UnitInfos.AddPodGroup(pg)
}

func (d *Dispatcher) updatePodGroupInUnitInfos(oldObj, newObj interface{}) {
	old, ok := oldObj.(*scheduling.PodGroup)
	if !ok {
		klog.InfoS("Failed to convert oldObj to *scheduling.PodGroup", "oldObject", oldObj)
		return
	}
	new, ok := newObj.(*scheduling.PodGroup)
	if !ok {
		klog.InfoS("Failed to convert newObj to *scheduling.PodGroup", "newObject", newObj)
		return
	}
	klog.V(4).InfoS("Started to handle Update event for PodGroup",
		"podGroup", klog.KObj(new))
	d.UnitInfos.UpdatePodGroup(old, new)
}

func (d *Dispatcher) deletePodGroupFromUnitInfos(obj interface{}) {
	var podGroup *scheduling.PodGroup
	switch t := obj.(type) {
	case *scheduling.PodGroup:
		podGroup = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		podGroup, ok = t.Obj.(*scheduling.PodGroup)
		if !ok {
			klog.InfoS("Failed to convert object to *scheduling.PodGroup", "object", t.Obj)
			return
		}
	default:
		klog.InfoS("Failed to convert to *scheduling.PodGroup", "object", t)
		return
	}

	klog.V(4).InfoS("Started to handle the Delete event for the PodGroup",
		"podGroup", klog.KObj(podGroup))
	d.UnitInfos.DeletePodGroup(podGroup)
}

func (d *Dispatcher) addNode(obj interface{}) {
	node, ok := obj.(*v1.Node)
	if !ok {
		klog.InfoS("Failed to convert to *v1.Node", "object", obj)
		return
	}

	klog.V(4).InfoS("Started to add the node", "node", klog.KObj(node))
	d.maintainer.AddNodeToGodelSchedulerIfNotPresent(node)
	if utilfeature.DefaultFeatureGate.Enabled(features.DispatcherNodeShuffle) {
		d.shuffler.AddNode(node)
	}
}

func (d *Dispatcher) updateNode(oldObj, newObj interface{}) {
	oldNode, ok := oldObj.(*v1.Node)
	if !ok {
		klog.InfoS("Failed to convert oldObj to *v1.Node", "oldObject", oldObj)
		return
	}
	newNode, ok := newObj.(*v1.Node)
	if !ok {
		klog.InfoS("Failed to convert newObj to *v1.Node", "newObject", newObj)
		return
	}

	klog.V(4).InfoS("Started to update node", "node", klog.KObj(oldNode))
	d.maintainer.UpdateNodeInGodelSchedulerIfNecessary(oldNode, newNode)
	if utilfeature.DefaultFeatureGate.Enabled(features.DispatcherNodeShuffle) {
		d.shuffler.UpdateNode(oldNode, newNode)
	}
}

func (d *Dispatcher) deleteNode(obj interface{}) {
	var node *v1.Node
	switch t := obj.(type) {
	case *v1.Node:
		node = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		node, ok = t.Obj.(*v1.Node)
		if !ok {
			klog.InfoS("Failed to convert to *v1.Node", "object", t.Obj)
			return
		}
	default:
		klog.InfoS("Failed to convert to *v1.Node", "object", t)
		return
	}

	klog.V(4).InfoS("Started to delete node", "node", klog.KObj(node))
	d.maintainer.DeleteNodeFromGodelScheduler(node)
}

func (d *Dispatcher) addNMNode(obj interface{}) {
	nmNode, ok := obj.(*nodev1alpha1.NMNode)
	if !ok {
		klog.InfoS("Failed to convert to *nodev1alpha1.NMNode", "object", obj)
		return
	}

	klog.V(3).InfoS("Started to add nmNode", "nmNode", nmNode.Name)
	d.maintainer.AddNMNodeToGodelSchedulerIfNotPresent(nmNode)
	if utilfeature.DefaultFeatureGate.Enabled(features.DispatcherNodeShuffle) {
		d.shuffler.AddNMNode(nmNode)
	}
}

func (d *Dispatcher) updateNMNode(oldObj, newObj interface{}) {
	oldNMNode, ok := oldObj.(*nodev1alpha1.NMNode)
	if !ok {
		klog.InfoS("Failed to convert oldObj to *nodev1alpha1.NMNode", "oldObject", oldObj)
		return
	}
	newNMNode, ok := newObj.(*nodev1alpha1.NMNode)
	if !ok {
		klog.InfoS("Failed to convert newObj to *nodev1alpha1.NMNode", "newObject", newObj)
		return
	}

	klog.V(3).InfoS("Started to update nmNode", "nmNode", oldNMNode.Name)
	d.maintainer.UpdateNMNodeInGodelSchedulerIfNecessary(oldNMNode, newNMNode)
	if utilfeature.DefaultFeatureGate.Enabled(features.DispatcherNodeShuffle) {
		d.shuffler.UpdateNMNode(oldNMNode, newNMNode)
	}
}

func (d *Dispatcher) deleteNMNode(obj interface{}) {
	var nmNode *nodev1alpha1.NMNode
	switch t := obj.(type) {
	case *nodev1alpha1.NMNode:
		nmNode = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		nmNode, ok = t.Obj.(*nodev1alpha1.NMNode)
		if !ok {
			klog.InfoS("Failed to convert to *nodev1alpha1.NMNode", "object", t.Obj)
			return
		}
	default:
		klog.InfoS("Failed to convert to *nodev1alpha1.NMNode", "object", t)
		return
	}

	klog.V(3).InfoS("Started to delete nmNode", "nmNode", nmNode.Name)
	d.maintainer.DeleteNMNodeFromGodelScheduler(nmNode)
}

func (d *Dispatcher) addPodToAbnormalQueue(obj interface{}) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.InfoS("Failed to add pod to abnormal queue", "err", err)
		return
	}

	if abnormal := podutil.AbnormalPodStateOfGodel(pod, d.SchedulerName, d.TakeOverDefaultScheduler); abnormal {
		podKey, err := cache.MetaNamespaceKeyFunc(pod)
		if err == nil {
			d.reconciler.AbnormalPodsEnqueue(podKey)
		} else {
			klog.InfoS("Failed to get key for pod", "pod", klog.KObj(pod), "err", err)
		}
	}
}

func (d *Dispatcher) updatePodInAbnormalQueue(_, newObj interface{}) {
	newPod, err := podutil.ConvertToPod(newObj)
	if err != nil {
		klog.InfoS("Failed to add pod to dispatched", "err", err)
		return
	}
	if abnormal := podutil.AbnormalPodStateOfGodel(newPod, d.SchedulerName, d.TakeOverDefaultScheduler); abnormal {
		podKey, err := cache.MetaNamespaceKeyFunc(newPod)
		if err == nil {
			d.reconciler.AbnormalPodsEnqueue(podKey)
		} else {
			klog.InfoS("Failed to get key for pod", "pod", klog.KObj(newPod), "err", err)
		}
	}
}
