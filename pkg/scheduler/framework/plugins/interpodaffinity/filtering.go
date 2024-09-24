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

package interpodaffinity

import (
	"context"
	"fmt"
	"sync/atomic"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/podlauncher"
	schedutil "github.com/kubewharf/godel-scheduler/pkg/scheduler/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/parallelize"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	v1 "k8s.io/api/core/v1"
)

const (
	// preFilterStateKey is the key in CycleState to InterPodAffinity pre-computed data for Filtering.
	// Using the name of the plugin will likely help us avoid collisions with other plugins.
	preFilterStateKey = "PreFilter" + Name

	// ErrReasonExistingAntiAffinityRulesNotMatch is used for ExistingPodsAntiAffinityRulesNotMatch predicate error.
	ErrReasonExistingAntiAffinityRulesNotMatch = "node(s) didn't satisfy existing pods anti-affinity rules"
	// ErrReasonAffinityNotMatch is used for MatchInterPodAffinity predicate error.
	ErrReasonAffinityNotMatch = "node(s) didn't match pod affinity/anti-affinity"
	// ErrReasonAffinityRulesNotMatch is used for PodAffinityRulesNotMatch predicate error.
	ErrReasonAffinityRulesNotMatch = "node(s) didn't match pod affinity rules"
	// ErrReasonAntiAffinityRulesNotMatch is used for PodAntiAffinityRulesNotMatch predicate error.
	ErrReasonAntiAffinityRulesNotMatch = "node(s) didn't match pod anti-affinity rules"
)

// PreFilterState computed at PreFilter and used at Filter.
type PreFilterState struct {
	// A map of topology pairs to the number of existing pods that has anti-affinity terms that match the "pod".
	TopologyToMatchedExistingAntiAffinityTerms topologyToMatchedTermCount
	// A map of topology pairs to the number of existing pods that match the affinity terms of the "pod".
	TopologyToMatchedAffinityTerms topologyToMatchedTermCount
	// A map of topology pairs to the number of existing pods that match the anti-affinity terms of the "pod".
	TopologyToMatchedAntiAffinityTerms topologyToMatchedTermCount
	// PodInfo of the incoming pod.
	PodInfo *framework.PodInfo
}

// Clone the prefilter state.
func (s *PreFilterState) Clone() framework.StateData {
	if s == nil {
		return nil
	}

	copy := PreFilterState{}
	copy.TopologyToMatchedAffinityTerms = s.TopologyToMatchedAffinityTerms.clone()
	copy.TopologyToMatchedAntiAffinityTerms = s.TopologyToMatchedAntiAffinityTerms.clone()
	copy.TopologyToMatchedExistingAntiAffinityTerms = s.TopologyToMatchedExistingAntiAffinityTerms.clone()
	// No need to deep copy the podInfo because it shouldn't change.
	copy.PodInfo = s.PodInfo

	return &copy
}

// updateWithPod updates the preFilterState counters with the (anti)affinity matches for the given pod.
func (s *PreFilterState) updateWithPod(updatedPod *v1.Pod, nodeInfo framework.NodeInfo, multiplier int64) error {
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
	s.TopologyToMatchedExistingAntiAffinityTerms.updateWithAntiAffinityTerms(s.PodInfo.Pod, nodeLabels, updatedPodInfo.RequiredAntiAffinityTerms, multiplier)

	// Update matching incoming pod (anti)affinity terms.
	s.TopologyToMatchedAffinityTerms.updateWithAffinityTerms(updatedPod, nodeLabels, s.PodInfo.RequiredAffinityTerms, multiplier)
	s.TopologyToMatchedAntiAffinityTerms.updateWithAntiAffinityTerms(updatedPod, nodeLabels, s.PodInfo.RequiredAntiAffinityTerms, multiplier)

	return nil
}

// TODO(Huang-Wei): It might be possible to use "make(map[topologyPair]*int64)" so that
// we can do atomic additions instead of using a global mutext, however we need to consider
// how to init each topologyToMatchedTermCount.
type topologyPair struct {
	key   string
	value string
}
type topologyToMatchedTermCount map[topologyPair]int64

func (m topologyToMatchedTermCount) append(toAppend topologyToMatchedTermCount) {
	for pair := range toAppend {
		m[pair] += toAppend[pair]
	}
}

func (m topologyToMatchedTermCount) clone() topologyToMatchedTermCount {
	copy := make(topologyToMatchedTermCount, len(m))
	copy.append(m)
	return copy
}

