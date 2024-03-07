/*
Copyright 2019 The Kubernetes Authors.

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

package scheduler

import (
	"fmt"
	"reflect"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	katalystinformers "github.com/kubewharf/katalyst-api/pkg/client/informers/externalversions"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	storagev1 "k8s.io/api/storage/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/features"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	unitutil "github.com/kubewharf/godel-scheduler/pkg/util/unit"
)

func (sched *Scheduler) onPvAdd(obj interface{}) {
	// Pods created when there are no PVs available will be stuck in
	// unschedulable queue. But unbound PVs created for static provisioning and
	// delay binding storage class are skipped in PV controller dynamic
	// provisioning and binding process, will not trigger events to schedule pod
	// again. So we need to move pods to active queue on PV add for this
	// scenario.
	sched.ScheduleSwitch.Process(
		// TODO: Parse SwitchType for PV
		framework.SwitchTypeAll,
		func(dataSet ScheduleDataSet) {
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.PvAdd)
		},
	)
}

func (sched *Scheduler) onPvUpdate(old, new interface{}) {
	// Scheduler.bindVolumesWorker may fail to update assumed pod volume
	// bindings due to conflicts if PVs are updated by PV controller or other
	// parties, then scheduler will add pod back to unschedulable queue. We
	// need to move pods to active queue on PV update for this scenario.
	sched.ScheduleSwitch.Process(
		// TODO: Parse SwitchType for PV
		framework.SwitchTypeAll,
		func(dataSet ScheduleDataSet) {
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.PvUpdate)
		},
	)
}

func (sched *Scheduler) onPvcAdd(obj interface{}) {
	sched.ScheduleSwitch.Process(
		// TODO: Parse SwitchType for PV
		framework.SwitchTypeAll,
		func(dataSet ScheduleDataSet) {
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.PvcAdd)
		},
	)
}

func (sched *Scheduler) onPvcUpdate(old, new interface{}) {
	sched.ScheduleSwitch.Process(
		// TODO: Parse SwitchType for PVC
		framework.SwitchTypeAll,
		func(dataSet ScheduleDataSet) {
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.PvcUpdate)
		},
	)
}

func (sched *Scheduler) onStorageClassAdd(obj interface{}) {
	sc, ok := obj.(*storagev1.StorageClass)
	if !ok {
		klog.InfoS("Failed to convert to *storagev1.StorageClass", "object", obj)
		return
	}

	// CheckVolumeBindingPred fails if pod has unbound immediate PVCs. If these
	// PVCs have specified StorageClass name, creating StorageClass objects
	// with late binding will cause predicates to pass, so we need to move pods
	// to active queue.
	// We don't need to invalidate cached results because results will not be
	// cached for pod that has unbound immediate PVCs.
	if sc.VolumeBindingMode != nil && *sc.VolumeBindingMode == storagev1.VolumeBindingWaitForFirstConsumer {
		sched.ScheduleSwitch.Process(
			// TODO: Parse SwitchType for StorageClass
			framework.SwitchTypeAll,
			func(dataSet ScheduleDataSet) {
				dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.StorageClassAdd)
			},
		)
	}
}

func (sched *Scheduler) onServiceAdd(obj interface{}) {
	sched.ScheduleSwitch.Process(
		// TODO: Parse SwitchType for Service
		framework.SwitchTypeAll,
		func(dataSet ScheduleDataSet) {
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.ServiceAdd)
		},
	)
}

func (sched *Scheduler) onServiceUpdate(oldObj interface{}, newObj interface{}) {
	sched.ScheduleSwitch.Process(
		// TODO: Parse SwitchType for Service
		framework.SwitchTypeAll,
		func(dataSet ScheduleDataSet) {
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.ServiceUpdate)
		},
	)
}

func (sched *Scheduler) onServiceDelete(obj interface{}) {
	sched.ScheduleSwitch.Process(
		// TODO: Parse SwitchType for Service
		framework.SwitchTypeAll,
		func(dataSet ScheduleDataSet) {
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.ServiceDelete)
		},
	)
}

func (sched *Scheduler) onCSINodeAdd(obj interface{}) {
	sched.ScheduleSwitch.Process(
		// TODO: Parse SwitchType for CSI
		framework.SwitchTypeAll,
		func(dataSet ScheduleDataSet) {
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.CSINodeAdd)
		},
	)
}

func (sched *Scheduler) onCSINodeUpdate(oldObj, newObj interface{}) {
	sched.ScheduleSwitch.Process(
		// TODO: Parse SwitchType for CSI
		framework.SwitchTypeAll,
		func(dataSet ScheduleDataSet) {
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.CSINodeUpdate)
		},
	)
}

// update nodes within scheduler api
func (sched *Scheduler) onSchedulerUpdate(_, _ interface{}) {
	sched.ScheduleSwitch.Process(
		// TODO: Parse SwitchType for Scheduler
		framework.SwitchTypeAll,
		func(dataSet ScheduleDataSet) {},
	)
}

func (sched *Scheduler) addNodeToCache(obj interface{}) {
	node, ok := obj.(*v1.Node)
	if !ok {
		klog.InfoS("Failed to convert to *v1.Node", "object", obj)
		return
	}

	klog.V(3).InfoS("Detected an Add event for node", "node", node.Name)

	if err := sched.commonCache.AddNode(node); err != nil {
		klog.InfoS("Failed to add node to scheduler cache", "err", err)
		return
	}

	sched.ScheduleSwitch.Process(
		ParseSwitchTypeForNode(node),
		func(dataSet ScheduleDataSet) {
			// TODO: revisit this.
			// Comment out this if-condition for now and remove this logic when the physical is completely removed.
			// if sched.nodeManagedByThisScheduler(node.Name) {
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.NodeAdd)
			// }
		},
	)
}

func (sched *Scheduler) updateNodeInCache(oldObj, newObj interface{}) {
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

	klog.V(3).InfoS("Detected an update event for node", "node", oldNode.Name)

	if err := sched.commonCache.UpdateNode(oldNode, newNode); err != nil {
		klog.InfoS("Failed to update node in scheduler cache", "err", err)
	}

	sched.ScheduleSwitch.Process(
		ParseSwitchTypeForNode(newNode),
		func(dataSet ScheduleDataSet) {
			// Only activate unschedulable pods if the node became more schedulable.
			// We skip the node property comparison when there is no unschedulable pods in the queue
			// to save processing cycles. We still trigger a move to active queue to cover the case
			// that a pod being processed by the scheduler is determined unschedulable. We want this
			// pod to be reevaluated when a change in the cluster happens.
			// Because pod preemption among all nodes, we should trigger a move as well.
			if dataSet.SchedulingQueue().NumUnschedulableUnits() == 0 {
				return
			} else if event := nodeSchedulingPropertiesChange(newNode, oldNode); event != "" {
				klog.V(3).InfoS("Detected an Update event for node", "node", newNode.Name, "type", dataSet.Type())
				// TODO: revisit this.
				// Comment out this if-condition for now and remove this logic when the physical is completely removed.
				// if sched.nodeManagedByThisScheduler(newNode.Name) {
				dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(event)
				// }
			}
		},
	)
}

func (sched *Scheduler) deleteNodeFromCache(obj interface{}) {
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
	klog.V(3).InfoS("Detected a Delete event for node", "node", node.Name)

	if err := sched.commonCache.RemoveNode(node); err != nil {
		klog.InfoS("Failed to remove node from Scheduler cache", "err", err)
	}
}

func (sched *Scheduler) addNMNodeToCache(obj interface{}) {
	nmNode, ok := obj.(*nodev1alpha1.NMNode)
	if !ok {
		klog.InfoS("Failed to convert to *nodev1alpha1.NMNode", "object", obj)
		return
	}

	klog.V(3).InfoS("Detected an Add event for nmNode", "node", nmNode.Name)

	if err := sched.commonCache.AddNMNode(nmNode); err != nil {
		klog.InfoS("Failed to add NMNode to Scheduler cache", "err", err)
		return
	}

	sched.ScheduleSwitch.Process(
		ParseSwitchTypeForNMNode(nmNode),
		func(dataSet ScheduleDataSet) {
			// TODO: revisit this.
			// Comment out this if-condition for now and remove this logic when the physical is completely removed.
			// if sched.nodeManagedByThisScheduler(nmNode.Name) {
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.NMNodeAdd)
			// }
		},
	)
}

func (sched *Scheduler) updateNMNodeInCache(oldObj, newObj interface{}) {
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

	klog.V(3).InfoS("Detected an update event for node", "nmnode", oldNMNode.Name)

	if err := sched.commonCache.UpdateNMNode(oldNMNode, newNMNode); err != nil {
		klog.InfoS("Failed to update NMNode in Scheduler cache", "err", err)
	}

	sched.ScheduleSwitch.Process(
		ParseSwitchTypeForNMNode(newNMNode),
		func(dataSet ScheduleDataSet) {
			// Only activate unschedulable pods if the node became more schedulable.
			// We skip the node property comparison when there is no unschedulable pods in the queue
			// to save processing cycles. We still trigger a move to active queue to cover the case
			// that a pod being processed by the scheduler is determined unschedulable. We want this
			// pod to be reevaluated when a change in the cluster happens.
			// Because pod preemption among all nodes, we should trigger a move as well.
			if dataSet.SchedulingQueue().NumUnschedulableUnits() == 0 {
				return
			} else if event := nmNodeSchedulingPropertiesChange(newNMNode, oldNMNode); event != "" {
				klog.V(3).InfoS("Detected an Update event for nmNode", "nmNode", newNMNode.Name, "type", dataSet.Type())
				// TODO: revisit this.
				// Comment out this if-condition for now and remove this logic when the physical is completely removed.
				// if sched.nodeManagedByThisScheduler(newNMNode.Name) {
				dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(event)
				// }
			}
		},
	)
}

func (sched *Scheduler) deleteNMNodeFromCache(obj interface{}) {
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
	klog.V(3).InfoS("Detected a Delete event for nmNode", "nmNode", nmNode.Name)

	if err := sched.commonCache.RemoveNMNode(nmNode); err != nil {
		klog.InfoS("Failed to remove NMNode from Scheduler cache", "err", err)
	}
}

func (sched *Scheduler) addCNRToCache(obj interface{}) {
	cnr, ok := obj.(*katalystv1alpha1.CustomNodeResource)
	if !ok {
		klog.InfoS("Failed to convert to *katalystv1alpha1.CustomNodeResource", "object", obj)
		return
	}

	klog.V(3).InfoS("Detected an add event", "cnr", cnr.Name)

	if err := sched.commonCache.AddCNR(cnr); err != nil {
		klog.InfoS("Failed to add CNR to Scheduler cache", "err", err)
		return
	}

	sched.ScheduleSwitch.Process(
		ParseSwitchTypeForCNR(cnr),
		func(dataSet ScheduleDataSet) {
			// TODO: revisit this.
			// Comment out this if-condition for now and remove this logic when the physical is completely removed.
			// if sched.nodeManagedByThisScheduler(cnr.Name) {
			klog.V(3).InfoS("Detected an Add event for cnr", "cnr", cnr.Name, "type", dataSet.Type())
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.CNRAdd)
			// }
		},
	)
}

func (sched *Scheduler) updateCNRInCache(oldObj, newObj interface{}) {
	oldCNR, ok := oldObj.(*katalystv1alpha1.CustomNodeResource)
	if !ok {
		klog.InfoS("Failed to convert oldObj to *katalystv1alpha1.CustomNodeResource", "oldObject", oldObj)
		return
	}
	newCNR, ok := newObj.(*katalystv1alpha1.CustomNodeResource)
	if !ok {
		klog.InfoS("Failed to convert newObj to *katalystv1alpha1.CustomNodeResource", "newObject", newObj)
		return
	}

	klog.V(3).InfoS("Detected an update event for cnr", "cnr", oldCNR.Name)

	if err := sched.commonCache.UpdateCNR(oldCNR, newCNR); err != nil {
		klog.InfoS("Failed to update CNR in scheduler cache", "err", err)
	}

	sched.ScheduleSwitch.Process(
		ParseSwitchTypeForCNR(newCNR),
		func(dataSet ScheduleDataSet) {
			// Only activate unschedulable pods if the node became more schedulable.
			// We skip the node property comparison when there is no unschedulable pods in the queue
			// to save processing cycles. We still trigger a move to active queue to cover the case
			// that a pod being processed by the scheduler is determined unschedulable. We want this
			// pod to be reevaluated when a change in the cluster happens.
			// Because pod preemption among all nodes, we should trigger a move as well.
			if dataSet.SchedulingQueue().NumUnschedulableUnits() == 0 {
				return
			} else if event := cnrSchedulingPropertiesChanged(newCNR, oldCNR); event != "" {
				klog.V(3).InfoS("Detected an Update event for cnr", "cnr", newCNR.Name, "type", dataSet.Type())
				// TODO: revisit this.
				// Comment out this if-condition for now and remove this logic when the physical is completely removed.
				// if sched.nodeManagedByThisScheduler(newCNR.Name) {
				dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(event)
				// }
			}
		},
	)
}

func (sched *Scheduler) deleteCNRFromCache(obj interface{}) {
	var cnr *katalystv1alpha1.CustomNodeResource
	switch t := obj.(type) {
	case *katalystv1alpha1.CustomNodeResource:
		cnr = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		cnr, ok = t.Obj.(*katalystv1alpha1.CustomNodeResource)
		if !ok {
			klog.InfoS("Failed to convert to *katalystv1alpha1.CustomNodeResource", "object", t.Obj)
			return
		}
	default:
		klog.InfoS("Failed to convert to *katalystv1alpha1.CustomNodeResource", "type", t)
		return
	}
	klog.V(3).InfoS("Detected a Delete event for cnr", "cnr", cnr.Name)

	if err := sched.commonCache.RemoveCNR(cnr); err != nil {
		klog.InfoS("Failed to remove CNR from scheduler cache", "err", err)
	}
}

func (sched *Scheduler) addPodGroupToCache(obj interface{}) {
	podGroup, ok := obj.(*schedulingv1a1.PodGroup)
	if !ok {
		klog.InfoS("Failed to convert obj to *v1alpha1.PodGroup", "object", obj)
		return
	}

	klog.V(3).InfoS("Detected an Add event for pod group", "podGroup", klog.KObj(podGroup))

	if err := sched.commonCache.AddPodGroup(podGroup); err != nil {
		klog.InfoS("Failed to add pod group to scheduler cache", "err", err)
		return
	}

	sched.ScheduleSwitch.Process(
		// TODO: Parse SwitchType for PodGroup
		framework.SwitchTypeAll,
		func(dataSet ScheduleDataSet) {
			// Pods created when there are no PodGroup available will be stuck in
			// unschedulable queue. Since job controller almost create pod group and pods at the same time,
			// it will not trigger events to schedule pod again if they are failed at PreFilter phase.
			// So we need to move pods to active queue on PodGroupUpdate for this scenario.
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.PodGroupAdd)
			dataSet.SchedulingQueue().ActivePodGroupUnit(unitutil.GetPodGroupKey(podGroup))
		},
	)
}

func (sched *Scheduler) updatePodGroupToCache(oldObj interface{}, newObj interface{}) {
	oldPodGroup, ok := oldObj.(*schedulingv1a1.PodGroup)
	if !ok {
		klog.InfoS("Failed to convert oldObj to *v1alpha1.PodGroup", "oldObject", oldObj)
		return
	}
	newPodGroup, ok := newObj.(*schedulingv1a1.PodGroup)
	if !ok {
		klog.InfoS("Failed to convert newObj to *v1alpha1.PodGroup", "newObject", newObj)
		return
	}

	if oldPodGroup.UID != newPodGroup.UID {
		sched.deletePodGroupFromCache(oldPodGroup)
		sched.addPodGroupToCache(oldPodGroup)
	}

	klog.V(3).InfoS("Detected an update event for pod group", "podGroup", klog.KObj(newPodGroup))

	if err := sched.commonCache.UpdatePodGroup(oldPodGroup, newPodGroup); err != nil {
		klog.InfoS("Failed to update pod group in scheduler cache", "err", err)
		return
	}

	sched.ScheduleSwitch.Process(
		// TODO: Parse SwitchType for PodGroup
		framework.SwitchTypeAll,
		func(dataSet ScheduleDataSet) {
			// Pods created with the associated timeout PodGroup will be stuck in
			// unschedulable queue. Since owner may change pod group status later,
			// it will not trigger events to schedule pod again if they are failed at PreFilter phase.
			// So we need to move pods to active queue on PodGroupUpdate for this scenario.
			dataSet.SchedulingQueue().MoveAllToActiveOrBackoffQueue(util.PodGroupUpdate)
			dataSet.SchedulingQueue().ActivePodGroupUnit(unitutil.GetPodGroupKey(newPodGroup))
		},
	)
}

func (sched *Scheduler) deletePodGroupFromCache(obj interface{}) {
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

	klog.V(3).InfoS("Detected a Delete event for pod group", "podGroup", klog.KObj(podGroup))

	if err := sched.commonCache.RemovePodGroup(podGroup); err != nil {
		klog.InfoS("Failed to remove pod group from scheduler cache", "err", err)
	}
}

func nodeAllocatableChanged(newNode *v1.Node, oldNode *v1.Node) bool {
	return !reflect.DeepEqual(oldNode.Status.Allocatable, newNode.Status.Allocatable)
}

func nodeLabelsChanged(newNode *v1.Node, oldNode *v1.Node) bool {
	return !reflect.DeepEqual(oldNode.GetLabels(), newNode.GetLabels())
}

func nodeTaintsChanged(newNode *v1.Node, oldNode *v1.Node) bool {
	return !reflect.DeepEqual(newNode.Spec.Taints, oldNode.Spec.Taints)
}

func nodeSchedulableChanged(newNode *v1.Node, oldNode *v1.Node) bool {
	return newNode.Spec.Unschedulable != oldNode.Spec.Unschedulable && !newNode.Spec.Unschedulable
}

func nodeConditionsChanged(newNode *v1.Node, oldNode *v1.Node) bool {
	strip := func(conditions []v1.NodeCondition) map[v1.NodeConditionType]v1.ConditionStatus {
		conditionStatuses := make(map[v1.NodeConditionType]v1.ConditionStatus, len(conditions))
		for i := range conditions {
			conditionStatuses[conditions[i].Type] = conditions[i].Status
		}
		return conditionStatuses
	}
	return !reflect.DeepEqual(strip(oldNode.Status.Conditions), strip(newNode.Status.Conditions))
}

func nmNodeAllocatableChanged(newNMNode, oldNMNode *nodev1alpha1.NMNode) bool {
	return !reflect.DeepEqual(newNMNode.Status.ResourceAllocatable, oldNMNode.Status.ResourceAllocatable)
}

func nmNodeLabelsChanged(newNMNode, oldNMNode *nodev1alpha1.NMNode) bool {
	return !reflect.DeepEqual(oldNMNode.GetLabels(), newNMNode.GetLabels())
}

func nmNodeConditionsChanged(newNMNode, oldNMNode *nodev1alpha1.NMNode) bool {
	strip := func(conditions []*v1.NodeCondition) map[v1.NodeConditionType]v1.ConditionStatus {
		conditionStatuses := make(map[v1.NodeConditionType]v1.ConditionStatus, len(conditions))
		for i := range conditions {
			conditionStatuses[conditions[i].Type] = conditions[i].Status
		}
		return conditionStatuses
	}
	return !reflect.DeepEqual(strip(oldNMNode.Status.NodeCondition),
		strip(newNMNode.Status.NodeCondition))
}

// TODO: may be necessary in the future
/*func CNRConditionsChanged(newCNR *v1alpha1.CNR, oldCNR *v1alpha1.CNR) bool {
	strip := func(condition *v1.NodeCondition) map[v1.NodeConditionType]v1.ConditionStatus {
		conditionStatuses := map[v1.NodeConditionType]v1.ConditionStatus{}
		if condition != nil {
			conditionStatuses[condition.Type] = condition.Status
		}
		return conditionStatuses
	}
	return !reflect.DeepEqual(strip(oldCNR.Status.NMPerspectiveNodeCondition),
		strip(newCNR.Status.NMPerspectiveNodeCondition))
}*/

