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

package interpodaffinity

import (
	"context"
	"fmt"
	"sync/atomic"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	schedutil "github.com/kubewharf/godel-scheduler/pkg/scheduler/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/parallelize"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	v1 "k8s.io/api/core/v1"
)

const (
	// ErrReasonExistingAntiAffinityRulesNotMatch is used for ExistingPodsAntiAffinityRulesNotMatch predicate error.
	ErrReasonExistingAntiAffinityRulesNotMatch = "node(s) didn't satisfy existing pods anti-affinity rules"
	// ErrReasonAffinityNotMatch is used for MatchInterPodAffinity predicate error.
	ErrReasonAffinityNotMatch = "node(s) didn't match pod affinity/anti-affinity"
	// ErrReasonAffinityRulesNotMatch is used for PodAffinityRulesNotMatch predicate error.
	ErrReasonAffinityRulesNotMatch = "node(s) didn't match pod affinity rules"
	// ErrReasonAntiAffinityRulesNotMatch is used for PodAntiAffinityRulesNotMatch predicate error.
	ErrReasonAntiAffinityRulesNotMatch = "node(s) didn't match pod anti-affinity rules"
)

// TODO(Huang-Wei): It might be possible to use "make(map[TopologyPair]*int64)" so that
// we can do atomic additions instead of using a global mutext, however we need to consider
// how to init each TopologyToMatchedTermCount.
type TopologyPair struct {
	Key   string
	Value string
}

type TopologyToMatchedTermCount map[TopologyPair]int64

func (m TopologyToMatchedTermCount) append(toAppend TopologyToMatchedTermCount) {
	for pair := range toAppend {
		m[pair] += toAppend[pair]
	}
}

func (m TopologyToMatchedTermCount) Clone() TopologyToMatchedTermCount {
	copy := make(TopologyToMatchedTermCount, len(m))
	copy.append(m)
	return copy
}

// UpdateWithAffinityTerms updates the topologyToMatchedTermCount map with the specified value
// for each affinity term if "targetPod" matches ALL terms.
func (m TopologyToMatchedTermCount) UpdateWithAffinityTerms(targetPod *v1.Pod, nodeLbaels map[string]string, affinityTerms []framework.AffinityTerm, value int64) {
	if PodMatchesAllAffinityTerms(targetPod, affinityTerms) {
		for _, t := range affinityTerms {
			if topologyValue, ok := nodeLbaels[t.TopologyKey]; ok {
				pair := TopologyPair{Key: t.TopologyKey, Value: topologyValue}
				m[pair] += value
				// value could be a negative value, hence we delete the entry if
				// the entry is down to zero.
				if m[pair] == 0 {
					delete(m, pair)
				}
			}
		}
	}
}

// UpdateWithAntiAffinityTerms updates the topologyToMatchedTermCount map with the specified value
// for each anti-affinity term matched the target pod.
func (m TopologyToMatchedTermCount) UpdateWithAntiAffinityTerms(targetPod *v1.Pod, nodeLabels map[string]string, antiAffinityTerms []framework.AffinityTerm, value int64) {
	// Check anti-affinity terms.
	for _, a := range antiAffinityTerms {
		if schedutil.PodMatchesTermsNamespaceAndSelector(targetPod, a.Namespaces, a.Selector) {
			if topologyValue, ok := nodeLabels[a.TopologyKey]; ok {
				pair := TopologyPair{Key: a.TopologyKey, Value: topologyValue}
				m[pair] += value
				// value could be a negative value, hence we delete the entry if
				// the entry is down to zero.
				if m[pair] == 0 {
					delete(m, pair)
				}
			}
		}
	}
}

// PreFilterState computed at PreFilter and used at Filter.
type PreFilterState struct {
	// A map of topology pairs to the number of existing pods that has anti-affinity terms that match the "pod".
	TopologyToMatchedExistingAntiAffinityTerms TopologyToMatchedTermCount
	// A map of topology pairs to the number of existing pods that match the affinity terms of the "pod".
	TopologyToMatchedAffinityTerms TopologyToMatchedTermCount
	// A map of topology pairs to the number of existing pods that match the anti-affinity terms of the "pod".
	TopologyToMatchedAntiAffinityTerms TopologyToMatchedTermCount
	// PodInfo of the incoming pod.
	PodInfo *framework.PodInfo
}

