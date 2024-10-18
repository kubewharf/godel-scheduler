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

package rescheduling

import (
	"fmt"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

type ownerRecommondations struct {
	// nodeName->MovementDetailOnNode
	nodeToMovements map[string][]*framework.MovementDetailOnNode
	waitingPodCount int
}

func newOwnerRecommondations() *ownerRecommondations {
	return &ownerRecommondations{
		nodeToMovements: make(map[string][]*framework.MovementDetailOnNode),
	}
}

type unitState struct {
	// ownerKey->ownerRecommondations
	index           map[string]*ownerRecommondations
	choosedMovement string
	algorithmName   string
}

func getUnitState(cycleState *framework.CycleState) (*unitState, error) {
	c, err := cycleState.Read(unitStateKey)
	if err != nil {
		return nil, fmt.Errorf("error reading %q from unit cycleState: %v", unitStateKey, err)
	}

	s, ok := c.(*unitState)
	if !ok {
		return nil, fmt.Errorf("%+v convert to Rescheduling.unitState error", c)
	}
	return s, nil
}

func (state *unitState) Clone() framework.StateData {
	// Do not clone for Rescheduling.
	return state
}