// nodeManagedByThisScheduler finds whether the scheduler can schedule pod to node
// if node partition type is logical, nodeManagedByThisScheduler always return true;
// if node partition type is physical and node is in node partition according to schedulercache, nodeManagedByThisScheduler return true, else return false
// This is called after cache operation, so it is ok to check whether this node is in scheduler's partition based on cache
// TODO: if this is called in other places, revisit this
func (sched *Scheduler) nodeManagedByThisScheduler(nodeName string) bool {
	return sched.commonCache.NodeInThisPartition(nodeName)
}

func nodeSchedulingPropertiesChange(newNode *v1.Node, oldNode *v1.Node) string {
	if nodeSchedulableChanged(newNode, oldNode) {
		return util.NodeSpecUnschedulableChange
	}
	if nodeAllocatableChanged(newNode, oldNode) {
		return util.NodeAllocatableChange
	}
	if nodeLabelsChanged(newNode, oldNode) {
		return util.NodeLabelChange
	}
	if nodeTaintsChanged(newNode, oldNode) {
		return util.NodeTaintChange
	}
	if nodeConditionsChanged(newNode, oldNode) {
		return util.NodeConditionChange
	}

	return ""
}

func nmNodeSchedulingPropertiesChange(newNMNode, oldNMNode *nodev1alpha1.NMNode) string {
	if nmNodeAllocatableChanged(newNMNode, oldNMNode) {
		return util.NodeAllocatableChange
	}
	if nmNodeLabelsChanged(newNMNode, oldNMNode) {
		return util.NodeLabelChange
	}
	if nmNodeConditionsChanged(newNMNode, oldNMNode) {
		return util.NodeConditionChange
	}

	return ""
}