// Clone the prefilter state.
func (s *PreFilterState) Clone() framework.StateData {
	if s == nil {
		return nil
	}

	copy := PreFilterState{}
	copy.TopologyToMatchedAffinityTerms = s.TopologyToMatchedAffinityTerms.Clone()
	copy.TopologyToMatchedAntiAffinityTerms = s.TopologyToMatchedAntiAffinityTerms.Clone()
	copy.TopologyToMatchedExistingAntiAffinityTerms = s.TopologyToMatchedExistingAntiAffinityTerms.Clone()
	// No need to deep copy the podInfo because it shouldn't change.
	copy.PodInfo = s.PodInfo

	return &copy
}

// UpdateWithPod updates the preFilterState counters with the (anti)affinity matches for the given pod.
func (s *PreFilterState) UpdateWithPod(updatedPod *v1.Pod, nodeInfo framework.NodeInfo, multiplier int64) error {
	if s == nil {
		return nil
	}

	podLauncher, err := podutil.GetPodLauncher(updatedPod)
	if err != nil {
		return fmt.Errorf("error getting pod launcher: %v", err)
	}

	nodeLabels := nodeInfo.GetNodeLabels(podLauncher)

	// Update matching existing anti-affinity terms.
	// TODO(#91058): AddPod/RemovePod should pass a *framework.PodInfo type instead of *v1.Pod.
	updatedPodInfo := framework.NewPodInfo(updatedPod)
	s.TopologyToMatchedExistingAntiAffinityTerms.UpdateWithAntiAffinityTerms(s.PodInfo.Pod, nodeLabels, updatedPodInfo.RequiredAntiAffinityTerms, multiplier)

	// Update matching incoming pod (anti)affinity terms.
	s.TopologyToMatchedAffinityTerms.UpdateWithAffinityTerms(updatedPod, nodeLabels, s.PodInfo.RequiredAffinityTerms, multiplier)
	s.TopologyToMatchedAntiAffinityTerms.UpdateWithAntiAffinityTerms(updatedPod, nodeLabels, s.PodInfo.RequiredAntiAffinityTerms, multiplier)

	return nil
}

// PodMatchesAllAffinityTerms returns true IFF the given pod matches all the given terms.
func PodMatchesAllAffinityTerms(pod *v1.Pod, terms []framework.AffinityTerm) bool {
	if len(terms) == 0 {
		return false
	}
	for _, term := range terms {
		if !schedutil.PodMatchesTermsNamespaceAndSelector(pod, term.Namespaces, term.Selector) {
			return false
		}
	}
	return true
}

// GetTPMapMatchingExistingAntiAffinity calculates the following for each existing pod on each node:
// (1) Whether it has PodAntiAffinity
// (2) Whether any AffinityTerm matches the incoming pod
func GetTPMapMatchingExistingAntiAffinity(pod *v1.Pod, nodes []framework.NodeInfo) TopologyToMatchedTermCount {
	topoMaps := make([]TopologyToMatchedTermCount, len(nodes))
	index := int32(-1)
	processNode := func(i int) {
		nodeInfo := nodes[i]
		topoMap := make(TopologyToMatchedTermCount)
		for _, existingPod := range nodeInfo.GetPodsWithRequiredAntiAffinity() {
			topoMap.UpdateWithAntiAffinityTerms(pod, nodeInfo.GetNodeLabels(existingPod.PodLauncher), existingPod.RequiredAntiAffinityTerms, 1)
		}
		if len(topoMap) != 0 {
			topoMaps[atomic.AddInt32(&index, 1)] = topoMap
		}
	}
	parallelize.Until(context.Background(), len(nodes), processNode)

	result := make(TopologyToMatchedTermCount)
	for i := 0; i <= int(index); i++ {
		result.append(topoMaps[i])
	}

	return result
}

