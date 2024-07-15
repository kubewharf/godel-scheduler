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
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	schedulingv1listers "k8s.io/client-go/listers/scheduling/v1"
	"k8s.io/klog/v2"
	utiltrace "k8s.io/utils/trace"

	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/api/config"
	schedulerconfig "github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/isolatedcache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/core"
	schedulerframework "github.com/kubewharf/godel-scheduler/pkg/scheduler/framework"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/runtime"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/util"
	schedulerutil "github.com/kubewharf/godel-scheduler/pkg/scheduler/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/constraints"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	"github.com/kubewharf/godel-scheduler/pkg/util/parallelize"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

// podScheduler is the component managing cache such as node and pod info, and other configs sharing the same life cycle with scheduler
type podScheduler struct {
	switchType         framework.SwitchType
	subCluster         string
	isolatedCache      isolatedcache.IsolatedCache
	clientSet          clientset.Interface
	crdClient          godelclient.Interface
	informerFactory    informers.SharedInformerFactory
	crdInformerFactory crdinformers.SharedInformerFactory
	pvcLister          corelisters.PersistentVolumeClaimLister
	podLister          corelisters.PodLister
	pcLister           schedulingv1listers.PriorityClassLister

	snapshot *cache.Snapshot

	disablePreemption                 bool
	candidateSelectPolicy             string
	betterSelectPolicies              []string
	percentageOfNodesToScore          int32
	increasedPercentageOfNodesToScore int32

	// pluginRegistry is the collection of all enabled plugins
	pluginRegistry framework.PluginMap
	// pluginOrder is the ordered list of all filter plugins
	pluginOrder framework.PluginOrder

	// preemptionPluginRegistry is the collection of all enabled preemption plugins
	preemptionPluginRegistry framework.PluginMap
	// basePlugins is the collection of all plugins supposed to run when a pod is scheduled
	// basePlugins are supposed to run always
	basePlugins framework.PluginCollectionSet

	metricsRecorder *runtime.MetricsRecorder

	schedulerName string

	schedulerFramework framework.SchedulerFramework

	schedulerPreemptionFramework framework.SchedulerPreemptionFramework

	betterSelectPoliciesRegistry map[string]betterSelectPolicy
}

// node groups
// bin-packing-first
// pod anti-affinity
func (gs *podScheduler) GetCachedNodesAndSchedule(
	ctx context.Context,
	f framework.SchedulerFramework,
	state *framework.CycleState,
	pod *v1.Pod,
	podOwner string,
	nodeGroup string,
) (podScheduleResult core.PodScheduleResult, err error) {
	// some filter plugins depend on the results of prefilter, so we need to run PreFilter plugins here
	// TODO: figure out if we can decouple filter plugins from prefilter
	// TODO: or at least we can run prefilter plugins based on configured filter plugins ?
	s := f.RunPreFilterPlugins(ctx, state, pod)
	if !s.IsSuccess() {
		return core.PodScheduleResult{}, fmt.Errorf("RunPreFilterPlugins faied, error: %+v", s.AsError())
	}

	// Perform cache timeout cleanup inertly only if cache results will be used.
	gs.isolatedCache.CleanupCachedNodesForPodOwner()

	for _, nodeName := range gs.isolatedCache.GetOrderedNodesForPodOwner(podOwner) {
		exist, group := gs.isolatedCache.IsNodeInCachedMap(podOwner, nodeName)
		// TODO: if node groups are not the same, continue instead of deleting cached node from cache ?
		if exist && group == nodeGroup {
			fit := false
			if nodeInfo := gs.snapshot.GetNodeInfo(nodeName); nodeInfo != nil {
				fit, _, _, _ = runtime.PodPassesFiltersOnNode(ctx, f, state, pod, nodeInfo)
			}

			if fit {
				sr := core.PodScheduleResult{
					NumberOfEvaluatedNodes: 1,
					NumberOfFeasibleNodes:  1,
				}
				sr.SuggestedHost = nodeName
				return sr, nil
			}
		}

		// node doesn't exist or doesn't fit anymore, remove it from cache
		gs.isolatedCache.DeleteNodeForPodOwner(podOwner, nodeName)
	}

	return core.PodScheduleResult{}, fmt.Errorf("can not schedule this pod based on cached nodes")
}

