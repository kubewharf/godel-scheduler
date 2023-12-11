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

package util

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
)

// CheckNodeAffinity looks at the PV node affinity, and checks if the node has the same corresponding labels
// This ensures that we don't mount a volume that doesn't belong to this node
func CheckNodeAffinity(pv *v1.PersistentVolume, nodeLabels map[string]string) error {
	return checkVolumeNodeAffinity(pv, nodeLabels)
}

func checkVolumeNodeAffinity(pv *v1.PersistentVolume, nodeLabels map[string]string) error {
	if pv.Spec.NodeAffinity == nil {
		return nil
	}

	if pv.Spec.NodeAffinity.Required != nil {
		terms := pv.Spec.NodeAffinity.Required.NodeSelectorTerms
		message := fmt.Sprintf("Started to look for match for Required node selector terms %+v", terms)
		klog.V(6).InfoS(message)
		if !helper.MatchNodeSelectorTerms(terms, labels.Set(nodeLabels), nil) {
			return fmt.Errorf("No matching NodeSelectorTerms")
		}
	}

	return nil
}
