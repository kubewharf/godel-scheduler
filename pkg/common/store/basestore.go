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

package store

import (
	"sync"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

type StoreType int

const (
	Cache StoreType = iota
	Snapshot
)

type BaseStore interface {
	commoncache.ClusterEventsHandler
	AssumePod(*framework.CachePodInfo) error
	ForgetPod(*framework.CachePodInfo) error

	PeriodWorker(mu *sync.RWMutex)
}

type Store interface {
	BaseStore

	Name() StoreName
	UpdateSnapshot(Store) error
}

type BaseStoreImpl struct{}

var _ BaseStore = &BaseStoreImpl{}

func NewBaseStore() BaseStore { return &BaseStoreImpl{} }

func (i *BaseStoreImpl) AddPod(pod *v1.Pod) error                                     { return nil }
func (i *BaseStoreImpl) UpdatePod(oldPod, newPod *v1.Pod) error                       { return nil }
func (i *BaseStoreImpl) DeletePod(pod *v1.Pod) error                                  { return nil }
func (i *BaseStoreImpl) AddNode(node *v1.Node) error                                  { return nil }
func (i *BaseStoreImpl) UpdateNode(oldNode, newNode *v1.Node) error                   { return nil }
func (i *BaseStoreImpl) DeleteNode(node *v1.Node) error                               { return nil }
func (i *BaseStoreImpl) AddNMNode(nmNode *nodev1alpha1.NMNode) error                  { return nil }
func (i *BaseStoreImpl) UpdateNMNode(oldNMNode, newNMNode *nodev1alpha1.NMNode) error { return nil }
func (i *BaseStoreImpl) DeleteNMNode(nmNode *nodev1alpha1.NMNode) error               { return nil }
func (i *BaseStoreImpl) AddCNR(cnr *katalystv1alpha1.CustomNodeResource) error        { return nil }
func (i *BaseStoreImpl) UpdateCNR(oldCNR, newCNR *katalystv1alpha1.CustomNodeResource) error {
	return nil
}
func (i *BaseStoreImpl) DeleteCNR(cnr *katalystv1alpha1.CustomNodeResource) error { return nil }
func (i *BaseStoreImpl) AddPodGroup(podGroup *schedulingv1a1.PodGroup) error      { return nil }
func (i *BaseStoreImpl) UpdatePodGroup(oldPodGroup, newPodGroup *schedulingv1a1.PodGroup) error {
	return nil
}
func (i *BaseStoreImpl) DeletePodGroup(podGroup *schedulingv1a1.PodGroup) error         { return nil }
func (i *BaseStoreImpl) AddPDB(pdb *policy.PodDisruptionBudget) error                   { return nil }
func (i *BaseStoreImpl) UpdatePDB(oldPdb, newPdb *policy.PodDisruptionBudget) error     { return nil }
func (i *BaseStoreImpl) DeletePDB(pdb *policy.PodDisruptionBudget) error                { return nil }
func (i *BaseStoreImpl) AddOwner(ownerType, key string, labels map[string]string) error { return nil }
func (i *BaseStoreImpl) UpdateOwner(ownerType, key string, oldLabels, newLabels map[string]string) error {
	return nil
}
func (i *BaseStoreImpl) DeleteOwner(ownerType, key string) error                { return nil }
func (i *BaseStoreImpl) AddMovement(movement *schedulingv1a1.Movement) error    { return nil }
func (i *BaseStoreImpl) UpdateMovement(_, _ *schedulingv1a1.Movement) error     { return nil }
func (i *BaseStoreImpl) DeleteMovement(movement *schedulingv1a1.Movement) error { return nil }

func (i *BaseStoreImpl) AssumePod(podInfo *framework.CachePodInfo) error { return nil }
func (i *BaseStoreImpl) ForgetPod(podInfo *framework.CachePodInfo) error { return nil }
func (i *BaseStoreImpl) PeriodWorker(mu *sync.RWMutex)                   { return }