// ScheduleInSpecificNodeGroup tries to schedule the given pod to the node of specific node group.
// If it succeeds, it will return the name of the node.
// If it fails, it will return a FitError error with reasons.
// TODO@chenyu.jiang: find feasible nodes, feasible nodes selection
func (gs *podScheduler) ScheduleInSpecificNodeGroup(
	ctx context.Context, f framework.SchedulerFramework,
	unitState, commonPreemptionState, state *framework.CycleState, pod *v1.Pod,
	nodeGroup framework.NodeGroup,
	usr *framework.UnitSchedulingRequest,
	cachedStatusMap framework.NodeToStatusMap,
) (result core.PodScheduleResult, err error) {
	defer func() {
		// remove used cached node from statusMap
		if err != nil && len(result.SuggestedHost) > 0 {
			delete(cachedStatusMap, result.SuggestedHost)
		}
	}()

	podTrace, _ := framework.GetPodTrace(state)

	trace := utiltrace.New("Scheduling", utiltrace.Field{Key: "namespace", Value: pod.Namespace}, utiltrace.Field{Key: "name", Value: pod.Name})
	defer trace.LogIfLong(100 * time.Millisecond)

	if err := podPassesBasicChecks(pod, gs.pvcLister); err != nil {
		return core.PodScheduleResult{}, err
	}
	trace.Step("Basic checks done")

	// Preferred nodes
	{
		// Run "prefilter" plugins.
		s := f.RunPreFilterPlugins(ctx, state, pod)
		if !s.IsSuccess() {
			return core.PodScheduleResult{}, fmt.Errorf("RunPreFilterPlugins faied, error: %+v", s.AsError())
		}

		preferredNodes := nodeGroup.GetPreferredNodes()
		if l := len(preferredNodes.List()); l > 0 {
			preferTraceContext := podTrace.NewTraceContext(tracing.SchedulerSchedulePodSpan, tracing.SchedulerCheckPreferredNodesSpan)
			for i, nodeInfo := range preferredNodes.List() {
				nodeName := nodeInfo.GetNodeName()
				hooks := preferredNodes.Get(nodeName)
				nodeInfoToUse, stateToUse, preferStatus := nodeInfo, state, &framework.Status{}

				if nodeInfoToUse, stateToUse, preferStatus = hooks.PrePreferNode(ctx, unitState, stateToUse, pod, nodeInfoToUse); !preferStatus.IsSuccess() {
					msg := "Failed to run PrePreferNode on node and skip this node"
					statusErr := preferStatus.AsError()

					klog.V(4).InfoS(msg, "pod", klog.KObj(pod), "nodeName", nodeName, "err", statusErr)
					preferTraceContext.WithFields(tracing.WithMessageField(fmt.Sprintf("msg: %v, nodeName: %v, err: %v", msg, nodeName, statusErr)))
					continue
				}

				fit, status, _, _ := runtime.PodPassesFiltersOnNode(ctx, f, stateToUse, pod, nodeInfoToUse)

				// ATTENTION: status.IsSuccess() == true if and only if fit == true
				if preferStatus = hooks.PostPreferNode(ctx, unitState, state, pod, nodeInfo, status); !preferStatus.IsSuccess() {
					msg := "Failed to run PostPreferNode on node and skip this node"
					statusErr := preferStatus.AsError()

					klog.V(4).InfoS(msg, "pod", klog.KObj(pod), "nodeName", nodeName, "err", statusErr)
					preferTraceContext.WithFields(tracing.WithMessageField(fmt.Sprintf("msg: %v, nodeName: %v, err: %v", msg, nodeName, statusErr)))
					continue
				}

				if fit {
					preferTraceContext.WithFields(tracing.WithMessageField(fmt.Sprintf("evaluate %d preferred nodes and select %v in scheduling", i+1, nodeName)))
					preferTraceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))
					tracing.AsyncFinishTraceContext(preferTraceContext, time.Now())
					return core.PodScheduleResult{
						NumberOfEvaluatedNodes: i + 1,
						NumberOfFeasibleNodes:  1,
						SuggestedHost:          nodeName,
						// TODO: revisit this. Left the FilteredNodesStatuses to be nil for preferred nodes.
						FilteredNodesStatuses: nil,
					}, nil
				}
			}
			preferTraceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
			preferTraceContext.WithFields(tracing.WithMessageField(fmt.Sprintf("evaluate %d preferred nodes and find 0 feasible node in scheduling", l)))
			tracing.AsyncFinishTraceContext(preferTraceContext, time.Now())
		}
	}

	podOwner := podutil.GetPodOwner(pod)
	{
		// TODO: revisit this.
		// Keep the position of the PodOwnerCache unchanged.
		if len(podOwner) > 0 {
			cacheNodesTraceContext := podTrace.NewTraceContext(tracing.SchedulerSchedulePodSpan, tracing.SchedulerGetCachedNodesSpan)
			cacheNodesTraceContext.WithFields(tracing.WithPodOwnerField(podOwner))

			result, err = gs.GetCachedNodesAndSchedule(ctx, f, state, pod, podOwner, nodeGroup.GetKey())
			defer tracing.AsyncFinishTraceContext(cacheNodesTraceContext, time.Now())

			if err == nil {
				cacheNodesTraceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))

				klog.V(4).InfoS("Pod was scheduled based on cached nodes [cache hit]", "pod", klog.KObj(pod))
				return result, nil
			} else {
				cacheNodesTraceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
				cacheNodesTraceContext.WithFields(tracing.WithErrorField(err))

				klog.V(4).InfoS("Fell back to normal scheduling process as cached nodes were empty or not available any more [cache miss]", "pod", klog.KObj(pod), "err", err)
			}
		}
	}

	scheduleInSpecificNodeCircle := func(nodeCircle framework.NodeCircle) (result core.PodScheduleResult, err error) {
		startPredicateEvalTime := time.Now()
		predicateTraceContext := podTrace.NewTraceContext(tracing.SchedulerSchedulePodSpan, tracing.SchedulerFilterSpan)
		predicateTraceContext.WithFields(tracing.WithNodeCircleKey(nodeCircle.GetKey()))

		feasibleNodes, filteredNodesStatuses, err := gs.findNodesThatFitPod(ctx, f, state, pod, nodeCircle, usr, cachedStatusMap)
		defer tracing.AsyncFinishTraceContext(predicateTraceContext, time.Now())
		predicateTraceContext.WithFields(tracing.WithMessageField(fmt.Sprintf("evaluate %d nodes, find %d feasible nodes", len(feasibleNodes)+len(filteredNodesStatuses), len(feasibleNodes))))
		if err != nil {
			predicateTraceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
			predicateTraceContext.WithFields(tracing.WithErrorField(err))
			return result, err
		}
		trace.Step("Computing predicates done")

		if len(feasibleNodes) == 0 {
			predicateTraceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))

			nodes := nodeCircle.List()
			// In the case where no node can pass through the filter, in addition to returning a fit error, a result must also be set.
			// TODO: Cleanup the FitError related logic.
			result.FilteredNodesStatuses = filteredNodesStatuses
			return result, &framework.FitError{
				Pod:                   pod,
				NumAllNodes:           len(nodes),
				FilteredNodesStatuses: filteredNodesStatuses,
			}
		}

		predicateTraceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))
		// TODO(lintong.jiang): LOG - revisit the log verbosity
		klog.V(4).InfoS(fmt.Sprintf("Dumped predicate cost: %v", helper.SinceInSeconds(startPredicateEvalTime)), "pod", klog.KObj(pod))

		startPriorityEvalTime := time.Now()
		// When only one node after predicate, just use it.
		if len(feasibleNodes) == 1 {
			return core.PodScheduleResult{
				SuggestedHost:          feasibleNodes[0].GetNodeName(),
				NumberOfEvaluatedNodes: 1 + len(filteredNodesStatuses),
				NumberOfFeasibleNodes:  1,
				FilteredNodesStatuses:  filteredNodesStatuses,
			}, nil
		}

		prioritizeTraceContext := podTrace.NewTraceContext(tracing.SchedulerSchedulePodSpan, tracing.SchedulerPrioritizeSpan)
		priorityList, err := gs.prioritizeNodes(ctx, f, state, pod, feasibleNodes)
		defer tracing.AsyncFinishTraceContext(prioritizeTraceContext, time.Now())

		if err != nil {
			prioritizeTraceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
			predicateTraceContext.WithFields(tracing.WithErrorField(err))
			return result, err
		}

		// TODO(lintong.jiang): LOG - revisit the log verbosity
		klog.V(4).InfoS(fmt.Sprintf("Dumped priority cost: %v", helper.SinceInSeconds(startPriorityEvalTime)), "pod", klog.KObj(pod))

		selectedNode, err := gs.selectHostAndCacheResults(priorityList, pod, podOwner, nodeCircle.GetKey(), usr)
		trace.Step("Prioritizing done")
		if err != nil {
			prioritizeTraceContext.WithTags(tracing.WithResultTag(tracing.ResultFailure))
			predicateTraceContext.WithFields(tracing.WithErrorField(err))
			return result, err
		}

		prioritizeTraceContext.WithTags(tracing.WithResultTag(tracing.ResultSuccess))
		// TODO: we use disablePreemption field to control whether to use prepare node plugins, but they are too scattered and need to be concentrated
		return core.PodScheduleResult{
			SuggestedHost:          selectedNode,
			NumberOfEvaluatedNodes: len(feasibleNodes) + len(filteredNodesStatuses),
			NumberOfFeasibleNodes:  len(feasibleNodes),
			FilteredNodesStatuses:  filteredNodesStatuses,
		}, nil
	}

	nodeCircles := nodeGroup.GetNodeCircles()
	for _, nodeCircle := range nodeCircles {
		result, err = scheduleInSpecificNodeCircle(nodeCircle)
		// Update NodeToStatusMap by template.
		cachedStatusMap.Update(result.FilteredNodesStatuses)
		if len(result.SuggestedHost) > 0 {
			break // should return
		}
	}
	return result, err
}

