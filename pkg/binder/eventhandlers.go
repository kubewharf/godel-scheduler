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
	"fmt"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	katalystinformers "github.com/kubewharf/katalyst-api/pkg/client/informers/externalversions"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	binderutils "github.com/kubewharf/godel-scheduler/pkg/binder/utils"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	frameworkutils "github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

func (binder *Binder) addNodeToCache(obj interface{}) {
	node, ok := obj.(*v1.Node)
	if !ok {
		klog.InfoS("Failed to convert to *v1.Node", "object", obj)
		return
	}

	if err := binder.BinderCache.AddNode(node); err != nil {
		klog.InfoS("Failed to execute binder cache AddNode", "node", klog.KObj(node), "err", err)
	}

	klog.V(3).InfoS("Found an add event", "node", klog.KObj(node))
}

func (binder *Binder) updateNodeInCache(oldObj, newObj interface{}) {
	oldNode, ok := oldObj.(*v1.Node)
	if !ok {
		klog.InfoS("Failed to convert oldObj to *v1.Node", "oldObj", oldObj)
		return
	}
	newNode, ok := newObj.(*v1.Node)
	if !ok {
		klog.InfoS("Failed to convert newObj to *v1.Node", "newObj", newObj)
		return
	}

	if err := binder.BinderCache.UpdateNode(oldNode, newNode); err != nil {
		klog.InfoS("Failed to execute binder cache UpdateNode", "err", err)
	}
	klog.V(3).InfoS("Found an update event", "node", klog.KObj(newNode))
}

func (binder *Binder) deleteNodeFromCache(obj interface{}) {
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
		klog.InfoS("Failed to convert to *v1.Node", "type", t)
		return
	}
	klog.V(3).InfoS("Found a delete event", "node", klog.KObj(node))
	// NOTE: Updates must be written to binder cache before invalidating
	// equivalence cache, because we could snapshot equivalence cache after the
	// invalidation and then snapshot the cache itself. If the cache is
	// snapshotted before updates are written, we would update equivalence
	// cache with stale information which is based on snapshot of old cache.
	if err := binder.BinderCache.RemoveNode(node); err != nil {
		klog.InfoS("Failed to execute binder cache RemoveNode", "err", err)
	}
}

func (binder *Binder) addNMNodeToCache(obj interface{}) {
	nmNode, ok := obj.(*nodev1alpha1.NMNode)
	if !ok {
		klog.InfoS("Failed to convert to *nodev1alpha1.NMNode", "object", obj)
		return
	}

	if err := binder.BinderCache.AddNMNode(nmNode); err != nil {
		klog.InfoS("Failed to do binder cache AddNMNode", "err", err)
		return
	}
}

func (binder *Binder) updateNMNodeInCache(oldObj, newObj interface{}) {
	oldNMNode, ok := oldObj.(*nodev1alpha1.NMNode)
	if !ok {
		klog.InfoS("Failed to convert oldObj to *nodev1alpha1.NMNode", "oldObj", oldObj)
		return
	}
	newNMNode, ok := newObj.(*nodev1alpha1.NMNode)
	if !ok {
		klog.InfoS("Failed to convert newObj to *nodev1alpha1.NMNode", "newObj", newObj)
		return
	}

	if err := binder.BinderCache.UpdateNMNode(oldNMNode, newNMNode); err != nil {
		klog.InfoS("Failed to execute binder cache UpdateNMNode", "err", err)
	}
}

func (binder *Binder) deleteNMNodeFromCache(obj interface{}) {
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
		klog.InfoS("Failed to convert to *nodev1alpha1.NMNode", "type", t)
		return
	}
	klog.V(3).InfoS("Found a delete event for nmNode", "nmNode", nmNode.Name)
	// NOTE: Updates must be written to binder cache before invalidating
	// equivalence cache, because we could snapshot equivalence cache after the
	// invalidation and then snapshot the cache itself. If the cache is
	// snapshotted before updates are written, we would update equivalence
	// cache with stale information which is based on snapshot of old cache.
	if err := binder.BinderCache.RemoveNMNode(nmNode); err != nil {
		klog.InfoS("Failed to execute binder cache RemoveNMNode", "err", err)
	}
}