func cnrAllocatableChanged(newCNR *katalystv1alpha1.CustomNodeResource, oldCNR *katalystv1alpha1.CustomNodeResource) bool {
	return !reflect.DeepEqual(oldCNR.Status.Resources.Allocatable, newCNR.Status.Resources.Allocatable) ||
		!reflect.DeepEqual(oldCNR.Spec.NodeResourceProperties, newCNR.Spec.NodeResourceProperties)
}

// TODO: find more properties which may change scheduling decisions
func cnrSchedulingPropertiesChanged(newCNR *katalystv1alpha1.CustomNodeResource, oldCNR *katalystv1alpha1.CustomNodeResource) string {
	if cnrAllocatableChanged(newCNR, oldCNR) {
		return util.NodeAllocatableChange
	}

	return ""
}

// skipPodUpdate checks whether the specified pod update should be ignored.
// This function will return true if
//   - The pod has already been assumed: pod is already in assumed cache or pod is set to assumed or preempted by annotation, AND
//   - The pod has only its ResourceVersion, Spec.NodeName, Annotations, ManagedFields, Finalizers and/or Conditions updated.
func (sched *Scheduler) skipPodUpdate(pod *v1.Pod) bool {
	// Non-assumed pods should never be skipped.
	isAssumed, err := sched.commonCache.IsAssumedPod(pod)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to check whether pod %s/%s is assumed: %v", pod.Namespace, pod.Name, err))
		return false
	}
	isAssumed = isAssumed || podutil.AssumedPod(pod) || podutil.BoundPod(pod)
	if isAssumed {
		klog.V(3).InfoS("Skipping assumed pod update", "pod", klog.KObj(pod))
		return true
	}
	return false
}

