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

package daemonset

import (
	v1 "k8s.io/api/core/v1"

	"github.com/kubewharf/godel-scheduler/pkg/util"
)

func parseDaemonSetAffinityTerm(pod *v1.Pod) []string {
	affinity := pod.Spec.Affinity
	if affinity != nil && affinity.NodeAffinity != nil && affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		terms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
		for _, term := range terms {
			for _, field := range term.MatchFields {
				// Use `metadata.name` here.
				if field.Key == util.ObjectNameField && field.Operator == v1.NodeSelectorOpIn {
					return field.Values
				}
			}
		}
	}
	return nil
}
