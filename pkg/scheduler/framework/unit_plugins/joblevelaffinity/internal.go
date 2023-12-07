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

package joblevelaffinity

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/util/sets"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util/parallelize"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// ------------------------------------------------------------------------------------------

var defaultSortRule = framework.SortRule{
	Resource:  framework.CPUResource,
	Dimension: framework.Capacity,
	Order:     framework.AscendingOrder,
}

func getSortRules(unit framework.ScheduleUnit) ([]framework.SortRule, error) {
	sortRules, err := unit.GetSortRulesForAffinity()
	if err != nil {
		return nil, err
	}

	// If there is no sort rules, we add default rule.
	if len(sortRules) == 0 {
		sortRules = []framework.SortRule{defaultSortRule}
	}
	return sortRules, nil
}

// ------------------------------------------------------------------------------------------

type TopologyError struct {
	message string
}

func (err *TopologyError) Error() string {
	return err.message
}

func newTopologyError(key string) *TopologyError {
	return &TopologyError{
		message: fmt.Sprintf("topology key %v not exist", key),
	}
}

// ------------------------------------------------------------------------------------------

// nodeGroupAffinityTerms contains node group affinity requirements defined in PodGroup.Spec
type nodeGroupAffinityTerms struct {
	terms []framework.UnitAffinityTerm
	// termsVal is in the format of "terms[0];terms[1];terms[2];"
	termsVal string
}

// getAffinitySpecs return nodeGroupAffinitySpecs according to labels, and all terms in nodeGroupAffinityTerms must be found in labels.
func (terms *nodeGroupAffinityTerms) getAffinitySpecs(labels map[string]string) (*nodeGroupAffinitySpecs, error) {
	specs := make([]nodeGroupAffinitySpec, 0, len(terms.terms))
	for i := range terms.terms {
		value, ok := labels[terms.terms[i].TopologyKey]
		if !ok {
			return nil, newTopologyError(terms.terms[i].TopologyKey)
		}
		specs = append(specs, nodeGroupAffinitySpec{terms.terms[i].TopologyKey, value})
	}
	sort.SliceStable(specs, func(i, j int) bool {
		return specs[i].topologyKey < specs[j].topologyKey
	})

	var builder strings.Builder
	for i := range specs {
		builder.WriteString(specs[i].topologyKey)
		builder.WriteByte(':')
		builder.WriteString(specs[i].topologyValue)
		builder.WriteByte(';')
	}

	return &nodeGroupAffinitySpecs{
		specs: specs,
		key:   builder.String(),
	}, nil
}

func (terms *nodeGroupAffinityTerms) String() string {
	return terms.termsVal
}

func newNodeGroupAffinityTerms(terms []framework.UnitAffinityTerm) *nodeGroupAffinityTerms {
	termsVal := ""
	for _, term := range terms {
		termsVal += term.TopologyKey + ";"
	}
	return &nodeGroupAffinityTerms{
		terms:    terms,
		termsVal: termsVal,
	}
}

// ------------------------------------------------------------------------------------------

// nodeGroupAffinitySpec contains both topology key and topology value, used in NodeGroupAffinity
type nodeGroupAffinitySpec struct {
	// spec is the label key used for affinity, such as miniPod or bigPod
	topologyKey string
	// status is the label value used if exists, Otherwise status should be nil
	topologyValue string
}

// nodeGroupAffinitySpecs is a collection of all topology key and topology value pairs that can define a specified node group.
// Each NodeGroup must have a distinct nodeGroupAffinitySpecs.
type nodeGroupAffinitySpecs struct {
	specs []nodeGroupAffinitySpec
	// key is the joint string of all specs, in the format of "topologyKey1:topologyVal1;topologyKey2:topologyVal2;topologyKey3:topologyVal3;"
	// For each node group, key is distinct.
	key string
}

// satisfy return true if all topologyKey and topologyVal are defined in the labels, else return false.
func (specs *nodeGroupAffinitySpecs) satisfy(labels map[string]string) bool {
	for _, spec := range specs.specs {
		topologyKey := spec.topologyKey
		topologyValue, ok := labels[topologyKey]
		if !ok {
			return false
		}
		if topologyValue != spec.topologyValue {
			return false
		}
	}
	return true
}

func (specs *nodeGroupAffinitySpecs) equal(other *nodeGroupAffinitySpecs) bool {
	return specs.key == other.key
}

func (specs *nodeGroupAffinitySpecs) getNodeGroupKey() string {
	return specs.key
}

// ------------------------------------------------------------------------------------------

// nodeGroupCandidates is used to filter nodes into different NodeGroups from given NodeGroups.
// In current implementation, a node can be added to nodeGroupCandidates when all affinity requirements are matched.
// Then for each node, there is only one NodeGroupKey can be found in nodeGroupCandidates.
type nodeGroupCandidates struct {
	// key is NodeGroup key, value is node name set
	candidates map[string]sets.String
	// key is node name, value is NodeGroupKey
	nodeTopologyMap map[string]string
}

