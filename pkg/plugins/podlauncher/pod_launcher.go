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

package podlauncher

import (
	"fmt"

	v1 "k8s.io/api/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// ErrReasonTemplate returned when node doesn't have the requested launcher in it.
const ErrReasonTemplate = "launcher %v doesn't exist on this node"

// canPodPlaceOnNode returns whether the pod can be placed on the node, according to pod type
func canPodPlaceOnNode(podLauncher podutil.PodLauncher, nodeInfo framework.NodeInfo) bool {
	switch podLauncher {
	case podutil.Kubelet:
		return nodeInfo.GetNode() != nil
	case podutil.NodeManager:
		return nodeInfo.GetNMNode() != nil
	}
	return false
}

func NodeFits(state *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) (podutil.PodLauncher, *framework.Status) {
	podLauncher, _ := podutil.GetPodLauncher(pod)
	if podLauncher == "" {
		errReason := fmt.Sprintf("pod launcher is empty for pod %v/%v", pod.Namespace, pod.Name)
		return podLauncher, framework.NewStatus(framework.Error, errReason)
	}
	if canPodPlaceOnNode(podLauncher, nodeInfo) {
		return podLauncher, nil
	}
	errReason := fmt.Sprintf(ErrReasonTemplate, podLauncher)
	return podLauncher, framework.NewStatus(framework.UnschedulableAndUnresolvable, errReason)
}
