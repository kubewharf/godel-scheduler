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

package fake

import (
	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

// BinderCache is used for testing
type Cache struct {
	AssumeFunc       func(*v1.Pod)
	ForgetFunc       func(*v1.Pod)
	IsAssumedPodFunc func(*v1.Pod) bool
	IsCachedPodFunc  func(*v1.Pod) bool
	GetPodFunc       func(*v1.Pod) *v1.Pod
	UnitStatus       *unitstatus.UnitStatusMap
}

// AssumePod is a fake method for testing.
func (c *Cache) AssumePod(podInfo *framework.CachePodInfo) error {
	c.AssumeFunc(podInfo.Pod)
	return nil
}

// FinishReserving is a fake method for testing.
func (c *Cache) FinishReserving(pod *v1.Pod) error { return nil }

// ForgetPod is a fake method for testing.
func (c *Cache) ForgetPod(podInfo *framework.CachePodInfo) error {
	c.ForgetFunc(podInfo.Pod)
	return nil
}

// AddPod is a fake method for testing.
func (c *Cache) AddPod(pod *v1.Pod) error { return nil }

// UpdatePod is a fake method for testing.
func (c *Cache) UpdatePod(oldPod, newPod *v1.Pod) error { return nil }

// RemovePod is a fake method for testing.
func (c *Cache) RemovePod(pod *v1.Pod) error { return nil }

// IsAssumedPod is a fake method for testing.
func (c *Cache) IsAssumedPod(pod *v1.Pod) (bool, error) {
	return c.IsAssumedPodFunc(pod), nil
}

// IsCachedPod is a fake method for testing.
func (c *Cache) IsCachedPod(pod *v1.Pod) (bool, error) {
	return c.IsCachedPodFunc(pod), nil
}

// GetPod is a fake method for testing.
func (c *Cache) GetPod(pod *v1.Pod) (*v1.Pod, error) {
	return c.GetPodFunc(pod), nil
}

// AddNode is a fake method for testing.
func (c *Cache) AddNode(node *v1.Node) error { return nil }

// UpdateNode is a fake method for testing.
func (c *Cache) UpdateNode(oldNode, newNode *v1.Node) error { return nil }

// RemoveNode is a fake method for testing.
func (c *Cache) RemoveNode(node *v1.Node) error { return nil }

// AddNMNode is a fake method for testing.
func (c *Cache) AddNMNode(nmDode *nodev1alpha1.NMNode) error { return nil }

// UpdateNMNode is a fake method for testing.
func (c *Cache) UpdateNMNode(oldNMNode, newNMNode *nodev1alpha1.NMNode) error { return nil }

// RemoveNMNode is a fake method for testing.
func (c *Cache) RemoveNMNode(nmNode *nodev1alpha1.NMNode) error { return nil }

// UpdateSnapshot is a fake method for testing.
func (c *Cache) UpdateSnapshot(snapshot *godelcache.Snapshot) error {
	return nil
}

// PodCount is a fake method for testing.
func (c *Cache) PodCount() (int, error) { return 0, nil }

// AddCNR adds custom resource information about node
func (c *Cache) AddCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	return nil
}

// UpdateCNR updates custom resource information about node.
func (c *Cache) UpdateCNR(oldCNR, newCNR *katalystv1alpha1.CustomNodeResource) error {
	return nil
}

// RemoveCNR removes custom resource information about node.
func (c *Cache) RemoveCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	return nil
}

func (c *Cache) NodeInThisPartition(nodeName string) bool {
	return true
}

// SetNodeInPartition sets node in partition of scheduler
func (c *Cache) SetNodeInPartition(nodeName string) error {
	return nil
}

// SetNodeOutOfPartition sets node out of partition of scheduler
func (c *Cache) SetNodeOutOfPartition(nodeName string) error {
	return nil
}

func (c *Cache) CacheNodeForPodOwner(podOwner string, nodeName string, nodeGroup string) error {
	return nil
}

func (c *Cache) CacheAscendingOrderNodesForPodOwner(podOwner string, nodeGroup string, nodeNames []string) error {
	return nil
}

// IsNodeInCachedMap checks if the node is still in cached nodes
func (c *Cache) IsNodeInCachedMap(podOwner string, nodeName string) (bool, string) {
	return false, ""
}

// GetOrderedNodesForPodOwner gets ordered nodes from cached nodes
func (c *Cache) GetOrderedNodesForPodOwner(podOwner string) []string {
	return nil
}

// DeleteNodeForPodOwner deletes node from cached nodes
func (c *Cache) DeleteNodeForPodOwner(podOwner string, nodeName string) error {
	return nil
}

func (c *Cache) Dump() *commoncache.Dump {
	return &commoncache.Dump{}
}

func (c *Cache) AddPodGroup(podGroup *schedulingv1a1.PodGroup) error {
	return nil
}

func (c *Cache) UpdatePodGroup(oldPodGroup, newPodGroup *schedulingv1a1.PodGroup) error {
	return nil
}

func (c *Cache) RemovePodGroup(podGroup *schedulingv1a1.PodGroup) error {
	return nil
}

func (c *Cache) AddNonTerminatingPodToQuota(pod *v1.Pod, queueName, schedulerName string) error {
	return nil
}

func (c *Cache) UpdateNonTerminatingPodInQuota(oldPod, newPod *v1.Pod, newQueueName, schedulerName string) error {
	return nil
}

func (c *Cache) DeletePodFromQuota(pod *v1.Pod) error {
	return nil
}

func (c *Cache) SetUnitSchedulingStatus(unitKey string, status unitstatus.SchedulingStatus) {
	c.UnitStatus.SetUnitSchedulingStatus(unitKey, status)
}

func (c *Cache) GetUnitSchedulingStatus(unitKey string) unitstatus.SchedulingStatus {
	return c.UnitStatus.GetUnitSchedulingStatus(unitKey)
}

func (cache *Cache) GetUnitStatus(unitKey string) unitstatus.UnitStatus {
	return cache.UnitStatus.GetUnitStatus(unitKey)
}

func (c *Cache) AddPDB(pdb *policy.PodDisruptionBudget) error {
	return nil
}

func (c *Cache) UpdatePDB(oldPdb, newPdb *policy.PodDisruptionBudget) error {
	return nil
}

func (c *Cache) DeletePDB(pdb *policy.PodDisruptionBudget) error {
	return nil
}

func (c *Cache) GetPDBItemList() []framework.PDBItem {
	return []framework.PDBItem{}
}

func (c *Cache) AddOwner(ownerType, ownerKey string, labels map[string]string) error {
	return nil
}

func (c *Cache) DeleteOwner(ownerType, ownerKey string) error {
	return nil
}

func (c *Cache) UpdateOwner(ownerType, ownerKey string, oldLabels, newLabels map[string]string) error {
	return nil
}

func (c *Cache) ScrapeCollectable(_ generationstore.RawStore) {
}
