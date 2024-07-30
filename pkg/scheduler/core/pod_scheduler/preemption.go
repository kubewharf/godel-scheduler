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

package podscheduler

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	utiltrace "k8s.io/utils/trace"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/nodeports"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/core"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/coscheduling"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodeaffinity"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodelabel"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/noderesources"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodeunschedulable"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodevolumelimits"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nonnativeresource"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/podlauncher"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/tainttoleration"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/volumebinding"
	preemption "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins"
	preemptionplugins "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/preemption-plugins"
	frameworkruntime "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/runtime"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

const (
	ReasonNotEligibleToPreemptOthers      string = "pod is not eligible for more preemption"
	ReasonUnresolvableByPreemption        string = "preemption will not help schedule pod on any node"
	ReasonPreemptionCandidatesNotFound    string = "can not find any candidates to preempt"
	ReasonPreemptionBestCandidateNotFound string = "best candidate for preemption not found"
)

var pluginsSkipCheckingDuringPreemption = sets.NewString(
	nodeaffinity.Name,
	tainttoleration.Name,
	nodeunschedulable.Name,
	podlauncher.Name,
	volumebinding.Name,
	nodelabel.Name,
	nodeports.Name,
	noderesources.FitName,
	nodevolumelimits.CSIName,
	nodevolumelimits.CinderName,
	nodevolumelimits.AzureDiskName,
	nodevolumelimits.GCEPDName,
	nodevolumelimits.EBSName,
	nonnativeresource.NonNativeTopologyName,
	coscheduling.Name,
)

type betterSelectPolicy func(context.Context, *framework.CycleState, *v1.Pod, podutil.PodResourceType, []int, []framework.NodeInfo, int, framework.SchedulerFramework, framework.SchedulerPreemptionFramework) ([]int, int)

func (gs *podScheduler) PreemptInSpecificNodeGroup(
	ctx context.Context, f framework.SchedulerFramework,
	pf framework.SchedulerPreemptionFramework,
	unitState, commonPreemptionState, state *framework.CycleState, pod *v1.Pod,
	nodeGroup framework.NodeGroup, nodeToStatus framework.NodeToStatusMap,
	cachedNominatedNodes *framework.CachedNominatedNodes,
) (result core.PodScheduleResult, err error) {
	if framework.GetPodSchedulingStageInCycleState(state, framework.PreemptInPreferredNodes) {
		if result, err = gs.PreemptInPreferredNodes(ctx, f, pf, unitState, commonPreemptionState, state, pod, nodeGroup, nodeToStatus, cachedNominatedNodes); err == nil {
			return
		}
	} else {
		klog.InfoS("Skip PreemptInPreferredNodes scheduling stage for pod", "pod", podutil.GetPodKey(pod))
	}
	if framework.GetPodSchedulingStageInCycleState(state, framework.PreemptInNodeCircles) {
		if result, err = gs.PreemptInNodeCircles(ctx, f, pf, unitState, commonPreemptionState, state, pod, nodeGroup, nodeToStatus, cachedNominatedNodes); err == nil {
			return
		}
	} else {
		klog.InfoS("Skip PreemptInNodeCircles scheduling stage for pod", "pod", podutil.GetPodKey(pod))
	}
	return result, err
}

func (gs *podScheduler) PreemptInPreferredNodes(
	ctx context.Context, f framework.SchedulerFramework,
	pf framework.SchedulerPreemptionFramework,
	unitState, commonPreemptionState, state *framework.CycleState, pod *v1.Pod,
	nodeGroup framework.NodeGroup, nodeToStatus framework.NodeToStatusMap,
	cachedNominatedNodes *framework.CachedNominatedNodes,
) (result core.PodScheduleResult, err error) {
	if len(nodeGroup.GetPreferredNodes().List()) == 0 {
		return core.PodScheduleResult{}, fmt.Errorf("can not preempt in empty preferred nodes")
	}

	// Prepare information.
	podTrace, _ := framework.GetPodTrace(state)
	preemptTraceContext := podTrace.GetTraceContext(tracing.SchedulerPreemptPodSpan)
	preferredNodes := nodeGroup.GetPreferredNodes()
	preferredNodesCount := len(preferredNodes.List())

	for i, nodeInfo := range preferredNodes.List() {
		nodeName := nodeInfo.GetNodeName()
		hooks := preferredNodes.Get(nodeName)
		nodeInfoToUse, stateToUse, preferStatus := nodeInfo, state, &framework.Status{}

		preemptTraceContext.WithFields(tracing.WithMessageField(fmt.Sprintf("Started to check preferred node, nodeName: %s, preferredNodeIndex: %d, totalPreferredNodes: %d", nodeName, i, preferredNodesCount)))
		if nodeInfoToUse, stateToUse, preferStatus = hooks.PrePreferNode(ctx, unitState, stateToUse, pod, nodeInfoToUse); !preferStatus.IsSuccess() {
			msg := "Failed to run PrePreferNode on node and skip this node"
			statusErr := preferStatus.AsError()

			klog.V(4).InfoS(msg, "pod", podutil.GetPodKey(pod), "nodeName", nodeName, "err", statusErr)
			preemptTraceContext.WithFields(tracing.WithMessageField(fmt.Sprintf("msg: %s, nodeName: %s, err: %v", msg, nodeName, statusErr)))
			continue
		}

		nodeSet := []framework.NodeInfo{nodeInfoToUse}
		nominatedNodeName, victims, err := gs.runPreemption(ctx, f, pf, stateToUse, commonPreemptionState, pod, nodeSet, nodeToStatus, cachedNominatedNodes)

		// TODO: how to define `fit` in preemption?
		fit := err == nil && len(nominatedNodeName) > 0
		// ATTENTION: status.IsSuccess() == true if and only if fit == true
		// TODO: more specific message after preemption framework constructed.
		var status *framework.Status
		if !fit {
			status = framework.NewStatus(framework.Error, "failed to preempt")
		}

		if preferStatus = hooks.PostPreferNode(ctx, unitState, state, pod, nodeInfo, status); !preferStatus.IsSuccess() {
			msg := "Failed to run PostPreferNode on node and skip this node"
			statusErr := preferStatus.AsError()

			klog.V(4).InfoS(msg, "pod", podutil.GetPodKey(pod), "nodeName", nodeName, "err", statusErr)
			preemptTraceContext.WithFields(tracing.WithMessageField(fmt.Sprintf("msg: %s, nodeName: %s, err: %v", msg, nodeName, statusErr)))
			continue
		}

		if fit {
			preemptTraceContext.WithFields(tracing.WithMessageField("Succeed to find feasible preferred node"))
			return core.PodScheduleResult{
				NominatedNode: utils.ConstructNominatedNode(nominatedNodeName, victims),
				Victims:       victims,
				// TODO: revisit this. Left the FilteredNodesStatuses to be nil for preferred nodes.
				FilteredNodesStatuses: nil,
			}, nil
		}
	}
	preemptTraceContext.WithFields(tracing.WithMessageField("Can not preempt in preferred nodes"))
	klog.InfoS("Can not preempt in preferred nodes", "pod", podutil.GetPodKey(pod))
	return core.PodScheduleResult{}, fmt.Errorf("can not preempt in non-empty preferred nodes")
}

