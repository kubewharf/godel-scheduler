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

package reservation

import (
	"fmt"

	v1 "k8s.io/api/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

type unitState struct {
	// nodeName->fakePod
	index                   framework.ReservationPlaceholdersOfNodes
	unavailablePlaceholders map[string]*v1.Pod
}

func (state *unitState) removeUnavailableFakePods(nodeName, fakePodKey string) error {
	fakePods, ok := state.index[nodeName]
	if !ok || len(fakePods) == 0 {
		return fmt.Errorf("fakePod not found in unit state on node %s, fakePod key: %s", nodeName, fakePodKey)
	}

	fakePod := fakePods[fakePodKey]
	state.unavailablePlaceholders[fakePodKey] = fakePod

	delete(state.index[nodeName], fakePodKey)
	if len(state.index[nodeName]) == 0 {
		delete(state.index, nodeName)
	}

	return nil
}

func getUnitState(cycleState *framework.CycleState) (*unitState, error) {
	c, err := cycleState.Read(unitStateKey)
	if err != nil {
		return nil, fmt.Errorf("error reading %q from unit cycleState: %v", unitStateKey, err)
	}

	s, ok := c.(*unitState)
	if !ok {
		return nil, fmt.Errorf("%+v convert to Reservation.unitState error", c)
	}
	return s, nil
}

func (state *unitState) Clone() framework.StateData {
	// Do not clone for Reservation.
	return state
}