func (sched *Scheduler) onPdbAdd(obj interface{}) {
	pdb, ok := obj.(*policy.PodDisruptionBudget)
	if !ok {
		klog.InfoS("Failed to convert to *policy.PodDisruptionBudget", "object", obj)
		return
	}

	klog.V(3).InfoS("Detected an Add event for pdb, disruptions allowed", "pdb", klog.KObj(pdb), "disruptionsAllowed", pdb.Status.DisruptionsAllowed)

	if err := sched.commonCache.AddPDB(pdb); err != nil {
		klog.InfoS("Failed to add pdb", "pdb", klog.KObj(pdb), "err", err)
	}
}

func (sched *Scheduler) onPdbUpdate(oldObj, newObj interface{}) {
	oldPdb, ok := oldObj.(*policy.PodDisruptionBudget)
	if !ok {
		klog.InfoS("Failed to convert to *policy.PodDisruptionBudget", "oldObject", oldObj)
		return
	}
	newPdb, ok := newObj.(*policy.PodDisruptionBudget)
	if !ok {
		klog.InfoS("Failed to convert to *policy.PodDisruptionBudget", "newObject", newObj)
		return
	}
	klog.V(3).InfoS("Detected an Update event for pdb, disruptions allowed status changed", "pdb", klog.KObj(newPdb), "oldPdbDisruptionsAllowed", oldPdb.Status.DisruptionsAllowed, "newPdbDisruptionsAllowed", newPdb.Status.DisruptionsAllowed)

	if err := sched.commonCache.UpdatePDB(oldPdb, newPdb); err != nil {
		klog.InfoS("Failed to update pdb", "oldPdb", klog.KObj(oldPdb), "newPdb", klog.KObj(newPdb), "err", err)
	}
}

