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

package utils

import (
	"context"
	"time"

	v1 "k8s.io/api/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

type BinderUnitInfo struct {
	*framework.QueuedUnitInfo

	FailedPods []*FailedPod

	// pods belong to the unit which are not in failed status or preemptors.
	BindingPods []*BindingPod

	Preemptors []*Preemptor

	NodeToVictims map[string][]*v1.Pod
}

type BindingPod struct {
	PodInfo *framework.QueuedPodInfo
	Ctx     *PodCtx
	// if it's true, BindingPod will be wrapped as a Preemptor
	IsPreemptor bool
	// placeholder pod for resource reservation.
	PlaceholderPod *v1.Pod
}

type Preemptor struct {
	*BindingPod
	VictimPods []*v1.Pod
}

func NewBindingPod(pInfo *framework.QueuedPodInfo) *BindingPod {
	return &BindingPod{
		PodInfo:        pInfo,
		Ctx:            &PodCtx{},
		IsPreemptor:    false,
		PlaceholderPod: nil,
	}
}

type FailedPod struct {
	PodInfo *framework.QueuedPodInfo
	// placeholder pod for resource reservation.
	PlaceholderPod *v1.Pod
	Err            error
}

type PodCtx struct {
	Ctx               context.Context
	State             *framework.CycleState
	CheckConflictTime time.Time
	Framework         framework.BinderFramework
	AllBound          bool
	SpanContext       tracing.SpanContext
	SuggestedHost     string
}
