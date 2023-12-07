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

package nodeaffinity

import (
	"errors"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubewharf/godel-scheduler/pkg/util"
)

var (
	ErrNilPreFilterState = errors.New("nil preFilterState in NodeAffinity")

	ErrNodeSelectorNotMatching = errors.New("node(s) didn't match node selector")

	ErrNodeAffinityNotMatching = errors.New("node(s) didn't match node affinity")
)

// podMatchesNodeSelectorAndAffinityTerms checks whether the pod is schedulable onto nodes according to
// the requirements in both NodeAffinity and nodeSelector.
func podMatchesNodeSelectorAndAffinityTerms(pod *v1.Pod, nodeAffinityRelated *preFilterState, nodeLabels map[string]string, nodeName string) error {
	// Check if node.Labels match pod.Spec.NodeSelector.
	if len(pod.Spec.NodeSelector) > 0 && nodeAffinityRelated.nodeLabelSelector != nil {
		if !nodeAffinityRelated.nodeLabelSelector.Matches(labels.Set(nodeLabels)) {
			return ErrNodeSelectorNotMatching
		}
	}

	// 1. nil NodeSelector matches all nodes (i.e. does not filter out any nodes)
	// 2. nil []NodeSelectorTerm (equivalent to non-nil empty NodeSelector) matches no nodes
	// 3. zero-length non-nil []NodeSelectorTerm matches no nodes also, just for simplicity
	// 4. nil []NodeSelectorRequirement (equivalent to non-nil empty NodeSelectorTerm) matches no nodes
	// 5. zero-length non-nil []NodeSelectorRequirement matches no nodes also, just for simplicity
	// 6. non-nil empty NodeSelectorRequirement is not allowed
	nodeAffinityMatches := true
	affinity := pod.Spec.Affinity
	if affinity != nil && affinity.NodeAffinity != nil {
		nodeAffinity := affinity.NodeAffinity
		// if no required NodeAffinity requirements, will do no-op, means select all nodes.
		// TODO: Replace next line with subsequent commented-out line when implement RequiredDuringSchedulingRequiredDuringExecution.
		if nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
			// if nodeAffinity.RequiredDuringSchedulingRequiredDuringExecution == nil && nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
			return nil
		}

		// Match node selector for requiredDuringSchedulingRequiredDuringExecution.
		// TODO: Uncomment this block when implement RequiredDuringSchedulingRequiredDuringExecution.
		// if nodeAffinity.RequiredDuringSchedulingRequiredDuringExecution != nil {
		// 	nodeSelectorTerms := nodeAffinity.RequiredDuringSchedulingRequiredDuringExecution.NodeSelectorTerms
		// 	klog.V(10).Infof("Match for RequiredDuringSchedulingRequiredDuringExecution node selector terms %+v", nodeSelectorTerms)
		// 	nodeAffinityMatches = nodeMatchesNodeSelectorTerms(node, nodeSelectorTerms)
		// }

		// Match node selector for requiredDuringSchedulingIgnoredDuringExecution.
		if nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
			nodeAffinityMatches = nodeAffinityMatches && nodeMatchesNodeSelectorTerms(nodeName, nodeLabels, nodeAffinityRelated.requiredNodeAffinityTermSelectors)
		}

	}

	if nodeAffinityMatches {
		return nil
	}
	return ErrNodeAffinityNotMatching
}

// nodeMatchesNodeSelectorTerms checks if a node's labels satisfy a list of node selector terms,
// terms are ORed, and an empty list of terms will match nothing.
func nodeMatchesNodeSelectorTerms(nodeName string, nodeLabels labels.Set, selectors []*requiredNodeAffinityTermSelector) bool {
	nodeFields := fields.Set{util.ObjectNameField: nodeName}

	for _, s := range selectors {
		if s.LabelSelector != nil {
			if !s.LabelSelector.Matches(nodeLabels) {
				continue
			}
		}
		if s.FieldSelector != nil {
			if !s.FieldSelector.Matches(nodeFields) {
				continue
			}
		}

		return true
	}

	return false
}