func (gs *podScheduler) Close() {
	gs.metricsRecorder.Close()
}

func podPassesBasicChecks(pod *v1.Pod, pvcLister corelisters.PersistentVolumeClaimLister) error {
	// Check PVCs used by the pod
	namespace := pod.Namespace
	manifest := &(pod.Spec)
	for i := range manifest.Volumes {
		volume := &manifest.Volumes[i]
		var pvcName string
		switch {
		case volume.PersistentVolumeClaim != nil:
			pvcName = volume.PersistentVolumeClaim.ClaimName
		default:
			// Volume is not using a PVC, ignore
			continue
		}
		pvc, err := pvcLister.PersistentVolumeClaims(namespace).Get(pvcName)
		if err != nil {
			// The error has already enough context ("persistentvolumeclaim "myclaim" not found")
			return err
		}

		if pvc.DeletionTimestamp != nil {
			return fmt.Errorf("persistentvolumeclaim %q is being deleted", pvc.Name)
		}

	}

	return nil
}

// Filters the nodes to find the ones that fit the pod based on the framework
// filter plugins and filter extenders.
func (gs *podScheduler) findNodesThatFitPod(
	ctx context.Context,
	f framework.SchedulerFramework,
	state *framework.CycleState,
	pod *v1.Pod,
	nodeLister framework.NodeInfoLister,
	usr *framework.UnitSchedulingRequest,
	cachedStatusMap framework.NodeToStatusMap,
) ([]framework.NodeInfo, framework.NodeToStatusMap, error) {
	filteredNodesStatuses := make(framework.NodeToStatusMap)

	// Run "prefilter" plugins.
	s := f.RunPreFilterPlugins(ctx, state, pod)
	if !s.IsSuccess() {
		if !s.IsUnschedulable() {
			return nil, nil, s.AsError()
		}
		// All nodes will have the same status. Some non-trivial refactoring is
		// needed to avoid this copy.
		allNodes := nodeLister.List()
		for _, n := range allNodes {
			filteredNodesStatuses[n.GetNodeName()] = s
		}
		return nil, filteredNodesStatuses, nil
	}

	feasibleNodes, err := gs.findNodesThatPassFilters(ctx, f, state, pod, filteredNodesStatuses, cachedStatusMap, nodeLister, usr)
	if err != nil {
		return nil, nil, err
	}

	return feasibleNodes, filteredNodesStatuses, nil
}