func (candidates *nodeGroupCandidates) addNodes(nodeGroupKey string, nodeNames ...string) {
	if candidates.candidates[nodeGroupKey] == nil {
		candidates.candidates[nodeGroupKey] = sets.NewString()
	}
	for _, nodeName := range nodeNames {
		candidates.candidates[nodeGroupKey].Insert(nodeName)
		candidates.nodeTopologyMap[nodeName] = nodeGroupKey
	}
}

func (candidates *nodeGroupCandidates) isNodeIn(nodeName string) bool {
	_, ok := candidates.nodeTopologyMap[nodeName]
	return ok
}

func (candidates *nodeGroupCandidates) getNodeTopology(nodeName string) string {
	return candidates.nodeTopologyMap[nodeName]
}

func newNodeGroupCandidates() *nodeGroupCandidates {
	return &nodeGroupCandidates{
		candidates:      make(map[string]sets.String),
		nodeTopologyMap: make(map[string]string),
	}
}

func getPotentialCandidates(podLauncher podutil.PodLauncher, nodeGroupKey string, current sets.String, nodes []framework.NodeInfo, affinities *nodeGroupAffinitySpecs) *nodeGroupCandidates {
	candidates := newNodeGroupCandidates()
	candidates.addNodes(nodeGroupKey, current.UnsortedList()...)
	for _, node := range nodes {
		nodeName := node.GetNodeName()
		if current.Has(nodeName) {
			continue
		}

		labels, matched := getNodeLabelsIfLauncherMatches(podLauncher, node)
		if !matched {
			continue
		}
		if affinities.satisfy(labels) {
			candidates.addNodes(nodeGroupKey, nodeName)
		}
	}
	return candidates
}

// ------------------------------------------------------------------------------------------

func getNodeLabelsIfLauncherMatches(launcher podutil.PodLauncher, node framework.NodeInfo) (map[string]string, bool) {
	switch launcher {
	case podutil.Kubelet:
		if node.GetNode() != nil {
			return node.GetNode().Labels, true
		}
	case podutil.NodeManager:
		if node.GetNMNode() != nil {
			return node.GetNMNode().Labels, true
		}
	}
	return nil, false
}

func getRequiredAffinitySpecs(
	ctx context.Context,
	podLauncher podutil.PodLauncher,
	requiredAffinityTerms *nodeGroupAffinityTerms,
	assigned sets.String,
	nodeLister framework.NodeInfoLister,
) (*nodeGroupAffinitySpecs, error) {
	// one from all nodeInfos MUST match all required affinity requirements
	var nodeGroupTopology *nodeGroupAffinitySpecs
	var lock sync.Mutex
	// Potential conflicts here: current implementation is RequiredDuringSchedulingIgnoredDuringRunning,
	// if topology changes on nodes where pods are running in the unit, the result will be incorrect.
	// If we want to support RequiredDuringSchedulingRequiredDuringRunning, modifications in kubelet is needed.
	// In current situation, this situation is not handled yet. The topology of first node that meets requirement will be used.
	nodes := assigned.List()
	errCh := parallelize.NewErrorChannel()
	parallelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	parallelize.Until(parallelCtx, len(nodes), func(index int) {
		nodeName := nodes[index]
		nodeInfo, err := nodeLister.Get(nodeName)
		if err != nil {
			errCh.SendErrorWithCancel(err, cancel)
			return
		}

		matchedLabels, matched := getNodeLabelsIfLauncherMatches(podLauncher, nodeInfo)
		if matched {
			lock.Lock()
			defer lock.Unlock()
			if nodeGroupTopology == nil {
				nodeGroupTopology, _ = requiredAffinityTerms.getAffinitySpecs(matchedLabels)
				return
			}
			nodeTopology, _ := requiredAffinityTerms.getAffinitySpecs(matchedLabels)
			if !nodeTopology.equal(nodeGroupTopology) {
				errCh.SendErrorWithCancel(fmt.Errorf("assigned pods are in different topologies: %v, %v", nodeTopology.getNodeGroupKey(), nodeGroupTopology.getNodeGroupKey()), cancel)
				return
			}
		}
	})

	if err := errCh.ReceiveError(); err != nil {
		return nil, err
	}

	return nodeGroupTopology, nil
}

// ------------------------------------------------------------------------------------------

func printNodeGroups(nodeGroups []framework.NodeGroup) string {
	var builder strings.Builder
	builder.WriteByte('[')
	for _, nodeGroup := range nodeGroups {
		builder.WriteString(nodeGroup.GetKey())
		builder.WriteByte(';')
	}
	builder.WriteByte(']')
	return builder.String()
}
