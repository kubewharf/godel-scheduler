/*
Copyright 2019 The Kubernetes Authors.

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

package node

import v1 "k8s.io/api/core/v1"

const (
	// GodelSchedulerNodeAnnotationKey is the annotation key in both Node and CNR api objects,
	// value is the godel scheduler whose node partition contains this node
	GodelSchedulerNodeAnnotationKey = "godel.bytedance.com/scheduler-name"
)

func NodeOfThisScheduler(annotations map[string]string, schedulerName string) bool {
	name, ok := annotations[GodelSchedulerNodeAnnotationKey]
	if ok && name == schedulerName {
		return true
	}
	return false
}

// Scheduable checks if the node is schedulable
// a node is schedulable only if its condition NodeReady == True
func Scheduable(node *v1.Node) bool {
	if node == nil || node.Spec.Unschedulable {
		return false
	}

	for _, v := range node.Status.Conditions {
		if v.Type == v1.NodeReady && v.Status == v1.ConditionTrue {
			return true
		}
	}

	return false
}

// Tainted checks if the node has taints
func Tainted(node *v1.Node) bool {
	if node == nil || len(node.Spec.Taints) == 0 {
		return false
	}
	return true
}