// findNodesThatPassFilters finds the nodes that fit the filter plugins.
func (gs *podScheduler) findNodesThatPassFilters(
	ctx context.Context,
	f framework.SchedulerFramework,
	state *framework.CycleState,
	pod *v1.Pod, statuses, cachedStatuses framework.NodeToStatusMap,
	nodeLister framework.NodeInfoLister,
	usr *framework.UnitSchedulingRequest,
) ([]framework.NodeInfo, error) {
	beginCheckNode := time.Now()
	numberOfFeasibleNode := 0
	defer func() {
		klog.V(4).InfoS(fmt.Sprintf("Found feasible nodes with the cost %v", helper.SinceInSeconds(beginCheckNode)), "numFeasibleNodes", numberOfFeasibleNode, "pod", klog.KObj(pod))
	}()

	// always prefer to find feasible nodes from in-partition nodes,
	// if none is found and the schedulerNodePartitionType is 'Logical',
	// try to find feasible nodes from out-of-partition nodes.
	inPartitionNodes := nodeLister.InPartitionList()
	outOfPartitionNodes := nodeLister.OutOfPartitionList()

	isLongRunningTask := podutil.IsLongRunningTask(pod)

	feasibleNodes, err := gs.findFeasibleNodes(ctx, f, state, pod, statuses, cachedStatuses,
		inPartitionNodes, isLongRunningTask, usr)
	if err != nil {
		return nil, err
	}

	if len(feasibleNodes) != 0 {
		numberOfFeasibleNode = len(feasibleNodes)
		return feasibleNodes, nil
	}

	feasibleNodes, err = gs.findFeasibleNodes(ctx, f, state, pod, statuses, cachedStatuses,
		outOfPartitionNodes, isLongRunningTask, usr)
	if err != nil {
		return nil, err
	}
	numberOfFeasibleNode = len(feasibleNodes)
	return feasibleNodes, nil
}