// updateWithAffinityTerms updates the topologyToMatchedTermCount map with the specified value
// for each affinity term if "targetPod" matches ALL terms.
func (m topologyToMatchedTermCount) updateWithAffinityTerms(targetPod *v1.Pod, nodeLbaels map[string]string, affinityTerms []framework.AffinityTerm, value int64) {
	if podMatchesAllAffinityTerms(targetPod, affinityTerms) {
		for _, t := range affinityTerms {
			if topologyValue, ok := nodeLbaels[t.TopologyKey]; ok {
				pair := topologyPair{key: t.TopologyKey, value: topologyValue}
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

// updateWithAntiAffinityTerms updates the topologyToMatchedTermCount map with the specified value
// for each anti-affinity term matched the target pod.
func (m topologyToMatchedTermCount) updateWithAntiAffinityTerms(targetPod *v1.Pod, nodeLabels map[string]string, antiAffinityTerms []framework.AffinityTerm, value int64) {
	// Check anti-affinity terms.
	for _, a := range antiAffinityTerms {
		if schedutil.PodMatchesTermsNamespaceAndSelector(targetPod, a.Namespaces, a.Selector) {
			if topologyValue, ok := nodeLabels[a.TopologyKey]; ok {
				pair := topologyPair{key: a.TopologyKey, value: topologyValue}
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

// podMatchesAllAffinityTerms returns true IFF the given pod matches all the given terms.
func podMatchesAllAffinityTerms(pod *v1.Pod, terms []framework.AffinityTerm) bool {
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
func GetTPMapMatchingExistingAntiAffinity(pod *v1.Pod, nodes []framework.NodeInfo, podLauncher podutil.PodLauncher) topologyToMatchedTermCount {
	topoMaps := make([]topologyToMatchedTermCount, len(nodes))
	index := int32(-1)
	processNode := func(i int) {
		nodeInfo := nodes[i]
		topoMap := make(topologyToMatchedTermCount)
		for _, existingPod := range nodeInfo.GetPodsWithRequiredAntiAffinity() {
			topoMap.updateWithAntiAffinityTerms(pod, nodeInfo.GetNodeLabels(podLauncher), existingPod.RequiredAntiAffinityTerms, 1)
		}
		if len(topoMap) != 0 {
			topoMaps[atomic.AddInt32(&index, 1)] = topoMap
		}
	}
	parallelize.Until(context.Background(), len(nodes), processNode)

	result := make(topologyToMatchedTermCount)
	for i := 0; i <= int(index); i++ {
		result.append(topoMaps[i])
	}

	return result
}

// GetTPMapMatchingIncomingAffinityAntiAffinity finds existing Pods that match affinity terms of the given "pod".
// It returns a topologyToMatchedTermCount that are checked later by the affinity
// predicate. With this topologyToMatchedTermCount available, the affinity predicate does not
// need to check all the pods in the cluster.
func GetTPMapMatchingIncomingAffinityAntiAffinity(podInfo *framework.PodInfo, allNodes []framework.NodeInfo, podLauncher podutil.PodLauncher) (topologyToMatchedTermCount, topologyToMatchedTermCount) {
	affinityCounts := make(topologyToMatchedTermCount)
	antiAffinityCounts := make(topologyToMatchedTermCount)
	if len(podInfo.RequiredAffinityTerms) == 0 && len(podInfo.RequiredAntiAffinityTerms) == 0 {
		return affinityCounts, antiAffinityCounts
	}

	affinityCountsList := make([]topologyToMatchedTermCount, len(allNodes))
	antiAffinityCountsList := make([]topologyToMatchedTermCount, len(allNodes))
	index := int32(-1)
	processNode := func(i int) {
		nodeInfo := allNodes[i]
		affinity := make(topologyToMatchedTermCount)
		antiAffinity := make(topologyToMatchedTermCount)
		for _, existingPod := range nodeInfo.GetPods() {
			// Check affinity terms.
			affinity.updateWithAffinityTerms(existingPod.Pod, nodeInfo.GetNodeLabels(podLauncher), podInfo.RequiredAffinityTerms, 1)

			// Check anti-affinity terms.
			antiAffinity.updateWithAntiAffinityTerms(existingPod.Pod, nodeInfo.GetNodeLabels(podLauncher), podInfo.RequiredAntiAffinityTerms, 1)
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

// PreFilter invoked at the prefilter extension point.
func (pl *InterPodAffinity) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod) *framework.Status {
	podLauncher, err := podutil.GetPodLauncher(pod)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	var allNodes []framework.NodeInfo
	var nodesWithRequiredAntiAffinityPods []framework.NodeInfo
	allNodes = pl.sharedLister.NodeInfos().List()
	nodesWithRequiredAntiAffinityPods = pl.sharedLister.NodeInfos().HavePodsWithRequiredAntiAffinityList()

	podInfo := framework.NewPodInfo(pod)
	if podInfo.ParseError != nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("parsing pod: %+v", podInfo.ParseError))
	}

	// existingPodAntiAffinityMap will be used later for efficient check on existing pods' anti-affinity
	existingPodAntiAffinityMap := GetTPMapMatchingExistingAntiAffinity(pod, nodesWithRequiredAntiAffinityPods, podLauncher)

	// incomingPodAffinityMap will be used later for efficient check on incoming pod's affinity
	// incomingPodAntiAffinityMap will be used later for efficient check on incoming pod's anti-affinity
	incomingPodAffinityMap, incomingPodAntiAffinityMap := GetTPMapMatchingIncomingAffinityAntiAffinity(podInfo, allNodes, podLauncher)

	s := &PreFilterState{
		TopologyToMatchedAffinityTerms:             incomingPodAffinityMap,
		TopologyToMatchedAntiAffinityTerms:         incomingPodAntiAffinityMap,
		TopologyToMatchedExistingAntiAffinityTerms: existingPodAntiAffinityMap,
		PodInfo: podInfo,
	}

	cycleState.Write(preFilterStateKey, s)
	return nil
}

// PreFilterExtensions returns prefilter extensions, pod add and remove.
func (pl *InterPodAffinity) PreFilterExtensions() framework.PreFilterExtensions {
	return pl
}

// AddPod from pre-computed data in cycleState.
func (pl *InterPodAffinity) AddPod(ctx context.Context, cycleState *framework.CycleState, podToSchedule *v1.Pod, podToAdd *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	state, err := getPreFilterState(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	state.updateWithPod(podToAdd, nodeInfo, 1)
	return nil
}

// RemovePod from pre-computed data in cycleState.
func (pl *InterPodAffinity) RemovePod(ctx context.Context, cycleState *framework.CycleState, podToSchedule *v1.Pod, podToRemove *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	state, err := getPreFilterState(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	state.updateWithPod(podToRemove, nodeInfo, -1)
	return nil
}

func getPreFilterState(cycleState *framework.CycleState) (*PreFilterState, error) {
	c, err := cycleState.Read(preFilterStateKey)
	if err != nil {
		// preFilterState doesn't exist, likely PreFilter wasn't invoked.
		return nil, fmt.Errorf("error reading %q from cycleState: %v", preFilterStateKey, err)
	}

	s, ok := c.(*PreFilterState)
	if !ok {
		return nil, fmt.Errorf("%+v  convert to interpodaffinity.state error", c)
	}
	return s, nil
}

// Checks if scheduling the pod onto this node would break any anti-affinity
// terms indicated by the existing pods.
func SatisfyExistingPodsAntiAffinity(state *PreFilterState, nodeInfo framework.NodeInfo, podLauncher podutil.PodLauncher) bool {
	if len(state.TopologyToMatchedExistingAntiAffinityTerms) > 0 {
		// Iterate over topology pairs to get any of the pods being affected by
		// the scheduled pod anti-affinity terms
		for topologyKey, topologyValue := range nodeInfo.GetNodeLabels(podLauncher) {
			tp := topologyPair{key: topologyKey, value: topologyValue}
			if state.TopologyToMatchedExistingAntiAffinityTerms[tp] > 0 {
				return false
			}
		}
	}
	return true
}

// Checks if the node satisifies the incoming pod's anti-affinity rules.
func SatisfyPodAntiAffinity(state *PreFilterState, nodeInfo framework.NodeInfo, podLauncher podutil.PodLauncher) bool {
	if len(state.TopologyToMatchedAntiAffinityTerms) > 0 {
		for _, term := range state.PodInfo.RequiredAntiAffinityTerms {
			if topologyValue, ok := nodeInfo.GetNodeLabels(podLauncher)[term.TopologyKey]; ok {
				tp := topologyPair{key: term.TopologyKey, value: topologyValue}
				if state.TopologyToMatchedAntiAffinityTerms[tp] > 0 {
					return false
				}
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
			tp := topologyPair{key: term.TopologyKey, value: topologyValue}
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
		if len(state.TopologyToMatchedAffinityTerms) == 0 && podMatchesAllAffinityTerms(podInfo.Pod, podInfo.RequiredAffinityTerms) {
			return true
		}
		return false
	}
	return true
}

// Filter invoked at the filter extension point.
// It checks if a pod can be scheduled on the specified node with pod affinity/anti-affinity configuration.
func (pl *InterPodAffinity) Filter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	podLauncher, status := podlauncher.NodeFits(cycleState, pod, nodeInfo)
	if status != nil {
		return status
	}

	state, err := getPreFilterState(cycleState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	if !SatisfyPodAffinity(state, nodeInfo, podLauncher) {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonAffinityNotMatch, ErrReasonAffinityRulesNotMatch)
	}

	if !SatisfyPodAntiAffinity(state, nodeInfo, podLauncher) {
		return framework.NewStatus(framework.Unschedulable, ErrReasonAffinityNotMatch, ErrReasonAntiAffinityRulesNotMatch)
	}

	if !SatisfyExistingPodsAntiAffinity(state, nodeInfo, podLauncher) {
		return framework.NewStatus(framework.Unschedulable, ErrReasonAffinityNotMatch, ErrReasonExistingAntiAffinityRulesNotMatch)
	}

	return nil
}
