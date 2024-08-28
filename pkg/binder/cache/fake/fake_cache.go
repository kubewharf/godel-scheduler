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
	"fmt"

	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
)

// BinderCache is used for testing
type Cache struct {
	AssumeFunc                        func(*v1.Pod) error
	ForgetFunc                        func(*v1.Pod)
	IsAssumedPodFunc                  func(*v1.Pod) bool
	GetPodFunc                        func(*v1.Pod) *v1.Pod
	GetNodeInfoFunc                   func(string) framework.NodeInfo
	GetAvailablePlaceholderFunc       func(pod *v1.Pod) (*v1.Pod, error)
	FindReservationPlaceHolderPodFunc func(pod *v1.Pod) (*v1.Pod, error)
}

// AssumePod is a fake method for testing.
func (c *Cache) AssumePod(podInfo *framework.CachePodInfo) error {
	return c.AssumeFunc(podInfo.Pod)
}

// FinishBinding is a fake method for testing.
func (c *Cache) FinishBinding(pod *v1.Pod) error { return nil }

// ForgetPod is a fake method for testing.
func (c *Cache) ForgetPod(podInfo *framework.CachePodInfo) error {
	c.ForgetFunc(podInfo.Pod)
	return nil
}

// AddPod is a fake method for testing.
func (c *Cache) AddPod(pod *v1.Pod) error { return nil }

// UpdatePod is a fake method for testing.
func (c *Cache) UpdatePod(oldPod, newPod *v1.Pod) error { return nil }

// DeletePod is a fake method for testing.
func (c *Cache) DeletePod(pod *v1.Pod) error { return nil }

// IsAssumedPod is a fake method for testing.
func (c *Cache) IsAssumedPod(pod *v1.Pod) (bool, error) {
	return c.IsAssumedPodFunc(pod), nil
}

// GetPod is a fake method for testing.
func (c *Cache) GetPod(pod *v1.Pod) (*v1.Pod, error) {
	return c.GetPodFunc(pod), nil
}

func (c *Cache) MarkPodToDelete(pod, preemptor *v1.Pod) error {
	return nil
}

func (c *Cache) IsPodMarkedToDelete(pod *v1.Pod) (bool, error) {
	return true, nil
}

func (c *Cache) RemoveDeletePodMarker(po, preemptor *v1.Pod) error {
	return nil
}

func (c *Cache) RemoveDeletePodMarkerByKey(podKey, preemptorKey string) error {
	return nil
}

// AddNode is a fake method for testing.
func (c *Cache) AddNode(node *v1.Node) error { return nil }

// AddNMNode is a fake method for testing.
func (c *Cache) AddNMNode(nmNode *nodev1alpha1.NMNode) error { return nil }

// UpdateNode is a fake method for testing.
func (c *Cache) UpdateNode(oldNode, newNode *v1.Node) error { return nil }

// UpdateNMNode is a fake method for testing.
func (c *Cache) UpdateNMNode(oldNMNode, newNMNode *nodev1alpha1.NMNode) error { return nil }

// DeleteNode is a fake method for testing.
func (c *Cache) DeleteNode(node *v1.Node) error { return nil }

// DeleteNMNode is a fake method for testing.
func (c *Cache) DeleteNMNode(nmNode *nodev1alpha1.NMNode) error { return nil }

// PodCount is a fake method for testing.
func (c *Cache) PodCount() (int, error) { return 0, nil }

// Dump is a fake method for testing.
func (c *Cache) Dump() *commoncache.Dump {
	return &commoncache.Dump{}
}

// AddCNR adds custom resource information about node
func (c *Cache) AddCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	return nil
}

// UpdateCNR updates custom resource information about node.
func (c *Cache) UpdateCNR(oldCNR, newCNR *katalystv1alpha1.CustomNodeResource) error {
	return nil
}

// DeleteCNR removes custom resource information about node.
func (c *Cache) DeleteCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	return nil
}

func (c *Cache) GetNodeInfo(nodename string) framework.NodeInfo {
	return c.GetNodeInfoFunc(nodename)
}

func (c *Cache) AddPodGroup(podGroup *schedulingv1a1.PodGroup) error {
	return nil
}

func (c *Cache) UpdatePodGroup(oldPodGroup, newPodGroup *schedulingv1a1.PodGroup) error {
	return nil
}

func (c *Cache) DeletePodGroup(podGroup *schedulingv1a1.PodGroup) error {
	return nil
}

func (c *Cache) GetPodGroupInfo(podGroupName string) (*schedulingv1a1.PodGroup, error) {
	return nil, nil
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

func (c *Cache) CheckIfVictimExist(deployNamespace, deployName, victimUID string) bool {
	return false
}

func (c *Cache) GetPDBItemList() []framework.PDBItem {
	return nil
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

func (c *Cache) AddOwner(ownerType, key string, labels map[string]string) error {
	return nil
}

func (c *Cache) UpdateOwner(ownerType, key string, oldLabels, newLabels map[string]string) error {
	return nil
}

func (c *Cache) DeleteOwner(ownerType, key string) error {
	return nil
}

func (c *Cache) GetUnitStatus(unitKey string) unitstatus.UnitStatus { return nil }

func (c *Cache) FindStore(storeName commonstore.StoreName) commonstore.Store {
	return nil
}

func (c *Cache) SetUnitSchedulingStatus(unitKey string, status unitstatus.SchedulingStatus) { return }

func (c *Cache) GetUnitSchedulingStatus(unitKey string) unitstatus.SchedulingStatus {
	return unitstatus.ScheduledStatus
}

func (c *Cache) AddMovement(movement *schedulingv1a1.Movement) error {
	return nil
}

func (c *Cache) UpdateMovement(oldMovement, newMovement *schedulingv1a1.Movement) error {
	return nil
}

func (c *Cache) DeleteMovement(movement *schedulingv1a1.Movement) error {
	return nil
}
func (c *Cache) FindReservationPlaceHolderPod(
	pod *v1.Pod,
) (*v1.Pod, error) {
	if c.FindReservationPlaceHolderPodFunc != nil {
		return c.FindReservationPlaceHolderPodFunc(pod)
	}
	return nil, fmt.Errorf("empty store")
}

func (c *Cache) AddReservation(request *schedulingv1a1.Reservation) error {
	return nil
}

func (c *Cache) UpdateReservation(oldRequest, newRequest *schedulingv1a1.Reservation) error {
	return nil
}

func (c *Cache) DeleteReservation(request *schedulingv1a1.Reservation) error {
	return nil
}

func (c *Cache) GetAvailablePlaceholderPod(
	pod *v1.Pod,
) (*v1.Pod, error) {
	if c.GetAvailablePlaceholderFunc != nil {
		return c.GetAvailablePlaceholderFunc(pod)
	}
	return nil, fmt.Errorf("empty store")
}
