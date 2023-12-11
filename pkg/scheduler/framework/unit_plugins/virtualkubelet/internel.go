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

package virtualkubelet

import (
	v1 "k8s.io/api/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	VirtualKubeletKey   = "type"
	VirtualKubeletValue = "virtual-kubelet"
)

func isVirtualKubeletPod(pod *v1.Pod) bool {
	if len(pod.Spec.NodeSelector) == 0 {
		return false
	}
	if value, ok := pod.Spec.NodeSelector[VirtualKubeletKey]; ok && value == VirtualKubeletValue {
		return true
	}
	return false
}

func isVirtualKubeletNode(nodeInfo framework.NodeInfo, podLauncher podutil.PodLauncher) bool {
	labels := nodeInfo.GetNodeLabels(podLauncher)
	if len(labels) == 0 {
		return false
	}
	if value, ok := labels[VirtualKubeletKey]; ok && value == VirtualKubeletValue {
		return true
	}
	return false
}