func (gs *podScheduler) PreemptInNodeCircles(
	ctx context.Context, f framework.SchedulerFramework,
	pf framework.SchedulerPreemptionFramework,
	unitState, commonPreemptionState, state *framework.CycleState, pod *v1.Pod,
	nodeGroup framework.NodeGroup, nodeToStatus framework.NodeToStatusMap,
	cachedNominatedNodes *framework.CachedNominatedNodes,
) (result core.PodScheduleResult, err error) {
	podTrace, _ := framework.GetPodTrace(state)
	preemptTraceContext := podTrace.GetTraceContext(tracing.SchedulerPreemptPodSpan)

	trace := utiltrace.New("Preemption", utiltrace.Field{Key: "namespace", Value: pod.Namespace}, utiltrace.Field{Key: "name", Value: pod.Name})
	defer trace.LogIfLong(100 * time.Millisecond)

	// check cached nominated nodes
	{
		nominatedNodeName, victims := gs.PreemptBasedOnCachedNominatedNodes(ctx, f, pf, state, commonPreemptionState, pod, cachedNominatedNodes)
		if len(nominatedNodeName) > 0 {
			preemptTraceContext.WithFields(tracing.WithMessageField("Succeed to find feasible node base on cached nominated nodes"))
			preemptTraceContext.WithTags(tracing.WithHitCacheTag(tracing.ResultSuccess))
			return core.PodScheduleResult{
				NominatedNode: utils.ConstructNominatedNode(nominatedNodeName, victims),
				Victims:       victims,
			}, nil
		}
		klog.InfoS("Can not preempt in cached nominated nodes", "pod", podutil.GetPodKey(pod))
		preemptTraceContext.WithFields(tracing.WithMessageField("Can not find feasible node base on cached nominated nodes"))
		preemptTraceContext.WithTags(tracing.WithHitCacheTag(tracing.ResultFailure))
	}

	preemptInSpecificNodeCircle := func(nodeCircle framework.NodeCircle) (result core.PodScheduleResult, err error) {
		nodeSet := nodeCircle.List()
		nominatedNodeName, victims, err := gs.runPreemption(ctx, f, pf, state, commonPreemptionState, pod, nodeSet, nodeToStatus, cachedNominatedNodes)
		if err != nil || len(nominatedNodeName) == 0 {
			klog.ErrorS(err, "Failed to run preemption in node circle", "pod", podutil.GetPodKey(pod), "nodeCircle", nodeCircle.GetKey())
			preemptTraceContext.WithFields(tracing.WithMessageField(fmt.Sprintf("Failed to run preemption, err: %v", err)))
			return core.PodScheduleResult{}, err
		}

		klog.InfoS("Succeed to run preemption in node circle", "pod", podutil.GetPodKey(pod), "nodeCircle", nodeCircle.GetKey(), "nominatedNodeName", nominatedNodeName, "victims", victims)
		preemptTraceContext.WithFields(tracing.WithMessageField(fmt.Sprintf("Succeed to run preemption, nominatedNodeName: %s, victims: %#v", nominatedNodeName, victims)))
		return core.PodScheduleResult{
			NominatedNode: utils.ConstructNominatedNode(nominatedNodeName, victims),
			Victims:       victims,
		}, nil
	}

	nodeCircles := nodeGroup.GetNodeCircles()
	nodeCirclesCount := len(nodeCircles)
	for i, nodeCircle := range nodeCircles {
		klog.InfoS("Start preempt in specific node circle", "pod", podutil.GetPodKey(pod), "nodeCircle", nodeCircle.GetKey())
		preemptTraceContext.WithFields(tracing.WithMessageField(fmt.Sprintf("Start to preempt in specific node circle, nodeCircleKey: %s, nodeCircleIndex: %d, totalNodeCircles: %d", nodeCircle.GetKey(), i, nodeCirclesCount)))

		result, err = preemptInSpecificNodeCircle(nodeCircle)
		if result.NominatedNode != nil {
			break // should return
		}

		preemptTraceContext.WithFields(tracing.WithMessageField(fmt.Sprintf("Failed to preempt in this nodeCircle, err: %v", err)))
	}

	return result, err
}