func (sched *Scheduler) onPdbDelete(obj interface{}) {
	pdb, ok := obj.(*policy.PodDisruptionBudget)
	if !ok {
		klog.InfoS("Failed to convert to *policy.PodDisruptionBudget", "object", obj)
		return
	}
	klog.V(3).InfoS("Detected a Delete event for pdb with disruptions allowed status", "pdb", klog.KObj(pdb), "disruptionsAllowed", pdb.Status.DisruptionsAllowed)

	if err := sched.commonCache.DeletePDB(pdb); err != nil {
		klog.InfoS("Failed to delete pdb", "pdb", klog.KObj(pdb))
	}
}

func (sched *Scheduler) onReplicaSetAdd(obj interface{}) {
	rs, ok := obj.(*appsv1.ReplicaSet)
	if !ok {
		klog.InfoS("Failed to convert to *appsv1.ReplicaSet", "object", obj)
		return
	}
	klog.V(3).InfoS("Detected an Add event for replicaset", "replicaSet", klog.KObj(rs))

	sched.commonCache.AddOwner(util.OwnerTypeReplicaSet, util.GetReplicaSetKey(rs), rs.GetLabels())
}

func (sched *Scheduler) onReplicaSetUpdate(oldObj, newObj interface{}) {
	oldRS, ok := oldObj.(*appsv1.ReplicaSet)
	if !ok {
		klog.InfoS("Failed to convert to *appsv1.ReplicaSet", "oldObject", oldObj)
		return
	}
	newRS, ok := newObj.(*appsv1.ReplicaSet)
	if !ok {
		klog.InfoS("Failed to convert to *appsv1.ReplicaSet", "newObject", newObj)
		return
	}
	klog.V(3).InfoS("Detected an Update event for replicaset", "replicaSet", klog.KObj(newRS))

	sched.commonCache.UpdateOwner(util.OwnerTypeReplicaSet, util.GetReplicaSetKey(newRS), oldRS.GetLabels(), newRS.GetLabels())
}