// findNodesThatPassFilters finds the feasible nodes from nodes with filter plugins.
func (gs *podScheduler) findFeasibleNodes(
	ctx context.Context,
	f framework.SchedulerFramework,
	state *framework.CycleState,
	pod *v1.Pod,
	statuses, cachedStatuses framework.NodeToStatusMap,
	nodes []framework.NodeInfo,
	isLongRunningTask bool,
	usr *framework.UnitSchedulingRequest,
) ([]framework.NodeInfo, error) {
	size := len(nodes)
	if size == 0 {
		return nil, nil
	}
	var increasePercentageOfNodesToScore bool
	if v, ok := pod.Annotations[podutil.IncreasePercentageOfNodesToScoreAnnotationKey]; ok && v == podutil.IncreasePercentageOfNodesToScore {
		increasePercentageOfNodesToScore = true
	}
	numNodesToFind := gs.numFeasibleNodesToFind(int32(size), isLongRunningTask, usr, increasePercentageOfNodesToScore)
	if numNodesToFind == 0 {
		return nil, nil
	}
	// Create feasible list with enough space to avoid growing it
	// and allow assigning.
	feasibleNodes := make([]framework.NodeInfo, numNodesToFind)
	startNodeIndex := rand.Intn(size)

	errCh := parallelize.NewErrorChannel()
	var statusesLock sync.Mutex
	var feasibleNodesLen int32
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	checkNode := func(i int) {
		// We check the nodes starting from where we left off in the previous scheduling cycle,
		// this is to make sure all nodes have the same chance of being examined across pods.
		nodeInfo := nodes[(startNodeIndex+i)%size]

		var fit bool
		var status *framework.Status

		// TODO: revisit this.
		// ATTENTION: Read only without modifying the original cachedStatuses.
		if cachedStatus, ok := cachedStatuses[nodeInfo.GetNodeName()]; ok && cachedStatus.IsSuccess() {
			fit = true
			// Keep the status to nil, we will delete this item when finish the scheduling phase.
		} else {
			var err error
			fit, status, _, err = runtime.PodPassesFiltersOnNode(ctx, f, state, pod, nodeInfo)
			if err != nil {
				klog.ErrorS(err, "error occurred in PodPassesFiltersOnNode", "pod", klog.KObj(pod), "node", nodeInfo.GetNodeName())
				errCh.SendErrorWithCancel(err, cancel)
				return
			}
		}

		if fit {
			length := atomic.AddInt32(&feasibleNodesLen, 1)
			if length > numNodesToFind {
				cancel()
				atomic.AddInt32(&feasibleNodesLen, -1)
			} else {
				feasibleNodes[length-1] = nodeInfo
			}
		} else {
			statusesLock.Lock()
			if !status.IsSuccess() {
				statuses[nodeInfo.GetNodeName()] = status
			}
			statusesLock.Unlock()
		}
	}

	// Stops searching for more nodes once the configured number of feasible nodes
	// are found.
	parallelize.Until(ctx, size, checkNode)

	feasibleNodes = feasibleNodes[:feasibleNodesLen]
	if err := errCh.ReceiveError(); err != nil {
		klog.ErrorS(err, "Failed to get feasible nodes", "pod", klog.KObj(pod))
		return nil, err
	}

	return feasibleNodes, nil
}

func getNumberOfFeasibleNodesToFind(percentage int32, numAllNodes int32) int32 {
	if percentage >= 100 {
		return numAllNodes
	}
	expectedNodeCount := numAllNodes * percentage / 100
	if expectedNodeCount == 0 {
		return 1
	}
	return expectedNodeCount
}

// numFeasibleNodesToFind returns the number of feasible nodes that once found, the scheduler stops
// its search for more feasible nodes.
func (gs *podScheduler) numFeasibleNodesToFind(numAllNodes int32, longRunningTask bool, usr *framework.UnitSchedulingRequest, increasePercentageOfNodesToScore bool) (numNodes int32) {
	if increasePercentageOfNodesToScore {
		if gs.increasedPercentageOfNodesToScore != schedulerconfig.DefaultIncreasedPercentageOfNodesToScore {
			return getNumberOfFeasibleNodesToFind(gs.increasedPercentageOfNodesToScore, numAllNodes)
		}
	}
	if gs.percentageOfNodesToScore != schedulerconfig.DefaultPercentageOfNodesToScore {
		return getNumberOfFeasibleNodesToFind(gs.percentageOfNodesToScore, numAllNodes)
	}

	var expectedNodeCount int32

	if (usr.AllMember > 1 && !usr.EverScheduled) || longRunningTask {
		// magic number here, revisit this if necessary
		expectedNodeCount = int32(usr.AllMember + 50)
	} else {
		// magic number too, revisit this later
		expectedNodeCount = int32(usr.AllMember + 10)
	}

	if expectedNodeCount > numAllNodes {
		expectedNodeCount = numAllNodes
	}

	return expectedNodeCount
}

