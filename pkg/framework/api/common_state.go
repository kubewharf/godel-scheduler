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
	"fmt"

	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

const (
	// State data key
	NodePartitionTypeStateKey = "NodePartitionType"
	PodLauncherStateKey       = "PodLauncher"
	PodResourceTypeStateKey   = "PodResourceType"
	PodTraceStateKey          = "PodTrace"
	NodeGroupStateKey         = "NodeGroup"
	PotentialVictimsKey       = "PotentialVictims"
	PDBsAllowedKey            = "PDBsAllowed"
	PDBItemsKey               = "PDBItems"
	PodsCanNotBePreemptedKey  = "PodsCanNotBePreempted"
	MatchedPDBIndexesKey      = "MatchedPDBIndexes"
	VictimCountOfDeployKey    = "VictimCountOfDeployKey"
	IndexOfPDBKey             = "IndexOfPDBKey"

	// Error Message
	NodePartitionTypeMissedErrorString = "failed to get NodePartitionType, supposed to be set in cycle state"
	PodLauncherMissedErrorString       = "failed to get PodLauncher, supposed to be set in cycle state"
	PodResourceTypeMissingErrorString  = "failed to get PodResourceType, supposed to be set in cycle state"
	PodTraceMissingErrorString         = "failed to get PodTrace, supposed to be set in cycle state"
	NodeGroupMissedErrorString         = "failed to get NodeGroup, supposed to be set in cycle state"

	UnsupportedError = "unsupported %s"
	MissedError      = "missed %s"

	PodPropertyKey = "PodProperty"

	AssignedNumasKey = "AssignedNumas"
)

// stateData contains single property to use in CycleState, use interface{} to do type casting
type stateData struct {
	data interface{}
}

func (s *stateData) Clone() StateData {
	return s
}

func SetPodResourceTypeState(resourceType podutil.PodResourceType, state *CycleState) error {
	if resourceType == podutil.GuaranteedPod || resourceType == podutil.BestEffortPod {
		data := &stateData{
			data: resourceType,
		}
		state.Write(PodResourceTypeStateKey, data)
		return nil
	}
	return podutil.PodResourceTypeUnsupportError
}

func SetPodLauncherState(podLauncher podutil.PodLauncher, state *CycleState) error {
	if podLauncher == podutil.Kubelet || podLauncher == podutil.NodeManager {
		data := &stateData{
			data: podLauncher,
		}
		state.Write(PodLauncherStateKey, data)
		return nil
	}
	return podutil.PodLauncherUnsupportError
}

func SetPodTrace(podTrace tracing.SchedulingTrace, state *CycleState) error {
	data := &stateData{
		data: podTrace,
	}
	state.Write(PodTraceStateKey, data)
	return nil
}

func SetNodeGroupKeyState(nodeGroup string, state *CycleState) error {
	data := &stateData{
		data: nodeGroup,
	}
	state.Write(NodeGroupStateKey, data)
	return nil
}

func SetPDBsAllowed(pdbsAllowed []int32, state *CycleState) error {
	data := &stateData{
		data: pdbsAllowed,
	}
	state.Write(PDBsAllowedKey, data)
	return nil
}

func GetPDBsAllowed(state *CycleState) ([]int32, error) {
	if data, err := state.Read(PDBsAllowedKey); err == nil {
		if s, ok := data.(*stateData); ok {
			if value, ok := s.data.([]int32); ok {
				return value, nil
			}
			return nil, fmt.Errorf(UnsupportedError, PDBsAllowedKey)
		}
	}
	return nil, fmt.Errorf(MissedError, PDBsAllowedKey)
}

func SetPDBItems(pdbItems []PDBItem, state *CycleState) error {
	data := &stateData{
		data: pdbItems,
	}
	state.Write(PDBItemsKey, data)
	return nil
}

func GetPDBItems(state *CycleState) ([]PDBItem, error) {
	if data, err := state.Read(PDBItemsKey); err == nil {
		if s, ok := data.(*stateData); ok {
			if value, ok := s.data.([]PDBItem); ok {
				return value, nil
			}
			return nil, fmt.Errorf(UnsupportedError, PDBItemsKey)
		}
	}
	return nil, fmt.Errorf(MissedError, PDBItemsKey)
}

func SetPodsCanNotBePreempted(podKey string, state *CycleState) error {
	pods, _ := GetPodsCanNotBePreempted(state)
	pods = append(pods, podKey)
	data := &stateData{
		data: pods,
	}
	state.Write(PodsCanNotBePreemptedKey, data)
	return nil
}