func (sched *Scheduler) onReplicaSetDelete(obj interface{}) {
	rs, ok := obj.(*appsv1.ReplicaSet)
	if !ok {
		klog.InfoS("Failed to convert to *appsv1.ReplicaSet", "object", obj)
		return
	}
	klog.V(3).InfoS("Detected a Delete event for replicaset", "replicaSet", klog.KObj(rs))

	sched.commonCache.DeleteOwner(util.OwnerTypeReplicaSet, util.GetReplicaSetKey(rs))
}

func (sched *Scheduler) onDaemonSetAdd(obj interface{}) {
	ds, ok := obj.(*appsv1.DaemonSet)
	if !ok {
		klog.InfoS("Failed to convert to *appsv1.DaemonSet", "object", obj)
		return
	}
	klog.V(3).InfoS("Detected an Add event for daemonset", "daemonSet", klog.KObj(ds))

	sched.commonCache.AddOwner(util.OwnerTypeDaemonSet, util.GetDaemonSetKey(ds), ds.GetLabels())
}

func (sched *Scheduler) onDaemonSetUpdate(oldObj, newObj interface{}) {
	oldDS, ok := oldObj.(*appsv1.DaemonSet)
	if !ok {
		klog.InfoS("Failed to convert to *appsv1.DaemonSet", "oldObject", oldObj)
		return
	}
	newDS, ok := newObj.(*appsv1.DaemonSet)
	if !ok {
		klog.InfoS("Failed to convert to *appsv1.DaemonSet", "newObject", newObj)
		return
	}
	klog.V(3).InfoS("Detected an Update event for daemonset", "daemonSet", klog.KObj(newDS))

	sched.commonCache.UpdateOwner(util.OwnerTypeDaemonSet, util.GetDaemonSetKey(newDS), oldDS.GetLabels(), newDS.GetLabels())
}