func (gs *podScheduler) PreemptBasedOnCachedNominatedNodes(
	ctx context.Context, f framework.SchedulerFramework,
	pf framework.SchedulerPreemptionFramework,
	state, commonPreemptionState *framework.CycleState, pod *v1.Pod,
	cachedNominatedNodes *framework.CachedNominatedNodes,
) (string, *framework.Victims) {
	if cachedNominatedNodes.IsEmpty() {
		return "", nil
	}

	hasCrossNodeConstraints := cachedNominatedNodes.HasCrossNodeConstraints()
	switch gs.candidateSelectPolicy {
	case config.CandidateSelectPolicyBest:
		if hasCrossNodeConstraints != nil && *hasCrossNodeConstraints {
			return "", nil
		}
	case config.CandidateSelectPolicyBetter:
		if hasCrossNodeConstraints != nil && *hasCrossNodeConstraints {
			return "", nil
		}
	case config.CandidateSelectPolicyRandom:
	default:
		return "", nil
	}

	if status := f.RunPreFilterPlugins(ctx, state, pod); !status.IsSuccess() {
		return "", nil
	}
	if status := pf.RunClusterPrePreemptingPlugins(pod, state, commonPreemptionState); !status.IsSuccess() {
		return "", nil
	}

	usedCandidate := cachedNominatedNodes.GetUsedNominatedNode()
	if usedCandidate != nil {
		usedCandidate = gs.preemptOnNode(ctx, f, pf, state, pod, usedCandidate.Name)
	}
	unusedCandidates := cachedNominatedNodes.GetUnusedNominatedNodes()

	bestCandidate := gs.SelectCandidate(ctx, f, pf, state, pod, unusedCandidates, usedCandidate, cachedNominatedNodes, true)
	if bestCandidate == nil {
		return "", nil
	}
	if status := pf.RunNodePostPreemptingPlugins(pod, bestCandidate.Victims.Pods, state, commonPreemptionState); !status.IsSuccess() {
		return "", nil
	}
	return bestCandidate.Name, bestCandidate.Victims
}

func (gs *podScheduler) preemptOnNode(
	ctx context.Context, f framework.SchedulerFramework,
	pf framework.SchedulerPreemptionFramework,
	state *framework.CycleState, pod *v1.Pod,
	nodeName string,
) *framework.Candidate {
	if nodeName == "" {
		return nil
	}
	preemptionState := framework.NewCycleState()
	nodeInfo := gs.snapshot.GetNodeInfo(nodeName)
	pods, fits := gs.selectVictimsOnNode(ctx, state, preemptionState, f, pf, pod, nodeInfo)
	if fits {
		victims := framework.Victims{
			Pods:            pods,
			PreemptionState: preemptionState,
		}
		return &framework.Candidate{
			Victims: &victims,
			Name:    nodeName,
		}
	}
	return nil
}

func (gs *podScheduler) prepareNodes(ctx context.Context, preemptNodeCandidates []framework.NodeInfo, m framework.NodeToStatusMap) ([]framework.NodeInfo, error) {
	// 1) Filter nodes that might be useful by preemption.
	potentialCandidates := make([]framework.NodeInfo, len(preemptNodeCandidates))
	stop := false
	passed := false
	checkNode := func(i int) {
		node := preemptNodeCandidates[i]
		name := node.GetNodeName()
		// We reply on the status by each plugin - 'Unschedulable' or 'UnschedulableAndUnresolvable'
		// to determine whether preemption may help or not on the node.
		if m[name].Code() != framework.UnschedulableAndUnresolvable {
			potentialCandidates[i] = node
			passed = true
		}
	}
	util.ParallelizeUntil(&stop, 32, len(preemptNodeCandidates), checkNode)

	if !passed {
		// no potential nodes, return nil.
		return nil, errors.New(ReasonUnresolvableByPreemption)
	}

	return potentialCandidates, nil
}

func (gs *podScheduler) runPreemption(ctx context.Context,
	f framework.SchedulerFramework, pf framework.SchedulerPreemptionFramework,
	state, commonPreemptionState *framework.CycleState, clonePod *v1.Pod,
	nodeSet []framework.NodeInfo, nodeToStatus framework.NodeToStatusMap,
	cachedNominatedNodes *framework.CachedNominatedNodes,
) (string, *framework.Victims, error) {
	// 0) Prepare preemptor pod and nodes.
	start := time.Now()
	pod, canPreemptOthers, err := gs.preparePod(ctx, clonePod)
	if err != nil {
		return "", nil, err
	} else if !canPreemptOthers {
		return "", nil, errors.New(ReasonNotEligibleToPreemptOthers)
	}
	completePreparePod := time.Now()
	klog.InfoS("Complete prepare pod", "pod", podutil.GetPodKey(pod), "duration", completePreparePod.Sub(start))

	nodeSet, err = gs.prepareNodes(ctx, nodeSet, nodeToStatus)
	if err != nil {
		return "", nil, err
	}
	completePrepareNode := time.Now()
	klog.InfoS("Complete prepare node", "pod", podutil.GetPodKey(pod), "duration", completePrepareNode.Sub(completePreparePod))

	// 1) Find all preemption candidates.
	podProperty, _ := framework.GetPodProperty(state)

	findCandidatesStart := time.Now()
	candidates, err := gs.FindCandidates(ctx, f, pf, state, commonPreemptionState, pod, nodeSet, cachedNominatedNodes)
	if err != nil {
		metrics.PreemptingStageLatencyObserve(podProperty, metrics.PreemptingFindCandidates, helper.SinceInSeconds(findCandidatesStart))
		return "", nil, err
	}
	if len(candidates) == 0 {
		metrics.PreemptingStageLatencyObserve(podProperty, metrics.PreemptingFindCandidates, helper.SinceInSeconds(findCandidatesStart))
		return "", nil, errors.New(ReasonPreemptionCandidatesNotFound)
	}
	metrics.PreemptingStageLatencyObserve(podProperty, metrics.PreemptingFindCandidates, helper.SinceInSeconds(findCandidatesStart))

	// 2) Find the best candidate.
	selectCandidateStart := time.Now()
	bestCandidate := gs.SelectCandidate(ctx, f, pf, state, pod, candidates, nil, cachedNominatedNodes, false)
	if bestCandidate == nil || len(bestCandidate.Name) == 0 {
		metrics.PreemptingStageLatencyObserve(podProperty, metrics.PreemptingSelectCandidate, helper.SinceInSeconds(selectCandidateStart))
		return "", nil, errors.New(ReasonPreemptionBestCandidateNotFound)
	}
	metrics.PreemptingStageLatencyObserve(podProperty, metrics.PreemptingSelectCandidate, helper.SinceInSeconds(selectCandidateStart))

	if status := pf.RunNodePostPreemptingPlugins(pod, bestCandidate.Victims.Pods, state, commonPreemptionState); !status.IsSuccess() {
		return "", nil, status.AsError()
	}

	return bestCandidate.Name, bestCandidate.Victims, nil
}