// prioritizeNodes prioritizes the nodes by running the score plugins,
// which return a score for each node from the call to RunScorePlugins().
// The scores from each plugin are added together to make the score for that node, then
// any extenders are run as well.
// All scores are finally combined (added) to get the total weighted scores of all nodes
func (gs *podScheduler) prioritizeNodes(ctx context.Context,
	f framework.SchedulerFramework,
	state *framework.CycleState,
	pod *v1.Pod,
	nodes []framework.NodeInfo,
) (framework.NodeScoreList, error) {
	// Run PreScore plugins.
	preScoreStatus := f.RunPreScorePlugins(ctx, state, pod, nodes)
	if !preScoreStatus.IsSuccess() {
		return nil, preScoreStatus.AsError()
	}

	size := len(nodes)
	nodeNames := make([]string, size)
	for i, node := range nodes {
		nodeNames[i] = node.GetNodeName()
	}

	// Run the Score plugins.
	scoresMap, scoreStatus := f.RunScorePlugins(ctx, state, pod, nodeNames)
	if !scoreStatus.IsSuccess() {
		return nil, scoreStatus.AsError()
	}

	if klogV := klog.V(6); klogV.Enabled() {
		for plugin, nodeScoreList := range scoresMap {
			klogV.InfoS(fmt.Sprintf("Dumped plugin scores for pod: %v", nodeScoreList), "pluginName", plugin, "pod", klog.KObj(pod))
		}
	}

	// Summarize all scores.
	result := make(framework.NodeScoreList, size)

	for i := range nodeNames {
		result[i] = framework.NodeScore{Name: nodeNames[i], Score: 0}
		for j := range scoresMap {
			result[i].Score += scoresMap[j][i].Score
		}
	}

	if klogV := klog.V(6); klogV.Enabled() {
		for i := range result {
			klogV.InfoS(fmt.Sprintf("Dumped node score %d in the result", result[i].Score), "node", result[i].Name)
		}
	}
	return result, nil
}

func (gs *podScheduler) SwitchType() framework.SwitchType {
	return gs.switchType
}

func (gs *podScheduler) SubCluster() string {
	return gs.subCluster
}

func (gs *podScheduler) SchedulerName() string {
	return gs.schedulerName
}

func (gs *podScheduler) SnapshotSharedLister() framework.SharedLister {
	return gs.snapshot
}

func (gs *podScheduler) ClientSet() clientset.Interface {
	return gs.clientSet
}

func (gs *podScheduler) SharedInformerFactory() informers.SharedInformerFactory {
	return gs.informerFactory
}

func (gs *podScheduler) CRDSharedInformerFactory() crdinformers.SharedInformerFactory {
	return gs.crdInformerFactory
}

func (gs *podScheduler) FindStore(storeName commonstore.StoreName) commonstore.Store {
	return gs.snapshot.FindStore(storeName)
}

func needCacheNodesForPod(pod *v1.Pod, podOwner string) bool {
	// TODO: figure out whether pod group affinity && bin-packing-first will affect the checking logic
	if len(podOwner) == 0 {
		return false
	}
	var cacheNodes bool // default value: false
	if pod.Spec.Affinity != nil && pod.Spec.Affinity.PodAffinity != nil {
		// TODO: maybe there are more cases that we don't want to cache nodes
		cacheNodes = false
	} else {
		cacheNodes = true
	}
	return cacheNodes
}

func (gs *podScheduler) CacheNodesForPodOwner(
	podOwner string,
	minHeap *schedulerutil.MinHeap,
	selectedNode string,
	nodeGroup string,
) {
	// TODO: drop those nodes whose score is much less than the "selected node"
	// e.g. if heap cached node.score * 2 < selected node.score, do not return it
	nodeCount := minHeap.GetSize()
	if nodeCount == 1 {
		// if only one node here in the heap, it is the selected one, return directly
		return
	}
	nodeSlice := make([]string, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		nodeScore, err := minHeap.SiftDown()
		// if this pod doesn't ask for bin-packing-first, don't cache the selected node
		if err != nil || nodeScore.Name == selectedNode {
			continue
		}
		nodeSlice = append(nodeSlice, nodeScore.Name)
	}

	gs.isolatedCache.CacheAscendingOrderNodesForPodOwner(podOwner, nodeGroup, nodeSlice)
}