func (sched *Scheduler) onDaemonSetDelete(obj interface{}) {
	ds, ok := obj.(*appsv1.DaemonSet)
	if !ok {
		klog.InfoS("Failed to convert to *appsv1.DaemonSet", "object", obj)
		return
	}
	klog.V(3).InfoS("Detected a Delete event for daemonset", "daemonSet", klog.KObj(ds))

	sched.commonCache.DeleteOwner(util.OwnerTypeDaemonSet, util.GetDaemonSetKey(ds))
}

// addAllEventHandlers is a helper function used in tests and in Scheduler
// to add event handlers for various informers.
func addAllEventHandlers(
	sched *Scheduler,
	informerFactory informers.SharedInformerFactory,
	crdInformerFactory crdinformers.SharedInformerFactory,
	katalystCrdInformerFactory katalystinformers.SharedInformerFactory,
) {
	podInformer := informerFactory.Core().V1().Pods().Informer()
	podInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    sched.addPod,
			UpdateFunc: sched.updatePod,
			DeleteFunc: sched.deletePod,
		},
	)

	crdInformerFactory.Node().V1alpha1().NMNodes().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    sched.addNMNodeToCache,
			UpdateFunc: sched.updateNMNodeInCache,
			DeleteFunc: sched.deleteNMNodeFromCache,
		},
	)

	katalystCrdInformerFactory.Node().V1alpha1().CustomNodeResources().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    sched.addCNRToCache,
			UpdateFunc: sched.updateCNRInCache,
			DeleteFunc: sched.deleteCNRFromCache,
		},
	)

	informerFactory.Core().V1().Nodes().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    sched.addNodeToCache,
			UpdateFunc: sched.updateNodeInCache,
			DeleteFunc: sched.deleteNodeFromCache,
		},
	)

	// add Scheduler resource event listener
	crdInformerFactory.Scheduling().V1alpha1().Schedulers().Informer().AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: func(obj interface{}) bool {
				switch t := obj.(type) {
				case *schedulingv1a1.Scheduler:
					return t.Name == sched.Name
				case cache.DeletedFinalStateUnknown:
					if scheduler, ok := t.Obj.(*schedulingv1a1.Scheduler); ok {
						return scheduler.Name == sched.Name
					}
					utilruntime.HandleError(fmt.Errorf("unable to convert object %T to *v1alpha1.Scheduler in %T", obj, sched))
					return false
				default:
					utilruntime.HandleError(fmt.Errorf("unable to handle object in %T: %T", sched, obj))
					return false
				}
			},
			Handler: cache.ResourceEventHandlerFuncs{
				UpdateFunc: sched.onSchedulerUpdate,
			},
		},
	)

	if utilfeature.DefaultFeatureGate.Enabled(features.CSINodeInfo) {
		informerFactory.Storage().V1().CSINodes().Informer().AddEventHandler(
			cache.ResourceEventHandlerFuncs{
				AddFunc:    sched.onCSINodeAdd,
				UpdateFunc: sched.onCSINodeUpdate,
			},
		)
	}

	// On add and delete of PVs, it will affect equivalence cache items
	// related to persistent volume
	informerFactory.Core().V1().PersistentVolumes().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			// MaxPDVolumeCountPredicate: since it relies on the counts of PV.
			AddFunc:    sched.onPvAdd,
			UpdateFunc: sched.onPvUpdate,
		},
	)

	// This is for MaxPDVolumeCountPredicate: add/delete PVC will affect counts of PV when it is bound.
	informerFactory.Core().V1().PersistentVolumeClaims().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    sched.onPvcAdd,
			UpdateFunc: sched.onPvcUpdate,
		},
	)

	// This is for ServiceAffinity: affected by the selector of the service is updated.
	// Also, if new service is added, equivalence cache will also become invalid since
	// existing pods may be "captured" by this service and change this predicate result.
	informerFactory.Core().V1().Services().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    sched.onServiceAdd,
			UpdateFunc: sched.onServiceUpdate,
			DeleteFunc: sched.onServiceDelete,
		},
	)

	if sched.mayHasPreemption {
		informerFactory.Policy().V1().PodDisruptionBudgets().Informer().AddEventHandler(
			cache.ResourceEventHandlerFuncs{
				AddFunc:    sched.onPdbAdd,
				UpdateFunc: sched.onPdbUpdate,
				DeleteFunc: sched.onPdbDelete,
			},
		)

		informerFactory.Apps().V1().ReplicaSets().Informer().AddEventHandler(
			cache.ResourceEventHandlerFuncs{
				AddFunc:    sched.onReplicaSetAdd,
				UpdateFunc: sched.onReplicaSetUpdate,
				DeleteFunc: sched.onReplicaSetDelete,
			},
		)

		informerFactory.Apps().V1().DaemonSets().Informer().AddEventHandler(
			cache.ResourceEventHandlerFuncs{
				AddFunc:    sched.onDaemonSetAdd,
				UpdateFunc: sched.onDaemonSetUpdate,
				DeleteFunc: sched.onDaemonSetDelete,
			},
		)
	}

	informerFactory.Storage().V1().StorageClasses().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: sched.onStorageClassAdd,
		},
	)

	// add PodGroup resource event listener
	crdInformerFactory.Scheduling().V1alpha1().PodGroups().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    sched.addPodGroupToCache,
			UpdateFunc: sched.updatePodGroupToCache,
			DeleteFunc: sched.deletePodGroupFromCache,
		},
	)
}
