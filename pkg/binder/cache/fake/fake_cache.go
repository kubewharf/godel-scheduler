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

	binderutils "github.com/kubewharf/godel-scheduler/pkg/binder/utils"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

// BinderCache is used for testing
type Cache struct {
	AssumeFunc       func(*v1.Pod) error
	ForgetFunc       func(*v1.Pod)
	IsAssumedPodFunc func(*v1.Pod) bool
	GetPodFunc       func(*v1.Pod) *v1.Pod
	GetNodeFunc      func(string) (framework.NodeInfo, error)
	binderutils.UnitStatusMap
}

// AssumePod is a fake method for testing.
func (c *Cache) AssumePod(pod *v1.Pod) error {
	return c.AssumeFunc(pod)
}

// FinishBinding is a fake method for testing.
func (c *Cache) FinishBinding(pod *v1.Pod) error { return nil }

// ForgetPod is a fake method for testing.
func (c *Cache) ForgetPod(pod *v1.Pod) error {
	c.ForgetFunc(pod)
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

// RemoveNode is a fake method for testing.
func (c *Cache) RemoveNode(node *v1.Node) error { return nil }

// RemoveNMNode is a fake method for testing.
func (c *Cache) RemoveNMNode(nmNode *nodev1alpha1.NMNode) error { return nil }

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

// RemoveCNR removes custom resource information about node.
func (c *Cache) RemoveCNR(cnr *katalystv1alpha1.CustomNodeResource) error {
	return nil
}

func (c *Cache) GetNode(nodename string) (framework.NodeInfo, error) {
	return c.GetNodeFunc(nodename)
}

func (c *Cache) GetPodGroupPods(podGroupName string) []*v1.Pod {
	return nil
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

func (c *Cache) GetPDBItems() []framework.PDBItem {
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