// selectHost takes a prioritized list of nodes and then picks one
// in a reservoir sampling manner from the nodes that had the highest score.
func (gs *podScheduler) selectHostAndCacheResults(
	nodeScoreList framework.NodeScoreList,
	pod *v1.Pod,
	podOwner string,
	nodeGroupName string,
	usr *framework.UnitSchedulingRequest,
) (string, error) {
	if len(nodeScoreList) == 0 {
		return "", fmt.Errorf("empty priorityList")
	}

	// TODO: if this pod asks for bin-packing-first, heap operations are not necessary
	needCacheNodes := needCacheNodesForPod(pod, podOwner)
	var NumberOfNodesToBeCache int
	if usr.AllMember > 1 || usr.EverScheduled {
		NumberOfNodesToBeCache = usr.AllMember
	} else {
		// TODO: magic number here, revisit later
		NumberOfNodesToBeCache = 30
	}

	minHeap := schedulerutil.NewMinHeap()

	maxScore := nodeScoreList[0].Score
	selected := nodeScoreList[0].Name
	if needCacheNodes {
		minHeap.SiftUp(nodeScoreList[0])
	}
	cntOfMaxScore := 1
	for i, ns := range nodeScoreList[1:] {
		if ns.Score > maxScore {
			maxScore = ns.Score
			selected = ns.Name
			cntOfMaxScore = 1
		} else if ns.Score == maxScore {
			cntOfMaxScore++
			if rand.Intn(cntOfMaxScore) == 0 {
				// Replace the candidate with probability of 1/cntOfMaxScore
				selected = ns.Name
			}
		}
		if needCacheNodes {
			// since this loop starts from nodeScoreList[1], so, actually here ns == nodeScoreList[i+1]
			if i < NumberOfNodesToBeCache {
				minHeap.SiftUp(ns)
			} else {
				minVal, err := minHeap.GetMinVal()
				if err == nil && ns.Score > minVal.Score {
					minHeap.Replace(ns)
				}
			}
		}
	}

	// cache scheduling result
	if needCacheNodes {
		gs.CacheNodesForPodOwner(podOwner, minHeap, selected, nodeGroupName)
	}

	return selected, nil
}

// getBasePluginsForPod lists the default plugins for each pod
func (gs *podScheduler) getBasePluginsForPod(pod *v1.Pod) *framework.PluginCollection {
	podLauncher, err := podutil.GetPodLauncher(pod)
	if err != nil {
		return nil
	}
	return gs.basePlugins[string(podLauncher)]
}