// GetTPMapMatchingIncomingAffinityAntiAffinity finds existing Pods that match affinity terms of the given "pod".
// It returns a topologyToMatchedTermCount that are checked later by the affinity
// predicate. With this topologyToMatchedTermCount available, the affinity predicate does not
// need to check all the pods in the cluster.
func GetTPMapMatchingIncomingAffinityAntiAffinity(podInfo *framework.PodInfo, allNodes []framework.NodeInfo) (TopologyToMatchedTermCount, TopologyToMatchedTermCount) {
	affinityCounts := make(TopologyToMatchedTermCount)
	antiAffinityCounts := make(TopologyToMatchedTermCount)
	if len(podInfo.RequiredAffinityTerms) == 0 && len(podInfo.RequiredAntiAffinityTerms) == 0 {
		return affinityCounts, antiAffinityCounts
	}

	affinityCountsList := make([]TopologyToMatchedTermCount, len(allNodes))
	antiAffinityCountsList := make([]TopologyToMatchedTermCount, len(allNodes))
	index := int32(-1)
	processNode := func(i int) {
		nodeInfo := allNodes[i]
		affinity := make(TopologyToMatchedTermCount)
		antiAffinity := make(TopologyToMatchedTermCount)
		for _, existingPod := range nodeInfo.GetPods() {
			// Check affinity terms.
			affinity.UpdateWithAffinityTerms(existingPod.Pod, nodeInfo.GetNodeLabels(existingPod.PodLauncher), podInfo.RequiredAffinityTerms, 1)

			// Check anti-affinity terms.
			antiAffinity.UpdateWithAntiAffinityTerms(existingPod.Pod, nodeInfo.GetNodeLabels(existingPod.PodLauncher), podInfo.RequiredAntiAffinityTerms, 1)
		}

		if len(affinity) > 0 || len(antiAffinity) > 0 {
			k := atomic.AddInt32(&index, 1)
			affinityCountsList[k] = affinity
			antiAffinityCountsList[k] = antiAffinity
		}
	}
	parallelize.Until(context.Background(), len(allNodes), processNode)

	for i := 0; i <= int(index); i++ {
		affinityCounts.append(affinityCountsList[i])
		antiAffinityCounts.append(antiAffinityCountsList[i])
	}

	return affinityCounts, antiAffinityCounts
}

// Checks if scheduling the pod onto this node would break any anti-affinity
// terms indicated by the existing pods.
func SatisfyExistingPodsAntiAffinity(state *PreFilterState, nodeInfo framework.NodeInfo, podLauncher podutil.PodLauncher) bool {
	if len(state.TopologyToMatchedExistingAntiAffinityTerms) > 0 {
		// Iterate over topology pairs to get any of the pods being affected by
		// the scheduled pod anti-affinity terms
		for topologyKey, topologyValue := range nodeInfo.GetNodeLabels(podLauncher) {
			tp := TopologyPair{Key: topologyKey, Value: topologyValue}
			if state.TopologyToMatchedExistingAntiAffinityTerms[tp] > 0 {
				return false
			}
		}
	}
	return true
}

// Checks if the node satisfies the incoming pod's affinity rules.
func SatisfyPodAffinity(state *PreFilterState, nodeInfo framework.NodeInfo, podLauncher podutil.PodLauncher) bool {
	podsExist := true
	for _, term := range state.PodInfo.RequiredAffinityTerms {
		if topologyValue, ok := nodeInfo.GetNodeLabels(podLauncher)[term.TopologyKey]; ok {
			tp := TopologyPair{Key: term.TopologyKey, Value: topologyValue}
			if state.TopologyToMatchedAffinityTerms[tp] <= 0 {
				podsExist = false
			}
		} else {
			// All topology labels must exist on the node.
			return false
		}
	}

	if !podsExist {
		// This pod may be the first pod in a series that have affinity to themselves. In order
		// to not leave such pods in pending state forever, we check that if no other pod
		// in the cluster matches the namespace and selector of this pod, the pod matches
		// its own terms, and the node has all the requested topologies, then we allow the pod
		// to pass the affinity check.
		podInfo := state.PodInfo
		if len(state.TopologyToMatchedAffinityTerms) == 0 && PodMatchesAllAffinityTerms(podInfo.Pod, podInfo.RequiredAffinityTerms) {
			return true
		}
		return false
	}
	return true
}

// Checks if the node satisifies the incoming pod's anti-affinity rules.
func SatisfyPodAntiAffinity(state *PreFilterState, nodeInfo framework.NodeInfo, podLauncher podutil.PodLauncher) bool {
	if len(state.TopologyToMatchedAntiAffinityTerms) > 0 {
		for _, term := range state.PodInfo.RequiredAntiAffinityTerms {
			if topologyValue, ok := nodeInfo.GetNodeLabels(podLauncher)[term.TopologyKey]; ok {
				tp := TopologyPair{Key: term.TopologyKey, Value: topologyValue}
				if state.TopologyToMatchedAntiAffinityTerms[tp] > 0 {
					return false
				}
			}
		}
	}
	return true
}