func (gs *podScheduler) preparePod(ctx context.Context, pod *v1.Pod) (*v1.Pod, bool, error) {
	// 0) Fetch the latest version of <pod>.
	// It's safe to directly fetch pod here. Because the informer cache has already been
	// initialized when creating the Scheduler obj.
	// However, tests may need to manually initialize the shared pod informer.
	pod, err := gs.podLister.Pods(pod.Namespace).Get(pod.Name)
	if err != nil {
		klog.ErrorS(err, "Failed to get the updated preemptor pod object")
		return pod, false, err
	}
	// 1) Ensure the preemptor is eligible to preempt other pods.
	if !preemptionplugins.PodEligibleToPreemptOthers(pod, gs.pcLister) {
		klog.V(5).InfoS(ReasonNotEligibleToPreemptOthers, "pod", pod.Namespace+"/"+pod.Name)
		return pod, false, nil
	}
	return pod, true, nil
}

// FindCandidates calculates a slice of preemption candidates.
// Each candidate is executable to make the given <pod> schedulable.
func (gs *podScheduler) FindCandidates(ctx context.Context,
	f framework.SchedulerFramework, pf framework.SchedulerPreemptionFramework,
	state, commonState *framework.CycleState, pod *v1.Pod, nodesList []framework.NodeInfo,
	cachedNominatedNodes *framework.CachedNominatedNodes,
) ([]*framework.Candidate, error) {
	if status := f.RunPreFilterPlugins(ctx, state, pod); !status.IsSuccess() {
		return nil, status.AsError()
	}

	if status := pf.RunClusterPrePreemptingPlugins(pod, state, commonState); !status.IsSuccess() {
		return nil, status.AsError()
	}

	candidateSelectPolicy := gs.GetCandidateSelectPolicy()
	switch candidateSelectPolicy {
	case config.CandidateSelectPolicyBest:
		// check all nodes
		candidates, err := gs.bestPreemption(ctx, state, pod, nodesList, f, pf)
		return candidates, err
	case config.CandidateSelectPolicyBetter:
		// check all nodes
		candidates, err := gs.betterPreemption(ctx, state, pod, nodesList, cachedNominatedNodes, f, pf)
		return candidates, err
	case config.CandidateSelectPolicyRandom:
		// check all nodes
		candidates, err := gs.randomPreemption(ctx, state, pod, nodesList, cachedNominatedNodes, f, pf)
		return candidates, err
	}
	return nil, fmt.Errorf("unexpected candidate select policy: %s", candidateSelectPolicy)
}

func (gs *podScheduler) randomPreemption(
	ctx context.Context, state *framework.CycleState,
	pod *v1.Pod, nodesList []framework.NodeInfo,
	cachedNominatedNodes *framework.CachedNominatedNodes, fw framework.SchedulerFramework,
	pfw framework.SchedulerPreemptionFramework,
) ([]*framework.Candidate, error) {
	var candidates []*framework.Candidate
	var lock sync.Mutex
	resourceType, _ := podutil.GetPodResourceType(pod)
	partitionPriority := preemptionplugins.GetPodPartitionPriority(pod)
	podResource, _, _ := framework.CalculateResource(pod)

	var stop bool
	var count int
	checkNode := func(i int) {
		nodeInfo := nodesList[i]
		if nodeInfo == nil {
			return
		}

		// Perform heuristic checks to terminate the preemption process early.
		if !preemption.OccupiableResourcesCheck(partitionPriority, resourceType, podResource, nodeInfo) {
			return
		}

		preemptionState := framework.NewCycleState()
		// We will not clone the NodeInfo here immediately, but only when needed within `selectVictimsOnNode`.
		pods, fits := gs.selectVictimsOnNode(ctx, state, preemptionState, fw, pfw, pod, nodeInfo)
		if fits {
			victims := framework.Victims{
				Pods:            pods,
				PreemptionState: preemptionState,
			}
			c := &framework.Candidate{
				Victims: &victims,
				Name:    nodeInfo.GetNodeName(),
			}

			podsCanNotBePreempted, _ := framework.GetPodsCanNotBePreempted(preemptionState)
			lock.Lock()
			if count >= cachedNominatedNodes.GetPodCount() {
				lock.Unlock()
				return
			}
			candidates = append(candidates, c)
			if len(podsCanNotBePreempted) == 0 {
				count++
			}
			if count >= cachedNominatedNodes.GetPodCount() {
				stop = true
			}
			lock.Unlock()
		}
	}

	stop = false
	util.ParallelizeUntil(&stop, 32, len(nodesList), checkNode)
	return candidates, nil
}

func (gs *podScheduler) bestPreemption(
	ctx context.Context, state *framework.CycleState,
	pod *v1.Pod, nodesList []framework.NodeInfo,
	fw framework.SchedulerFramework,
	pfw framework.SchedulerPreemptionFramework,
) ([]*framework.Candidate, error) {
	var candidates []*framework.Candidate
	var lock sync.Mutex
	resourceType, _ := podutil.GetPodResourceType(pod)
	partitionPriority := preemptionplugins.GetPodPartitionPriority(pod)
	podResource, _, _ := framework.CalculateResource(pod)

	var stop bool
	checkNode := func(i int) {
		nodeInfo := nodesList[i]
		if nodeInfo == nil {
			return
		}

		// Perform heuristic checks to terminate the preemption process early.
		if !preemption.OccupiableResourcesCheck(partitionPriority, resourceType, podResource, nodeInfo) {
			return
		}

		preemptionState := framework.NewCycleState()
		// We will not clone the NodeInfo here immediately, but only when needed within `selectVictimsOnNode`.
		pods, fits := gs.selectVictimsOnNode(ctx, state, preemptionState, fw, pfw, pod, nodeInfo)
		if fits {
			victims := framework.Victims{
				Pods:            pods,
				PreemptionState: preemptionState,
			}
			c := &framework.Candidate{
				Victims: &victims,
				Name:    nodeInfo.GetNodeName(),
			}

			lock.Lock()
			candidates = append(candidates, c)
			lock.Unlock()
		}
	}

	// check all node list
	stop = false
	util.ParallelizeUntil(&stop, 32, len(nodesList), checkNode)
	return candidates, nil
}

