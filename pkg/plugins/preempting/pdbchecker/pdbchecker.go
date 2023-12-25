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

package pdbchecker

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

func CheckPodDisruptionBudgetViolation(victim *v1.Pod, pdbsAllowed []int32, pdbItems []framework.PDBItem) (bool, []int) {
	pdbsAllowedCopy := make([]int32, len(pdbsAllowed))
	copy(pdbsAllowedCopy, pdbsAllowed)
	var indexes []int
	var violating bool
	// A pod with no labels will not match any PDB. So, no need to check.
	if len(victim.Labels) != 0 {
		for i, pdbItem := range pdbItems {
			if !PDBMatched(victim, pdbItem) {
				continue
			}
			// Only decrement the matched pdb when it's not in its <DisruptedPods>;
			// otherwise we may over-decrement the budget number.
			indexes = append(indexes, i)
			pdbsAllowedCopy[i]--
			// We have found a matching PDB.
			if pdbsAllowedCopy[i] < 0 {
				violating = true
			}
		}
	}
	return violating, indexes
}

func PDBMatched(victim *v1.Pod, pdbItem framework.PDBItem) bool {
	pdb := pdbItem.GetPDB()
	if pdb.Namespace != victim.Namespace {
		return false
	}
	selector := pdbItem.GetPDBSelector()
	// A PDB with a nil or empty selector matches nothing.
	if selector == nil || selector.Empty() || !selector.Matches(labels.Set(victim.Labels)) {
		return false
	}

	// Existing in DisruptedPods means it has been processed in API server,
	// we don't treat it as a violating case.
	if _, exist := pdb.Status.DisruptedPods[victim.Name]; exist {
		return false
	}

	return true
}