func (binder *Binder) addCNRToCache(obj interface{}) {
	cnr, ok := obj.(*katalystv1alpha1.CustomNodeResource)
	if !ok {
		klog.InfoS("Failed to convert to *v1alpha1.CNR", "object", obj)
		return
	}

	if err := binder.BinderCache.AddCNR(cnr); err != nil {
		klog.InfoS("Failed to execute binder cache AddCNR", "err", err)
		return
	}
}

func (binder *Binder) updateCNRInCache(oldObj, newObj interface{}) {
	oldCNR, ok := oldObj.(*katalystv1alpha1.CustomNodeResource)
	if !ok {
		klog.InfoS("Failed to convert oldObj to *v1alpha1.CNR", "oldObj", oldObj)
		return
	}
	newCNR, ok := newObj.(*katalystv1alpha1.CustomNodeResource)
	if !ok {
		klog.InfoS("Failed to convert newObj to *v1alpha1.CNR", "newObj", newObj)
		return
	}

	if err := binder.BinderCache.UpdateCNR(oldCNR, newCNR); err != nil {
		klog.InfoS("Failed to execute binder cache UpdateCNR", "err", err)
	}
}

func (binder *Binder) deleteCNRFromCache(obj interface{}) {
	var cnr *katalystv1alpha1.CustomNodeResource
	switch t := obj.(type) {
	case *katalystv1alpha1.CustomNodeResource:
		cnr = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		cnr, ok = t.Obj.(*katalystv1alpha1.CustomNodeResource)
		if !ok {
			klog.InfoS("Failed to convert to *v1alpha1.CNR", "object", t.Obj)
			return
		}
	default:
		klog.InfoS("Failed to convert to *v1alpha1.CNR", "type", t)
		return
	}
	klog.V(3).InfoS("Found a delete event for cnr", "cnr", cnr.Name)
	// NOTE: Updates must be written to binder cache before invalidating
	// equivalence cache, because we could snapshot equivalence cache after the
	// invalidation and then snapshot the cache itself. If the cache is
	// snapshotted before updates are written, we would update equivalence
	// cache with stale information which is based on snapshot of old cache.
	if err := binder.BinderCache.RemoveCNR(cnr); err != nil {
		klog.InfoS("Failed to execute binder cache RemoveCNR", "err", err)
	}
}

func (binder *Binder) addPodToCache(obj interface{}) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.InfoS("Failed to add pod to cache", "err", err)
		return
	}
	klog.V(3).InfoS("Found an add event for scheduled pod with assigned node", "namespace", pod.Namespace, "podName", pod.Name, "nodeName", pod.Spec.NodeName)

	if needAddToCache(pod) {
		if err := binder.BinderCache.AddPod(pod); err != nil {
			klog.InfoS("Failed to execute binder cache AddPod", "err", err)
		}
	}
}

func (binder *Binder) updatePodInCache(oldObj, newObj interface{}) {
	oldPod, err := podutil.ConvertToPod(oldObj)
	if err != nil {
		klog.InfoS("Failed to update pod with oldObj", "err", err)
		return
	}
	newPod, err := podutil.ConvertToPod(newObj)
	if err != nil {
		klog.InfoS("Failed to update pod with newObj", "err", err)
		return
	}

	klog.V(3).InfoS("Found an update event for scheduled pod", "namespace", newPod.Namespace, "pod", klog.KObj(newPod))

	// NOTE: Updates must be written to binder cache before invalidating
	// equivalence cache, because we could snapshot equivalence cache after the
	// invalidation and then snapshot the cache itself. If the cache is
	// snapshotted before updates are written, we would update equivalence
	// cache with stale information which is based on snapshot of old cache.
	if needAddToCache(oldPod, newPod) {
		if err := binder.BinderCache.UpdatePod(oldPod, newPod); err != nil {
			klog.InfoS("Failed to execute binder cache UpdatePod", "err", err)
		}
	}
}