func (gs *podScheduler) betterPreemption(
	ctx context.Context, state *framework.CycleState,
	pod *v1.Pod, nodesList []framework.NodeInfo,
	cachedNominatedNodes *framework.CachedNominatedNodes,
	fw framework.SchedulerFramework,
	pfw framework.SchedulerPreemptionFramework,
) ([]*framework.Candidate, error) {
	if crossNodes := cachedNominatedNodes.HasCrossNodeConstraints(); crossNodes != nil && *crossNodes {
		cachedNominatedNodes.SetPodCount(1)
	}
	deployName := util.GetDeployNameFromPod(pod)
	gs.CleanupPreemptionPolicyForPodOwner()
	betterSelectPolicies := gs.GetBetterSelectPolicies()
	betterSelectPolicySet := sets.NewString(betterSelectPolicies...)
	preemptionPolicy := gs.GetPreemptionPolicy(deployName)
	if preemptionPolicy == "" {
		preemptionPolicy = betterSelectPolicies[0]
	}
	if gs.betterSelectPoliciesRegistry[preemptionPolicy] == nil {
		return nil, fmt.Errorf("unexpected preemption policy: %s", preemptionPolicy)
	}
	klog.InfoS("Better preemption policy for pod", "pod", podutil.GeneratePodKey(pod), "policy", preemptionPolicy)

	// get all priorities
	resourceType, err := framework.GetPodResourceType(state)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	stop := false
	priorities := make([][]int64, len(nodesList))
	getPriorities := func(i int) {
		nodeInfo := nodesList[i]
		if nodeInfo == nil {
			return
		}
		priorities[i] = nodeInfo.GetPrioritiesForPodsMayBePreempted(resourceType)
	}
	util.ParallelizeUntil(&stop, 32, len(nodesList), getPriorities)

	prioritySet := sets.NewInt()
	for _, prioritiesEachNode := range priorities {
		for _, priority := range prioritiesEachNode {
			prioritySet.Insert(int(priority))
		}
	}

	podPriority := podutil.GetPodPriority(pod)
	var prioritiesSlice []int
	for _, priority := range prioritySet.List() {
		if priority > int(podPriority) {
			break
		}
		prioritiesSlice = append(prioritiesSlice, priority)
	}

	var (
		nominatedNodeIndex     []int
		maxVictimPriorityIndex = -1
	)

	if len(prioritiesSlice) == 0 {
		var lock sync.Mutex
		stop := false
		checkNode := func(i int) {
			nodeInfo := nodesList[i]
			if fits, _, _, _ := frameworkruntime.PodPassesFiltersOnNode(ctx, fw, state, pod, nodeInfo); fits {
				lock.Lock()
				nominatedNodeIndex = append(nominatedNodeIndex, i)
				if len(nominatedNodeIndex) >= cachedNominatedNodes.GetPodCount() {
					stop = true
				}
				lock.Unlock()
			}
		}
		util.ParallelizeUntil(&stop, 32, len(nodesList), checkNode)
		klog.InfoS("Try schedule in preemption step", "pod", podutil.GeneratePodKey(pod), "policy", config.BetterPreemptionPolicyAscending)
	} else {
		preparePrioritiesDuration := time.Since(start)
		klog.InfoS("Better preemption prepare priorities for pod", "pod", podutil.GeneratePodKey(pod), "duration", preparePrioritiesDuration)
		podProperty, _ := framework.GetPodProperty(state)
		metrics.PreemptingStageLatencyObserve(podProperty, metrics.PreemptingPreparePriorities, preparePrioritiesDuration.Seconds())

		nominatedNodeIndex, maxVictimPriorityIndex = gs.betterSelectPoliciesRegistry[preemptionPolicy](ctx, state, pod, resourceType, prioritiesSlice, nodesList, cachedNominatedNodes.GetPodCount(), fw, pfw)
	}

	if nominatedNodeIndex == nil {
		if betterSelectPolicySet.Has(config.BetterPreemptionPolicyDichotomy) {
			gs.CachePreemptionPolicy(deployName, config.BetterPreemptionPolicyDichotomy)
		}
		return nil, nil
	} else if maxVictimPriorityIndex > int(math.Log2(float64(len(prioritiesSlice)))) {
		if betterSelectPolicySet.Has(config.BetterPreemptionPolicyDichotomy) {
			gs.CachePreemptionPolicy(deployName, config.BetterPreemptionPolicyDichotomy)
		}
	} else {
		if betterSelectPolicySet.Has(config.BetterPreemptionPolicyAscending) {
			gs.CachePreemptionPolicy(deployName, config.BetterPreemptionPolicyAscending)
		}
	}

	var candidates []*framework.Candidate
	for _, index := range nominatedNodeIndex {
		preemptionState := framework.NewCycleState()
		pods, fits := gs.selectVictimsOnNode(ctx, state, preemptionState, fw, pfw, pod, nodesList[index])
		if fits {
			victims := framework.Victims{
				Pods:            pods,
				PreemptionState: preemptionState,
			}
			c := &framework.Candidate{
				Victims: &victims,
				Name:    nodesList[index].GetNodeName(),
			}
			candidates = append(candidates, c)
		} else {
			klog.InfoS("WARN: Unexpected preemption error", "preemptionPolicy", preemptionPolicy, "pod", klog.KObj(pod), "node", nodesList[index].GetNodeName())
		}
	}
	return candidates, nil
}