func GetPodsCanNotBePreempted(state *CycleState) ([]string, error) {
	if data, err := state.Read(PodsCanNotBePreemptedKey); err == nil {
		if s, ok := data.(*stateData); ok {
			if value, ok := s.data.([]string); ok {
				return value, nil
			}
			return nil, fmt.Errorf(UnsupportedError, PodsCanNotBePreemptedKey)
		}
	}
	return nil, fmt.Errorf(MissedError, PodsCanNotBePreemptedKey)
}

func SetMatchedPDBIndexes(victimKey string, indexes []int, state *CycleState) error {
	key := victimKey
	indexesMap, _ := GetMatchedPDBIndexes(state)
	if indexesMap == nil {
		indexesMap = map[string][]int{}
	}
	indexesMap[key] = indexes
	data := &stateData{
		data: indexesMap,
	}
	state.Write(MatchedPDBIndexesKey, data)
	return nil
}

func GetMatchedPDBIndexes(state *CycleState) (map[string][]int, error) {
	if data, err := state.Read(MatchedPDBIndexesKey); err == nil {
		if s, ok := data.(*stateData); ok {
			if value, ok := s.data.(map[string][]int); ok {
				return value, nil
			}
			return nil, fmt.Errorf(UnsupportedError, MatchedPDBIndexesKey)
		}
	}
	return nil, fmt.Errorf(MissedError, MatchedPDBIndexesKey)
}

func SetIndexOfPDB(pdbNameToIndexes map[string]int, state *CycleState) {
	data := &stateData{
		data: pdbNameToIndexes,
	}
	state.Write(IndexOfPDBKey, data)
}

func GetIndexOfPDB(state *CycleState) (map[string]int, error) {
	if data, err := state.Read(IndexOfPDBKey); err == nil {
		if s, ok := data.(*stateData); ok {
			if value, ok := s.data.(map[string]int); ok {
				return value, nil
			}
			return nil, fmt.Errorf(UnsupportedError, IndexOfPDBKey)
		}
	}
	return nil, fmt.Errorf(MissedError, IndexOfPDBKey)
}

var PodResourceTypeMissingError = fmt.Errorf(PodResourceTypeMissingErrorString)

func GetPodResourceType(state *CycleState) (podutil.PodResourceType, error) {
	if data, err := state.Read(PodResourceTypeStateKey); err == nil {
		if s, ok := data.(*stateData); ok {
			if value, ok := s.data.(podutil.PodResourceType); ok {
				return value, nil
			}
			return "", podutil.PodResourceTypeUnsupportError
		}
	}
	return "", PodResourceTypeMissingError
}

var PodLauncherMissedError = fmt.Errorf(PodLauncherMissedErrorString)

func GetPodLauncher(state *CycleState) (podutil.PodLauncher, error) {
	if data, err := state.Read(PodLauncherStateKey); err == nil {
		if s, ok := data.(*stateData); ok {
			if value, ok := s.data.(podutil.PodLauncher); ok {
				return value, nil
			}
			return "", podutil.PodLauncherUnsupportError
		}
	}
	return "", PodLauncherMissedError
}

var PodTraceMissingError = fmt.Errorf(PodTraceMissingErrorString)

func GetPodTrace(state *CycleState) (tracing.SchedulingTrace, error) {
	if data, err := state.Read(PodTraceStateKey); err == nil {
		if s, ok := data.(*stateData); ok {
			return s.data.(tracing.SchedulingTrace), nil
		}
	}
	return nil, PodTraceMissingError
}

func GetNodeGroupKey(state *CycleState) (string, error) {
	if data, err := state.Read(NodeGroupStateKey); err == nil {
		if s, ok := data.(*stateData); ok {
			return s.data.(string), nil
		}
	}
	return "", nil
}

type VictimState struct {
	MatchedPDBIndexes      []int
	MatchedPDBIndexesExist bool
}

func NewVictimState() *VictimState {
	return &VictimState{}
}

// SetPodProperty adds a unit property to the state.
func SetPodProperty(p *PodProperty, state *CycleState) {
	data := &stateData{
		data: p,
	}
	state.Write(PodPropertyKey, data)
}

// GetPodProperty returns the unit property from the state.
func GetPodProperty(state *CycleState) (*PodProperty, error) {
	if state == nil {
		return nil, fmt.Errorf("nil cycle state")
	}

	if data, err := state.Read(PodPropertyKey); err == nil {
		if s, ok := data.(*stateData); ok {
			return s.data.(*PodProperty), nil
		}
	}
	return nil, fmt.Errorf("unit property not found")
}

func GetAssignedNumas(state *CycleState) ([]int, error) {
	if state == nil {
		return nil, fmt.Errorf("nil cycle state")
	}
	if data, err := state.Read(AssignedNumasKey); err == nil {
		if s, ok := data.(*stateData); ok {
			return s.data.([]int), nil
		}
	}
	return nil, fmt.Errorf("assigned numas state not found")
}