func (binder *Binder) deletePodFromCache(obj interface{}) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.InfoS("Failed to delete pod from cache", "err", err)
		return
	}
	klog.V(3).InfoS("Found a delete event for scheduled pod", "pod", klog.KObj(pod))
	// NOTE: Updates must be written to binder cache before invalidating
	// equivalence cache, because we could snapshot equivalence cache after the
	// invalidation and then snapshot the cache itself. If the cache is
	// snapshotted before updates are written, we would update equivalence
	// cache with stale information which is based on snapshot of old cache.
	if needAddToCache(pod) {
		if err := binder.BinderCache.RemovePod(pod); err != nil {
			klog.InfoS("Failed to execute binder cache RemovePod", "err", err)
		}
	}
}

func (binder *Binder) addPodToBinderQueue(obj interface{}) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.InfoS("Failed to add pod to binder queue", "err", err)
		return
	}

	if !podutil.AssumedPodOfGodel(pod, *binder.SchedulerName) {
		klog.V(4).InfoS("Pod is not in assumed state", "pod", klog.KObj(pod))
		return
	}

	podProperty := framework.ExtractPodProperty(pod)
	parentSpanContext := tracing.GetSpanContextFromPod(pod)
	traceContext, _ := tracing.StartSpanForPodWithParentSpan(
		podutil.GetPodKey(pod),
		tracing.BinderAddPodToQueueSpan,
		parentSpanContext,
		tracing.WithBinderOption(),
		podProperty.ConvertToTracingTags(),
	)
	defer func() {
		traceContext.WithTags(tracing.WithResultTag(err))
		go traceContext.Finish()
	}()

	klog.V(3).InfoS("Found an add event for unscheduled pod with annotations", "pod", klog.KObj(pod), "annotations", pod.GetAnnotations())
	if err = binder.BinderQueue.Add(pod); err != nil {
		utilruntime.HandleError(fmt.Errorf("unable to queue %T: %v", obj, err))
	}
}

func (binder *Binder) updatePodInBinderQueue(oldObj, newObj interface{}) {
	oldPod, err := podutil.ConvertToPod(oldObj)
	if err != nil {
		klog.InfoS("Failed to update pod in binder queue with oldObj", "err", err)
		return
	}
	newPod, err := podutil.ConvertToPod(newObj)
	if err != nil {
		klog.InfoS("Failed to update pod in binder queue with newObj", "err", err)
		return
	}

	if !podutil.AssumedPodOfGodel(newPod, *binder.SchedulerName) {
		if podutil.AssumedPodOfGodel(oldPod, *binder.SchedulerName) {
			// pod is reset to another state
			// if changed to Bound, the waiting pod is allowed, it will be added to Bound map in waitingTasksManager,
			// if reset to Pending/Dispatched by binder, the pod is rejected or failed after being allowed,
			// if reset to other state and the operator is not binder, the pod may be still waiting,
			// we can always remove the pod from waiting state
			binder.BinderQueue.Delete(oldPod)
		}
		return
	}

	parentSpanContext := tracing.GetSpanContextFromPod(newPod)
	podProperty := framework.ExtractPodProperty(newPod)
	traceContext, _ := tracing.StartSpanForPodWithParentSpan(
		podutil.GetPodKey(newPod),
		tracing.BinderUpdatePodToQueueSpan,
		parentSpanContext,
		tracing.WithBinderOption(),
		podProperty.ConvertToTracingTags(),
	)
	defer func() {
		traceContext.WithTags(tracing.WithResultTag(err))
		go traceContext.Finish()
	}()
	klog.V(3).InfoS("Found an update event for unscheduled pod with annotations", "pod", klog.KObj(newPod), "annotations", newPod.GetAnnotations())
	if err = binder.BinderQueue.Update(oldPod, newPod); err != nil {
		utilruntime.HandleError(fmt.Errorf("unable to update %T: %v", newObj, err))
	}
}