// selectVictimsOnNode finds minimum set of pods on the given node that should
// be preempted in order to make enough room for "pod" to be scheduled. The
// minimum set selected is subject to the constraint that a higher-priority pod
// is never preempted when a lower-priority pod could be (higher/lower relative
// to one another, not relative to the preemptor "pod").
// The algorithm first checks if the pod can be scheduled on the node when all the
// lower priority pods are gone. If so, it sorts all the lower priority pods by
// their priority and then puts them into two groups of those whose PodDisruptionBudget
// will be violated if preempted and other non-violating pods. Both groups are
// sorted by priority. It first tries to reprieve as many PDB violating pods as
// possible and then does them same for non-PDB-violating pods while checking
// that the "pod" can still fit on the node.
// NOTE: This function assumes that it is never called if "pod" cannot be scheduled
// due to pod affinity, node affinity, or node anti-affinity reasons. None of
// these predicates can be satisfied by removing more pods from the node.
func (gs *podScheduler) selectVictimsOnNode(
	ctx context.Context,
	state *framework.CycleState,
	preemptionState *framework.CycleState,
	fw framework.SchedulerFramework,
	pfw framework.SchedulerPreemptionFramework,
	pod *v1.Pod,
	nodeInfo framework.NodeInfo,
) ([]*v1.Pod, bool) {
	addPod := func(stateToUse *framework.CycleState, ap *v1.Pod, nodeInfo framework.NodeInfo) error {
		nodeInfo.AddPod(ap)
		status := fw.RunPreFilterExtensionAddPod(ctx, stateToUse, pod, ap, nodeInfo)
		if !status.IsSuccess() {
			return status.AsError()
		}
		return nil
	}

	var skipPlugins []string

	// do not need to preempt
	nodeName := nodeInfo.GetNodeName()
	if fits, _, statusMap, err := frameworkruntime.PodPassesFiltersOnNode(ctx, fw, state, pod, nodeInfo); fits {
		if err != nil {
			klog.InfoS("Failed to check if need to preempt victims on node", "node", nodeName, "err", err)
		}
		return nil, true
	} else {
		// The pod needs to be placed by preemption. In this case, skip the partial FilterPlugins if possible.
		for name, status := range statusMap {
			if status.IsSuccess() && pluginsSkipCheckingDuringPreemption.Has(name) {
				skipPlugins = append(skipPlugins, name)
			}
		}
	}

	// As the first step, remove all the lower priority pods(only remove pods don't violate PDB) from the node and
	// check if the given pod can be scheduled.
	// 2) Find all preemption candidates.
	// pick useful information from queue info
	filterVictimsPodsStart := time.Now()
	priority := preemptionplugins.GetPodPartitionPriority(pod)
	potentialVictims := preemption.FilterVictimsPods(gs, pfw, state, preemptionState, nodeInfo, pod, math.MinInt64, priority, false)
	podProperty, _ := framework.GetPodProperty(state)
	metrics.PreemptingStageLatencyObserve(podProperty,
		metrics.PreemptingFilterVictims, helper.SinceInSeconds(filterVictimsPodsStart))
	// No potential victims are found, and so we don't need to evaluate the node again since its state didn't change.
	if len(potentialVictims) == 0 {
		return nil, false
	}

	podsCanNotBePreempted, _ := framework.GetPodsCanNotBePreempted(preemptionState)
	podsCanNotBePreemptedSet := sets.NewString(podsCanNotBePreempted...)
	sort.SliceStable(potentialVictims, func(i, j int) bool {
		return moreImportantPod(potentialVictims[i], potentialVictims[j], podsCanNotBePreemptedSet)
	})
	// Clone NodeInfo here to perform `removePod`.
	nodeInfoCopy := nodeInfo.Clone()
	// TODO: revisit this.
	// Clone CycleState for PodAffinity plugin.
	stateCopy := state.Clone()

	for _, victim := range potentialVictims {
		if err := removePod(ctx, stateCopy, pod, victim, nodeInfoCopy, fw); err != nil {
			return nil, false
		}
	}
	// If the new pod does not fit after removing all the lower priority pods,
	// we are almost done and this node is not suitable for preemption. The only
	// condition that we could check is if the "pod" is failing to schedule due to
	// inter-pod affinity to one or more victims, but we have decided not to
	// support this case for performance reasons. Having affinity to lower
	// priority pods is not a recommended configuration anyway.
	if fits, _, _, err := frameworkruntime.PodPassesFiltersOnNode(ctx, fw, stateCopy, pod, nodeInfoCopy, skipPlugins...); !fits {
		if err != nil {
			klog.InfoS("Failed to select victims on node", "node", nodeName, "err", err)
		}
		return nil, false
	}

	var victims []*v1.Pod
	reprievePod := func(p *v1.Pod, nodeInfo framework.NodeInfo) (bool, error) {
		if err := addPod(stateCopy, p, nodeInfo); err != nil {
			return false, err
		}
		fits, _, _, _ := frameworkruntime.PodPassesFiltersOnNode(ctx, fw, stateCopy, pod, nodeInfo, skipPlugins...)
		if !fits {
			if err := removePod(ctx, stateCopy, pod, p, nodeInfo, fw); err != nil {
				return false, err
			}
			victims = append(victims, p)
			klog.V(5).InfoS("Found a potential preemption victim on node", "pod", klog.KObj(p), "podUID", p.GetUID(), "node", nodeName)
		}
		return fits, nil
	}

	for i := len(potentialVictims) - 1; i >= 0; i-- {
		p := potentialVictims[i]
		if _, err := reprievePod(p, nodeInfoCopy); err != nil {
			klog.InfoS("Failed to reprieve pod", "pod", klog.KObj(p), "err", err)
			return nil, false
		}
	}
	return victims, true
}

func moreImportantPod(pi1, pi2 *v1.Pod, podsCanNotBePreempted sets.String) bool {
	pi1Key := podutil.GeneratePodKey(pi1)
	pi2Key := podutil.GeneratePodKey(pi2)
	if !podsCanNotBePreempted.Has(pi1Key) && podsCanNotBePreempted.Has(pi2Key) {
		return true
	}
	return false
}

func removePod(ctx context.Context, stateToUse *framework.CycleState,
	pod *v1.Pod, rp *v1.Pod, nodeInfo framework.NodeInfo,
	fw framework.SchedulerFramework,
) error {
	if err := nodeInfo.RemovePod(rp, true); err != nil {
		return err
	}
	if status := fw.RunPreFilterExtensionRemovePod(ctx, stateToUse, pod, rp, nodeInfo); !status.IsSuccess() {
		return status.AsError()
	}
	return nil
}