// retrievePluginsFromPodConstraints provides constraints to run for each pod.
// Try to use constraints provided by the pod. If pod does not specify any constraints, use default ones from defaultConstraints
func (gs *podScheduler) retrievePluginsFromPodConstraints(pod *v1.Pod, constraintAnnotationKey string) (*framework.PluginCollection, error) {
	podConstraints, err := config.GetConstraints(pod, constraintAnnotationKey)
	if err != nil {
		return nil, err
	}
	size := len(podConstraints)
	specs := make([]*framework.PluginSpec, size)
	for index, constraint := range podConstraints {
		specs[index] = framework.NewPluginSpecWithWeight(constraint.PluginName, constraint.Weight)
	}
	switch constraintAnnotationKey {
	case constraints.HardConstraintsAnnotationKey:
		return &framework.PluginCollection{
			Filters: specs,
		}, nil
	case constraints.SoftConstraintsAnnotationKey:
		return &framework.PluginCollection{
			Scores: specs,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported constraintType %v", constraintAnnotationKey)
	}
}

func (gs *podScheduler) GetFrameworkForPod(pod *v1.Pod) (framework.SchedulerFramework, error) {
	podKey := podutil.GetPodKey(pod)
	hardConstraints, err := gs.retrievePluginsFromPodConstraints(pod, constraints.HardConstraintsAnnotationKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get hard constraints for pod %v: %v", podKey, err)
	}
	softConstraints, err := gs.retrievePluginsFromPodConstraints(pod, constraints.SoftConstraintsAnnotationKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get soft constraints for pod %v: %v", podKey, err)
	}
	return runtime.NewPodFramework(gs.pluginRegistry, gs.pluginOrder, gs.getBasePluginsForPod(pod), hardConstraints, softConstraints, gs.metricsRecorder)
}

func (gs *podScheduler) DisablePreemption() bool {
	return gs.disablePreemption
}

func (gs *podScheduler) GetPreemptionFrameworkForPod(pod *v1.Pod) framework.SchedulerPreemptionFramework {
	return runtime.NewPreemptionFramework(gs.preemptionPluginRegistry, gs.getBasePluginsForPod(pod))
}

func (gs *podScheduler) SetPreemptionFrameworkForPod(pf framework.SchedulerPreemptionFramework) {
	gs.schedulerPreemptionFramework = pf
}

func (gs *podScheduler) SetPotentialVictims(node string, potentialVictims []string) {
	gs.schedulerFramework.SetPotentialVictims(node, potentialVictims)
}

func (gs *podScheduler) GetPotentialVictims(node string) []string {
	return gs.schedulerFramework.GetPotentialVictims(node)
}

func (gs *podScheduler) SetFrameworkForPod(f framework.SchedulerFramework) {
	gs.schedulerFramework = f
}

func (gs *podScheduler) GetPreemptionPolicy(deployName string) string {
	return gs.isolatedCache.GetPreemptionPolicy(deployName)
}

func (gs *podScheduler) CachePreemptionPolicy(deployName, policyName string) {
	gs.isolatedCache.CachePreemptionPolicy(deployName, policyName)
}

func (gs *podScheduler) CleanupPreemptionPolicyForPodOwner() {
	gs.isolatedCache.CleanupPreemptionPolicyForPodOwner()
}

func (gs *podScheduler) GetBetterSelectPolicies() []string {
	return gs.betterSelectPolicies
}

func (gs *podScheduler) GetCandidateSelectPolicy() string {
	return gs.candidateSelectPolicy
}

func NewPodScheduler(
	schedulerName string,
	switchType framework.SwitchType,
	subCluster string,
	clientSet clientset.Interface,
	crdClient godelclient.Interface,
	informerFactory informers.SharedInformerFactory,
	crdInformerFactory crdinformers.SharedInformerFactory,
	snapshot *cache.Snapshot,
	clock clock.Clock,
	disablePreemption bool,
	candidateSelectPolicy string,
	betterSelectPolicies []string,
	percentageOfNodesToScore int32,
	increasedPercentageOfNodesToScore int32,
	basePlugins framework.PluginCollectionSet,
	pluginArgs map[string]*schedulerconfig.PluginConfig,
	preemptionPluginArgs map[string]*schedulerconfig.PluginConfig,
) core.PodScheduler {
	gs := &podScheduler{
		schedulerName:                     schedulerName,
		switchType:                        switchType,
		subCluster:                        subCluster,
		isolatedCache:                     isolatedcache.NewIsolatedCache(),
		clientSet:                         clientSet,
		crdClient:                         crdClient,
		informerFactory:                   informerFactory,
		crdInformerFactory:                crdInformerFactory,
		snapshot:                          snapshot,
		disablePreemption:                 disablePreemption,
		percentageOfNodesToScore:          percentageOfNodesToScore,
		increasedPercentageOfNodesToScore: increasedPercentageOfNodesToScore,
		basePlugins:                       basePlugins,
		metricsRecorder:                   runtime.NewMetricsRecorder(1000, time.Second, switchType, subCluster, schedulerName),
		candidateSelectPolicy:             candidateSelectPolicy,
		betterSelectPolicies:              betterSelectPolicies,
	}
	pluginRegistry, err := schedulerframework.NewPluginsRegistry(schedulerframework.NewInTreeRegistry(), pluginArgs, gs)
	if err != nil {
		klog.ErrorS(err, "Failed to initialize PodScheduler", "schedulerName", schedulerName, "subCluster", subCluster, "switchType", switchType, "pluginArgs", pluginArgs)
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	if pluginRegistry == nil {
		klog.ErrorS(nil, "Failed to initialize PodScheduler since pluginRegistry is not defined", "schedulerName", schedulerName, "subCluster", subCluster, "switchType", switchType, "pluginArgs", pluginArgs)
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	gs.pluginRegistry = pluginRegistry

	preemptionPluginRegistry, err := schedulerframework.NewPluginsRegistry(schedulerframework.NewInTreePreemptionRegistry(), preemptionPluginArgs, gs)
	if err != nil {
		klog.ErrorS(err, "Failed to initialize preemption registry", "schedulerName", schedulerName, "subCluster", subCluster, "switchType", switchType, "basePlugins", basePlugins)
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	if preemptionPluginRegistry == nil {
		klog.ErrorS(err, "Failed to initialize preemption registry", "schedulerName", schedulerName, "subCluster", subCluster, "switchType", switchType, "basePlugins", basePlugins)
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	gs.preemptionPluginRegistry = preemptionPluginRegistry

	gs.podLister = informerFactory.Core().V1().Pods().Lister()
	gs.pcLister = informerFactory.Scheduling().V1().PriorityClasses().Lister()
	gs.pvcLister = informerFactory.Core().V1().PersistentVolumeClaims().Lister()
	orderedPluginRegistry := schedulerframework.NewOrderedPluginRegistry()
	gs.pluginOrder = util.GetListIndex(orderedPluginRegistry)

	gs.betterSelectPoliciesRegistry = map[string]betterSelectPolicy{
		schedulerconfig.BetterPreemptionPolicyAscending: gs.ascendingOrderPreemption,
		schedulerconfig.BetterPreemptionPolicyDichotomy: gs.dichotomyPreemption,
	}

	return gs
}