func (binder *Binder) deletePodFromBinderQueue(obj interface{}) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.InfoS("Failed to delete pod from binder queue", "err", err)
		return
	}

	if !podutil.AssumedPodOfGodel(pod, *binder.SchedulerName) {
		return
	}

	parentSpanContext := tracing.GetSpanContextFromPod(pod)
	podKey := podutil.GetPodKey(pod)
	podProperty := framework.ExtractPodProperty(pod)
	traceContext, _ := tracing.StartSpanForPodWithParentSpan(
		podKey,
		tracing.BinderDeletePodFromQueueSpan,
		parentSpanContext,
		tracing.WithBinderOption(),
		podProperty.ConvertToTracingTags(),
	)
	defer func() {
		go traceContext.Finish()
	}()

	klog.V(3).InfoS("Found a delete event for unscheduled pod with annotations", "pod", klog.KObj(pod), "annotations", pod.GetAnnotations())
	if err := binder.BinderQueue.Delete(pod); err != nil {
		utilruntime.HandleError(fmt.Errorf("unable to dequeue %T: %v", obj, err))
	}

	// victims marker should also be deleted if preemptor in binder queue removed.
	if nominatedNode, err := frameworkutils.GetPodNominatedNode(pod); err == nil && nominatedNode != nil && len(nominatedNode.VictimPods) != 0 {
		removeDeleteVictimsMarkerByKey := func(victimPods framework.VictimPods, preemptor *v1.Pod) error {
			var errs []error
			pKey, err := framework.GetPodKey(preemptor)
			if err != nil {
				return err
			}

			for _, victimPod := range victimPods {
				if err := binder.BinderCache.RemoveDeletePodMarkerByKey(victimPod.UID, pKey); err != nil {
					errs = append(errs, err)
				}
			}
			return utilerrors.NewAggregate(errs)
		}
		if removeErr := removeDeleteVictimsMarkerByKey(nominatedNode.VictimPods, pod); removeErr != nil {
			klog.InfoS("Failed to remove delete victims marker", "podKey", podutil.GeneratePodKey(pod), "pod", klog.KObj(pod), "err", removeErr)
		}
	}

	if binder.handle.VolumeBinder() != nil {
		// Volume binder only wants to keep unassigned pods
		binder.handle.VolumeBinder().DeletePodBindings(pod)
	}

	// if assumed preemptor is deleted during the period of "Inhandling"
	// we need to forget it from cache
	if isAssumed, err := binder.BinderCache.IsAssumedPod(pod); err != nil {
		klog.InfoS("Failed to check if pod is assumed", "pod", klog.KObj(pod), "err", err)
	} else if isAssumed {
		if currentPod, err := binder.BinderCache.GetPod(pod); err != nil {
			klog.InfoS("Failed to get current pod", "pod", klog.KObj(pod), "err", err)
		} else if err := binder.BinderCache.ForgetPod(currentPod); err != nil {
			klog.InfoS("Failed to forget pod when deleted from binder queue", "pod", klog.KObj(pod), "err", err)
		}
	}
}

func needAddToCache(pods ...*v1.Pod) bool {
	for _, pod := range pods {
		if podutil.BoundPod(pod) {
			return true
		}
	}
	return false
}

func (binder *Binder) addPodGroup(obj interface{}) {
	podGroup, ok := obj.(*schedulingv1a1.PodGroup)
	if !ok {
		klog.InfoS("Failed to convert obj to *v1alpha1.PodGroup", "object", obj)
		return
	}

	klog.V(3).InfoS("Found an add event for pod group", "podGroup", klog.KObj(podGroup))

	if err := binder.BinderCache.AddPodGroup(podGroup); err != nil {
		klog.InfoS("Failed to execute binder cache AddPodGroup", "err", err)
		return
	}

	binder.addUnitStatus(podGroup)
}