// []int, nodeIndexes
// int, maxVictimPriorityIndex
func (gs *podScheduler) ascendingOrderPreemption(ctx context.Context,
	state *framework.CycleState, pod *v1.Pod, resourceType podutil.PodResourceType,
	prioritiesSlice []int, nodesList []framework.NodeInfo, expectedCount int,
	fw framework.SchedulerFramework, pfw framework.SchedulerPreemptionFramework,
) ([]int, int) {
	var (
		selectedNodeIndex   []int
		preemptionStates          = make([]*framework.CycleState, len(nodesList))
		states                    = make([]*framework.CycleState, len(nodesList))
		nodesListCopy             = make([]framework.NodeInfo, len(nodesList))
		skipPluginsForNodes       = make([][]string, len(nodesList))
		previousPriority    int64 = math.MinInt64
	)
	copy(nodesListCopy, nodesList)
	partitionPriority := preemptionplugins.GetPodPartitionPriority(pod)
	podResource, _, _ := framework.CalculateResource(pod)
	var lock sync.Mutex
	var firstPriorityIndex int = -1
	for priorityIndex, priority := range prioritiesSlice {
		start := time.Now()
		stop := false
		checkNode := func(i int) {
			nodeInfo := nodesListCopy[i]
			if nodeInfo == nil {
				return
			}

			// Perform heuristic checks to terminate the preemption process early.
			// Only need to perform in the first round
			if preemptionStates[i] == nil && !preemption.OccupiableResourcesCheck(partitionPriority, resourceType, podResource, nodeInfo) {
				nodesListCopy[i] = nil
				return
			}

			preemptionState := preemptionStates[i]
			checked := preemptionState != nil
			if !checked {
				preemptionState = framework.NewCycleState()
				preemptionStates[i] = preemptionState
			}
			potentialVictims := preemption.FilterVictimsPods(gs, pfw, state, preemptionState, nodeInfo, pod, previousPriority, int64(priority+1), checked)

			if len(potentialVictims) > 0 && states[i] == nil {
				nodesListCopy[i] = nodesListCopy[i].Clone()
				states[i] = state.Clone()
			}
			nodeInfoCopy := nodesListCopy[i]
			stateCopy := states[i]
			if states[i] == nil {
				stateCopy = state
			}
			for _, rp := range potentialVictims {
				if err := removePod(ctx, stateCopy, pod, rp, nodeInfoCopy, fw); err != nil {
					klog.ErrorS(err, "Failed to remove potential victim", "pod", podutil.GeneratePodKey(pod), "potentialVictim", podutil.GeneratePodKey(rp), "node", nodeInfo.GetNodeName())
				}
			}

			if fits, _, statusMap, _ := frameworkruntime.PodPassesFiltersOnNode(ctx, fw, stateCopy, pod, nodeInfoCopy, skipPluginsForNodes[i]...); fits {
				lock.Lock()
				// avoid select one node repeatedly
				nodesListCopy[i] = nil
				selectedNodeIndex = append(selectedNodeIndex, i)
				if len(selectedNodeIndex) >= expectedCount {
					stop = true
				}
				if firstPriorityIndex < 0 {
					firstPriorityIndex = priorityIndex
				}
				lock.Unlock()
			} else {
				// The pod needs to be placed by preemption. In this case, skip the partial FilterPlugins if possible.
				for name, status := range statusMap {
					if status.IsSuccess() && pluginsSkipCheckingDuringPreemption.Has(name) {
						skipPluginsForNodes[i] = append(skipPluginsForNodes[i], name)
					}
				}
			}
		}
		util.ParallelizeUntil(&stop, 32, len(nodesListCopy), checkNode)
		if stop {
			klog.InfoS("Better preemption policy check priority for pod", "pod", podutil.GeneratePodKey(pod), "policy", config.BetterPreemptionPolicyAscending, "priority", priority, "duration", time.Since(start))
			return selectedNodeIndex, firstPriorityIndex
		}
		previousPriority = int64(priority)
		klog.InfoS("Better preemption policy check priority for pod", "pod", podutil.GeneratePodKey(pod), "policy", config.BetterPreemptionPolicyAscending, "priority", priority, "duration", time.Since(start))
	}
	return selectedNodeIndex, firstPriorityIndex
}

