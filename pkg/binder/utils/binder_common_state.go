/*
Copyright 2024 The Godel Scheduler Authors.

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
	"fmt"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	BinderCommonStateKey = "BinderCommonState"
)

type CommonState struct {
	VictimsGroupByNode map[string]map[types.UID]*v1.Pod
}

func (s CommonState) Clone() framework.StateData {
	clonedVictimsGroupByNode := make(map[string]map[types.UID]*v1.Pod)

	for nodeName, victimsMap := range s.VictimsGroupByNode {
		clonedVictimsMap := make(map[types.UID]*v1.Pod)
		for uid, pod := range victimsMap {
			clonedVictimsMap[uid] = pod.DeepCopy()
		}
		clonedVictimsGroupByNode[nodeName] = clonedVictimsMap
	}

	return CommonState{
		VictimsGroupByNode: clonedVictimsGroupByNode,
	}
}

func WriteCommonState(cycleState *framework.CycleState, victimsGroupByNode map[string]map[types.UID]*v1.Pod) error {
	commonState := &CommonState{
		VictimsGroupByNode: victimsGroupByNode,
	}
	cycleState.Write(BinderCommonStateKey, commonState)

	return nil
}

func ReadCommonState(cycleState *framework.CycleState) (*CommonState, error) {
	if cycleState == nil {
		return nil, nil
	}
	commonStateData, err := cycleState.Read(BinderCommonStateKey)
	if err != nil {
		return nil, err
	}

	commonState, ok := commonStateData.(*CommonState)
	if !ok {
		return nil, fmt.Errorf("%+v convert to helper.AllNodeInfos error", commonStateData)
	}
	return commonState, nil
}