func (binder *Binder) updatePodGroup(oldObj interface{}, newObj interface{}) {
	oldPodGroup, ok := oldObj.(*schedulingv1a1.PodGroup)
	if !ok {
		klog.InfoS("Failed to convert oldObj to *v1alpha1.PodGroup", "oldObj", oldObj)
		return
	}
	newPodGroup, ok := newObj.(*schedulingv1a1.PodGroup)
	if !ok {
		klog.InfoS("Failed to convert newObj to *v1alpha1.PodGroup", "newObj", newObj)
		return
	}

	if oldPodGroup.UID != newPodGroup.UID {
		binder.deletePodGroup(oldPodGroup)
		binder.addPodGroup(oldPodGroup)
	}

	klog.V(3).InfoS("Found an update event for pod group", "podGroup", klog.KObj(newPodGroup))

	if err := binder.BinderCache.UpdatePodGroup(oldPodGroup, newPodGroup); err != nil {
		klog.InfoS("Failed to execute binder cache UpdatePodGroup", "err", err)
		return
	}

	binder.updateUnitStatus(newPodGroup)
}

func (binder *Binder) deletePodGroup(obj interface{}) {
	var podGroup *schedulingv1a1.PodGroup
	switch t := obj.(type) {
	case *schedulingv1a1.PodGroup:
		podGroup = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		podGroup, ok = t.Obj.(*schedulingv1a1.PodGroup)
		if !ok {
			klog.InfoS("Failed to convert to *v1.PodGroup", "object", t.Obj)
			return
		}
	default:
		klog.InfoS("Failed to convert to *v1.PodGroup", "type", t)
		return
	}

	klog.InfoS("Found a delete event for pod group", "podGroup", klog.KObj(podGroup))

	if err := binder.BinderCache.RemovePodGroup(podGroup); err != nil {
		klog.InfoS("Failed to execute binder cache RemovePodGroup", "err", err)
		return
	}
	binder.deleteUnitStatus(podGroup)
}

func (binder *Binder) onPdbAdd(obj interface{}) {
	pdb, ok := obj.(*policy.PodDisruptionBudget)
	if !ok {
		klog.InfoS("Failed to convert to *policy.PodDisruptionBudget", "object", obj)
		return
	}

	klog.V(4).InfoS("Found an add event for pdb", "pdb", util.GetPDBKey(pdb), "disruptionsAllowed", pdb.Status.DisruptionsAllowed)
	if err := binder.BinderCache.AddPDB(pdb); err != nil {
		klog.InfoS("Failed to add pdb", "pdb", pdb, "err", err)
	}
}

func (binder *Binder) onPdbUpdate(oldObj, newObj interface{}) {
	oldPdb, ok := oldObj.(*policy.PodDisruptionBudget)
	if !ok {
		klog.InfoS("Failed to convert to *policy.PodDisruptionBudget", "oldObj", oldObj)
		return
	}
	newPdb, ok := newObj.(*policy.PodDisruptionBudget)
	if !ok {
		klog.InfoS("Failed to convert to *policy.PodDisruptionBudget", "newObj", newObj)
		return
	}
	klog.V(4).InfoS("Found an update event for pdb", "pdb", util.GetPDBKey(newPdb),
		"oldPdbStatusDisruptionsAllowed", oldPdb.Status.DisruptionsAllowed,
		"newPdbStatusDisruptionsAllowed", newPdb.Status.DisruptionsAllowed)
	if err := binder.BinderCache.UpdatePDB(oldPdb, newPdb); err != nil {
		klog.InfoS("Failed to update pdb", "oldPdb", oldPdb, "newPdb", oldPdb, "err", err)
	}
}

func (binder *Binder) onPdbDelete(obj interface{}) {
	pdb, ok := obj.(*policy.PodDisruptionBudget)
	if !ok {
		klog.InfoS("Failed to convert to *policy.PodDisruptionBudget", "object", obj)
		return
	}
	klog.V(4).InfoS("Found a delete event for pdb", "pdb", util.GetPDBKey(pdb), "pdbStatusDisruptionsAllowed", pdb.Status.DisruptionsAllowed)
	if err := binder.BinderCache.DeletePDB(pdb); err != nil {
		klog.InfoS("Failed to delete pdb", "pdb", pdb, "err", err)
	}
}