// int, nodeIndex
// int, maxVictimPriorityIndex
func (gs *podScheduler) dichotomyPreemption(ctx context.Context,
	state *framework.CycleState, pod *v1.Pod, resourceType podutil.PodResourceType,
	prioritiesSlice []int, nodesList []framework.NodeInfo, expectedCount int,
	fw framework.SchedulerFramework, pfw framework.SchedulerPreemptionFramework,
) ([]int, int) {
	var (
		lock                        sync.Mutex
		selectedNodeIndexSlice      [][]int
		firstMaxVictimPriorityIndex = -1

		preemptionStates          = make([]*framework.CycleState, len(nodesList))
		states                    = make([]*framework.CycleState, len(nodesList))
		nodesListCopy             = make([]framework.NodeInfo, len(nodesList))
		skipPluginsForNodes       = make([][]string, len(nodesList))
		previousPriority    int64 = math.MinInt64
	)
	copy(nodesListCopy, nodesList)
	partitionPriority := preemptionplugins.GetPodPartitionPriority(pod)
	podResource, _, _ := framework.CalculateResource(pod)
	left := 0
	right := len(prioritiesSlice) - 1
	for left <= right {
		start := time.Now()
		var selectedNodeIndex []int
		failedNodes := make([]bool, len(nodesListCopy))
		mid := (left + right) / 2
		priority := prioritiesSlice[mid]
		passed := false

		checkNode := func(i int) {
			nodeInfo := nodesListCopy[i]
			if nodeInfo == nil {
				return
			}

			// Perform heuristic checks to terminate the preemption process early.
			// Only need to perform in the first round
			if states[i] == nil && !preemption.OccupiableResourcesCheck(partitionPriority, resourceType, podResource, nodeInfo) {
				nodesListCopy[i] = nil
				return
			}

			preemptionState := preemptionStates[i].Clone()
			checked := preemptionState != nil
			if !checked {
				preemptionState = framework.NewCycleState()
			}
			potentialVictims := preemption.FilterVictimsPods(gs, pfw, state, preemptionState, nodeInfo, pod, previousPriority, int64(priority+1), checked)

			if states[i] == nil {
				states[i] = state
			}
			stateCopy := states[i]
			nodeInfoCopy := nodeInfo
			if len(potentialVictims) > 0 {
				nodeInfoCopy = nodeInfo.Clone()
				stateCopy = states[i].Clone()
			}
			for _, rp := range potentialVictims {
				if err := removePod(ctx, stateCopy, pod, rp, nodeInfoCopy, fw); err != nil {
					klog.ErrorS(err, "Failed to remove potential victim", "pod", podutil.GeneratePodKey(pod), "potentialVictim", podutil.GeneratePodKey(rp), "node", nodeInfo.GetNodeName())
				}
			}

			if fits, _, statusMap, _ := frameworkruntime.PodPassesFiltersOnNode(ctx, fw, stateCopy, pod, nodeInfoCopy); fits {
				lock.Lock()
				selectedNodeIndex = append(selectedNodeIndex, i)
				if len(selectedNodeIndex) >= expectedCount {
					passed = true
				}
				firstMaxVictimPriorityIndex = mid
				lock.Unlock()
			} else {
				preemptionStates[i] = preemptionState
				states[i] = stateCopy
				nodesListCopy[i] = nodeInfoCopy
				failedNodes[i] = true

				// The pod needs to be placed by preemption. In this case, skip the partial FilterPlugins if possible.
				for name, status := range statusMap {
					if status.IsSuccess() && pluginsSkipCheckingDuringPreemption.Has(name) {
						skipPluginsForNodes[i] = append(skipPluginsForNodes[i], name)
					}
				}
			}
		}
		util.ParallelizeUntil(&passed, 32, len(nodesListCopy), checkNode)
		if len(selectedNodeIndex) > 0 {
			for i := range nodesListCopy {
				if failedNodes[i] {
					nodesListCopy[i] = nil
					skipPluginsForNodes[i] = nil
				}
			}
			right = mid - 1
			selectedNodeIndexSlice = append(selectedNodeIndexSlice, selectedNodeIndex)
		} else {
			left = mid + 1
			previousPriority = int64(priority)
		}
		klog.InfoS("Better preemption policy check priority for pod", "pod", podutil.GeneratePodKey(pod), "policy", config.BetterPreemptionPolicyDichotomy, "priority", priority, "duration", time.Since(start))
	}
	var selectedNodeIndexOrder []int
	selectedNodeIndexSet := sets.NewInt()
	length := len(selectedNodeIndexSlice)
	for i := length - 1; i >= 0; i-- {
		selectedNodeIndex := selectedNodeIndexSlice[i]
		for _, index := range selectedNodeIndex {
			if selectedNodeIndexSet.Has(index) {
				continue
			}
			selectedNodeIndexSet.Insert(index)
			selectedNodeIndexOrder = append(selectedNodeIndexOrder, index)
			if len(selectedNodeIndexOrder) >= expectedCount {
				break
			}
		}
		if len(selectedNodeIndexOrder) >= expectedCount {
			break
		}
	}
	return selectedNodeIndexOrder, firstMaxVictimPriorityIndex
}

// SelectCandidate chooses the best-fit candidate from given <candidates> and return it.
func (gs *podScheduler) SelectCandidate(
	ctx context.Context, f framework.SchedulerFramework,
	pf framework.SchedulerPreemptionFramework,
	state *framework.CycleState, pod *v1.Pod,
	candidates []*framework.Candidate, candidate *framework.Candidate,
	cachedNominatedNodes *framework.CachedNominatedNodes,
	usedCachedNominatedNodes bool,
) *framework.Candidate {
	if !usedCachedNominatedNodes {
		candidates = pf.RunCandidatesSortingPlugins(candidates, nil)
		return gs.selectCandidate(candidates, cachedNominatedNodes)
	}

	if candidate != nil {
		candidates = pf.RunCandidatesSortingPlugins(candidates, candidate)
		if candidates[0].Name == candidate.Name {
			return gs.selectCandidate(candidates, cachedNominatedNodes)
		} else if candidates[len(candidates)-1].Name == candidate.Name {
			candidates = candidates[:len(candidates)-1]
		}
	}

	lastCandidate := candidates[len(candidates)-1]
	time := 0
	allCandidatesCount := len(candidates)
	for len(candidates) > 0 && time < allCandidatesCount {
		time++
		if len(candidates) == 1 {
			c := gs.preemptOnNode(ctx, f, pf, state, pod, candidates[0].Name)
			if c == nil {
				break
			}
			candidates = pf.RunCandidatesSortingPlugins([]*framework.Candidate{lastCandidate}, c)
			// can not confirm c is the best one
			if reflect.DeepEqual(candidates[0], c) {
				candidates = candidates[:1]
				return gs.selectCandidate(candidates, cachedNominatedNodes)
			}
			break
		}

		found := false
		for i, candidate := range candidates {
			c := gs.preemptOnNode(ctx, f, pf, state, pod, candidate.Name)
			if c == nil {
				continue
			}
			candidates[i] = c
			candidates = candidates[i:]
			found = true
			break
		}
		if !found {
			break
		}

		if len(candidates) == 1 {
			continue
		}
		checkedNode := candidates[0]
		candidates = pf.RunCandidatesSortingPlugins(candidates[1:], checkedNode)
		if candidates[0].Name == checkedNode.Name {
			return gs.selectCandidate(candidates, cachedNominatedNodes)
		} else if candidates[len(candidates)-1].Name == checkedNode.Name {
			candidates = candidates[:len(candidates)-1]
		}
	}
	cachedNominatedNodes.SetUsedNominatedNode(nil)
	cachedNominatedNodes.SetUnusedNominatedNodes(nil)
	return nil
}

func (gs *podScheduler) selectCandidate(
	candidates []*framework.Candidate,
	cachedNominatedNodes *framework.CachedNominatedNodes,
) *framework.Candidate {
	selectedCandidate := candidates[0]

	if cachedNominatedNodes != nil {
		candidates = candidates[1:]
		cachedNominatedNodes.RemoveOnePod()
		if cachedNominatedNodes.GetPodCount() < len(candidates) {
			candidates = candidates[:cachedNominatedNodes.GetPodCount()]
		}
		if len(candidates) > 0 {
			cachedNominatedNodes.SetUsedNominatedNode(selectedCandidate)
			cachedNominatedNodes.SetUnusedNominatedNodes(candidates)
		} else {
			cachedNominatedNodes.SetUsedNominatedNode(nil)
			cachedNominatedNodes.SetUnusedNominatedNodes(nil)
		}
	}

	return selectedCandidate
}
