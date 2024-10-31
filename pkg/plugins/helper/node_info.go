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

package helper

import (
	"fmt"

	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

const (
	AllNodeInfoKey = "AllNodeInfo"
)

type AllNodeInfos struct {
	NodeInfos []framework.NodeInfo
}

func (f AllNodeInfos) Clone() framework.StateData {
	clonedNodeInfos := make([]framework.NodeInfo, len(f.NodeInfos))

	copy(clonedNodeInfos, f.NodeInfos)

	return AllNodeInfos{
		NodeInfos: clonedNodeInfos,
	}
}

func WriteAllNodeInfos(cycleState *framework.CycleState, frameworkHandle handle.BinderFrameworkHandle) error {
	nodeInfos := frameworkHandle.ListNodeInfos()

	allNodeInfoData := &AllNodeInfos{
		NodeInfos: nodeInfos,
	}
	cycleState.Write(AllNodeInfoKey, allNodeInfoData)

	return nil
}

func ReadAllNodeInfos(cycleState *framework.CycleState) ([]framework.NodeInfo, error) {
	nodeInfoData, err := cycleState.Read(AllNodeInfoKey)
	if err != nil {
		return nil, err
	}

	allNodeInfos, ok := nodeInfoData.(*AllNodeInfos)
	if !ok {
		return nil, fmt.Errorf("%+v convert to helper.AllNodeInfos error", nodeInfoData)
	}
	return allNodeInfos.NodeInfos, nil
}
