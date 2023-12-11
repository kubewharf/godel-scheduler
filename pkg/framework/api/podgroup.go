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

package api

import (
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"

	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
)

type GenerationPodGroup interface {
	SetPodGroup(*schedulingv1a1.PodGroup)
	GetPodGroup() *schedulingv1a1.PodGroup
	Clone() GenerationPodGroup
	generationstore.StoredObj
}

// ATTENTION: GenerationPodGroupImpl.Clone will not deep-copy the PodGroup object.
// The scheduling process CAN NOT modify the PodGroup object returned to it by Snapshot.
type GenerationPodGroupImpl struct {
	podgroup   *schedulingv1a1.PodGroup
	generation int64
}

var _ GenerationPodGroup = &GenerationPodGroupImpl{}

func NewGenerationPodGroup(podgroup *schedulingv1a1.PodGroup) GenerationPodGroup {
	return &GenerationPodGroupImpl{
		podgroup:   podgroup,
		generation: 0,
	}
}

func (i *GenerationPodGroupImpl) SetPodGroup(podgroup *schedulingv1a1.PodGroup) {
	i.podgroup = podgroup
}

func (i *GenerationPodGroupImpl) GetPodGroup() *schedulingv1a1.PodGroup {
	return i.podgroup
}

func (i *GenerationPodGroupImpl) SetGeneration(generation int64) {
	i.generation = generation
}

func (i *GenerationPodGroupImpl) GetGeneration() int64 {
	return i.generation
}

func (i GenerationPodGroupImpl) Clone() GenerationPodGroup {
	return &GenerationPodGroupImpl{
		podgroup:   i.podgroup, // ATTENTION: no need to deepcopy PodGroup because we won't modify this in Snapshot.
		generation: i.generation,
	}
}