// addAllEventHandlers is a helper function used in tests and in Prebinder
// to add event handlers for various informers.
func addAllEventHandlers(
	binder *Binder,
	informerFactory informers.SharedInformerFactory,
	crdInformerFactory crdinformers.SharedInformerFactory,
	katalystInformerFactory katalystinformers.SharedInformerFactory,
) {
	// scheduled pod cache
	informerFactory.Core().V1().Pods().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    binder.addPodToCache,
			UpdateFunc: binder.updatePodInCache,
			DeleteFunc: binder.deletePodFromCache,
		},
	)

	// pods with nominated nodes by binder
	informerFactory.Core().V1().Pods().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    binder.addPodToBinderQueue,
			UpdateFunc: binder.updatePodInBinderQueue,
			DeleteFunc: binder.deletePodFromBinderQueue,
		},
	)

	// TODO: Add cnr event handlers
	informerFactory.Core().V1().Nodes().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    binder.addNodeToCache,
			UpdateFunc: binder.updateNodeInCache,
			DeleteFunc: binder.deleteNodeFromCache,
		},
	)

	informerFactory.Policy().V1().PodDisruptionBudgets().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    binder.onPdbAdd,
			UpdateFunc: binder.onPdbUpdate,
			DeleteFunc: binder.onPdbDelete,
		},
	)

	crdInformerFactory.Node().V1alpha1().NMNodes().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    binder.addNMNodeToCache,
			UpdateFunc: binder.updateNMNodeInCache,
			DeleteFunc: binder.deleteNMNodeFromCache,
		},
	)

	katalystInformerFactory.Node().V1alpha1().CustomNodeResources().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    binder.addCNRToCache,
			UpdateFunc: binder.updateCNRInCache,
			DeleteFunc: binder.deleteCNRFromCache,
		},
	)

	// add PodGroup resource event listener
	crdInformerFactory.Scheduling().V1alpha1().PodGroups().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    binder.addPodGroup,
			UpdateFunc: binder.updatePodGroup,
			DeleteFunc: binder.deletePodGroup,
		},
	)
}

func (binder *Binder) addUnitStatus(pg *schedulingv1a1.PodGroup) {
	key := fmt.Sprintf("%s/%s/%s", framework.PodGroupUnitType, pg.Namespace, pg.Name)

	var status binderutils.UnitStatus

	switch pg.Status.Phase {
	case schedulingv1a1.PodGroupPending:
		status = binderutils.PendingStatus
	case schedulingv1a1.PodGroupPreScheduling:
		status = binderutils.PendingStatus
	case schedulingv1a1.PodGroupScheduled:
		status = binderutils.ScheduledStatus
	case schedulingv1a1.PodGroupRunning:
		status = binderutils.ScheduledStatus
	case schedulingv1a1.PodGroupTimeout:
		status = binderutils.TimeoutStatus
	case schedulingv1a1.PodGroupFinished:
		status = binderutils.ScheduledStatus
	case schedulingv1a1.PodGroupFailed:
		status = binderutils.ScheduledStatus
	case schedulingv1a1.PodGroupUnknown:
		status = binderutils.UnKnownStatus
	}

	binder.BinderCache.SetUnitStatus(key, status)
	binder.BinderQueue.SetUnitStatus(key, status)
	// When PodGroup is updated to ScheduledStatus, it is necessary to move Pods
	// associated with that PodGroupUnit from waitingUnitQ to readyUnitQ.
	// more details: https://github.com/kubewharf/godel-scheduler/merge_requests/723
	binder.BinderQueue.ActiveWaitingUnit(key)
}

func (binder *Binder) updateUnitStatus(pg *schedulingv1a1.PodGroup) {
	binder.addUnitStatus(pg)
}

func (binder *Binder) deleteUnitStatus(pg *schedulingv1a1.PodGroup) {
	key := fmt.Sprintf("%s/%s/%s", framework.PodGroupUnitType, pg.Namespace, pg.Name)
	binder.BinderCache.DeleteUnitStatus(key)
	binder.BinderQueue.DeleteUnitStatus(key)
}
