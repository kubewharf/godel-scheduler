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

// This file defines the scheduling framework plugin interfaces.

package api

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	crdclientset "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/kubewharf/godel-scheduler/pkg/volume/scheduling"
)

// NodeScoreList declares a list of nodes and their scores.
type NodeScoreList []NodeScore

// NodeScore is a struct with node name and score.
type NodeScore struct {
	Name  string
	Score int64
}

// PluginToNodeScores declares a map from plugin name to its NodeScoreList.
type PluginToNodeScores map[string]NodeScoreList

// NodeToStatusMap declares map from node name to its status.
type NodeToStatusMap map[string]*Status

func (t NodeToStatusMap) Update(m NodeToStatusMap) {
	if len(m) == 0 {
		return
	}
	for k, v := range m {
		t[k] = v
	}
}

// NodeToStatusMapByTemplate declares map from pod template to its NodeToStatusMap.
type NodeToStatusMapByTemplate map[string]NodeToStatusMap

// Code is the Status code/type which is returned from plugins.
type Code int

// These are predefined codes used in a Status.
const (
	// Success means that plugin ran correctly and found pod schedulable.
	// NOTE: A nil status is also considered as "Success".
	Success Code = iota
	// Error is used for internal plugin errors, unexpected input, etc.
	Error
	// Unschedulable is used when a plugin finds a pod unschedulable. The scheduler might attempt to
	// preempt other pods to get this pod scheduled. Use UnschedulableAndUnresolvable to make the
	// scheduler skip preemption.
	// The accompanying status message should explain why the pod is unschedulable.
	Unschedulable
	// UnschedulableAndUnresolvable is used when a (pre-)filter plugin finds a pod unschedulable and
	// preemption would not change anything. Plugins should return Unschedulable if it is possible
	// that the pod can get scheduled with preemption.
	// The accompanying status message should explain why the pod is unschedulable.
	UnschedulableAndUnresolvable
	// Wait is used when a permit plugin finds a pod scheduling should wait.
	Wait
	// Skip is used when a bind plugin chooses to skip binding.
	Skip
	// PreemptionSuccess is used when preemption pass
	PreemptionSucceed
	// PreemptionFail is used when preemption fail
	PreemptionFail
	// PreemptionNotSure is used when preemption result is not clear
	PreemptionNotSure
)

// This list should be exactly the same as the codes iota defined above in the same order.
var codes = []string{"Success", "Error", "Unschedulable", "UnschedulableAndUnresolvable", "Wait", "Skip", "PreemptionFail", "PreemptionNotSure"}

func (c Code) String() string {
	return codes[c]
}

const (
	// MaxNodeScore is the maximum score a Score plugin is expected to return.
	MaxNodeScore int64 = 100

	// MinNodeScore is the minimum score a Score plugin is expected to return.
	MinNodeScore int64 = 0

	// MaxTotalScore is the maximum total score.
	MaxTotalScore int64 = math.MaxInt64
)

// Status indicates the result of running a plugin. It consists of a code, a
// message and (optionally) an error. When the status code is not `Success`,
// the reasons should explain why.
// NOTE: A nil Status is also considered as Success.
type Status struct {
	code    Code
	reasons []string
	err     error
}

// Code returns code of the Status.
func (s *Status) Code() Code {
	if s == nil {
		return Success
	}
	return s.code
}

// Message returns a concatenated message on reasons of the Status.
func (s *Status) Message() string {
	if s == nil {
		return ""
	}
	return strings.Join(s.reasons, ", ")
}

// Reasons returns reasons of the Status.
func (s *Status) Reasons() []string {
	if s == nil {
		return nil
	}
	return s.reasons
}

// AppendReason appends given reason to the Status.
func (s *Status) AppendReason(reason string) {
	if s == nil {
		return
	}
	s.reasons = append(s.reasons, reason)
}

// IsSuccess returns true if and only if "Status" is nil or Code is "Success".
func (s *Status) IsSuccess() bool {
	return s.Code() == Success
}

// IsUnschedulable returns true if "Status" is Unschedulable (Unschedulable or UnschedulableAndUnresolvable).
func (s *Status) IsUnschedulable() bool {
	code := s.Code()
	return code == Unschedulable || code == UnschedulableAndUnresolvable
}

// AsError returns nil if the status is a success; otherwise returns an "error" object
// with a concatenated message on reasons of the Status.
func (s *Status) AsError() error {
	if s.IsSuccess() {
		return nil
	}
	if s.err != nil {
		return s.err
	}
	return errors.New(s.Message())
}

// NewStatus makes a Status out of the given arguments and returns its pointer.
func NewStatus(code Code, reasons ...string) *Status {
	s := &Status{
		code:    code,
		reasons: reasons,
	}
	if code == Error {
		s.err = errors.New(s.Message())
	}
	return s
}

// AsStatus wraps an error in a Status.
func AsStatus(err error) *Status {
	return &Status{
		code:    Error,
		reasons: []string{err.Error()},
		err:     err,
	}
}

// Plugin is the parent type for all the scheduling framework plugins, means the atomic scheduling step, the basic component of constraints
type Plugin interface {
	Name() string
}

type Plugins []Plugin

// LessFunc is the function to sort pod info
type LessFunc func(podInfo1, podInfo2 *QueuedPodInfo) bool

// UnitLessFunc is the function to sort unit info
type UnitLessFunc func(unitInfo1, unitInfo2 *QueuedUnitInfo) bool

// QueueSortPlugin is an interface that must be implemented to get sort score for pods in the scheduling queue.
type QueueSortPlugin interface {
	Plugin
	// Less are used to sort pods in the scheduling queue.
	Less(*QueuedPodInfo, *QueuedPodInfo) bool
}

// UnitQueueSortPlugin is an interface that must be implemented by "QueueSort" plugins.
// These plugins are used to sort units in the scheduling unit queue. Only one queue sort
// plugin may be enabled at a time.
type UnitQueueSortPlugin interface {
	Plugin
	Less(*QueuedUnitInfo, *QueuedUnitInfo) bool
}

type VictimSearchingPluginCollection struct {
	forceQuickPass  bool
	enableQuickPass bool
	rejectNotSure   bool
	plugins         []VictimSearchingPlugin
}

type ClusterPrePreemptingPlugin interface {
	Plugin
	ClusterPrePreempting(*v1.Pod, *CycleState, *CycleState) *Status
}

type NodePrePreemptingPlugin interface {
	Plugin
	NodePrePreempting(*v1.Pod, NodeInfo, *CycleState, *CycleState) *Status
}

type VictimSearchingPlugin interface {
	Plugin
	VictimSearching(*v1.Pod, *PodInfo, *CycleState, *CycleState, *VictimState) (Code, string)
}

type PostVictimSearchingPlugin interface {
	Plugin
	PostVictimSearching(*v1.Pod, *PodInfo, *CycleState, *CycleState, *VictimState) *Status
}

type NodePostPreemptingPlugin interface {
	Plugin
	NodePostPreempting(*v1.Pod, []*v1.Pod, *CycleState, *CycleState) *Status
}

type CandidatesSortingPlugin interface {
	Plugin
	Compare(*Candidate, *Candidate) int
}

func NewVictimSearchingPluginCollection(plugins []VictimSearchingPlugin, enableQuickPass, forceQuickPass, rejectNotSure bool) *VictimSearchingPluginCollection {
	return &VictimSearchingPluginCollection{
		enableQuickPass: enableQuickPass,
		forceQuickPass:  forceQuickPass,
		rejectNotSure:   rejectNotSure,
		plugins:         plugins,
	}
}

func (ppc *VictimSearchingPluginCollection) GetVictimSearchingPlugins() []VictimSearchingPlugin {
	return ppc.plugins
}

func (ppc *VictimSearchingPluginCollection) ForceQuickPass() bool {
	return ppc.forceQuickPass
}

func (ppc *VictimSearchingPluginCollection) EnableQuickPass() bool {
	return ppc.enableQuickPass
}

func (ppc *VictimSearchingPluginCollection) RejectNotSure() bool {
	return ppc.rejectNotSure
}

type VictimCheckingPluginCollection struct {
	forceQuickPass  bool
	enableQuickPass bool
	rejectNotSure   bool
	plugins         []VictimCheckingPlugin
}

type VictimCheckingPlugin interface {
	Plugin
	VictimChecking(*v1.Pod, *v1.Pod, *CycleState, *CycleState) (Code, string)
}

type PostVictimCheckingPlugin interface {
	Plugin
	PostVictimChecking(*v1.Pod, *v1.Pod, *CycleState, *CycleState) *Status
}

func NewVictimCheckingPluginCollection(plugins []VictimCheckingPlugin, enableQuickPass, forceQuickPass bool) *VictimCheckingPluginCollection {
	return &VictimCheckingPluginCollection{
		enableQuickPass: enableQuickPass,
		forceQuickPass:  forceQuickPass,
		plugins:         plugins,
	}
}

func (ppc *VictimCheckingPluginCollection) GetVictimCheckingPlugins() []VictimCheckingPlugin {
	return ppc.plugins
}

func (ppc *VictimCheckingPluginCollection) ForceQuickPass() bool {
	return ppc.forceQuickPass
}

func (ppc *VictimCheckingPluginCollection) EnableQuickPass() bool {
	return ppc.enableQuickPass
}

func (ppc *VictimCheckingPluginCollection) RejectNotSure() bool {
	return ppc.rejectNotSure
}

// PreFilterExtensions is an interface that is included in plugins that allow specifying
// callbacks to make incremental updates to its supposedly pre-calculated
// state.
type PreFilterExtensions interface {
	// AddPod is called by the framework while trying to evaluate the impact
	// of adding podToAdd to the node while scheduling podToSchedule.
	AddPod(ctx context.Context, state *CycleState, podToSchedule *v1.Pod, podToAdd *v1.Pod, nodeInfo NodeInfo) *Status
	// RemovePod is called by the framework while trying to evaluate the impact
	// of removing podToRemove from the node while scheduling podToSchedule.
	RemovePod(ctx context.Context, state *CycleState, podToSchedule *v1.Pod, podToRemove *v1.Pod, nodeInfo NodeInfo) *Status
}

// PreFilterPlugin is an interface that must be implemented by "prefilter" plugins.
// These plugins are called at the beginning of the scheduling cycle.
type PreFilterPlugin interface {
	Plugin
	// PreFilter is called at the beginning of the scheduling cycle. All PreFilter
	// plugins must return success or the pod will be rejected.
	PreFilter(ctx context.Context, state *CycleState, p *v1.Pod) *Status
	// PreFilterExtensions returns a PreFilterExtensions interface if the plugin implements one,
	// or nil if it does not. A Pre-filter plugin can provide extensions to incrementally
	// modify its pre-processed info. The framework guarantees that the extensions
	// AddPod/RemovePod will only be called after PreFilter, possibly on a cloned
	// CycleState, and may call those functions more than once before calling
	// Filter again on a specific node.
	PreFilterExtensions() PreFilterExtensions
}

// PluginToStatus maps plugin name to status. Currently used to identify which Filter plugin
// returned which status.
type PluginToStatus map[string]*Status

// Merge merges the statuses in the map into one. The resulting status code have the following
// precedence: Error, UnschedulableAndUnresolvable, Unschedulable.
func (p PluginToStatus) Merge() *Status {
	if len(p) == 0 {
		return nil
	}

	finalStatus := NewStatus(Success)
	var hasUnschedulableAndUnresolvable, hasUnschedulable bool
	for _, s := range p {
		if s.Code() == Error {
			finalStatus.err = s.AsError()
		} else if s.Code() == UnschedulableAndUnresolvable {
			hasUnschedulableAndUnresolvable = true
		} else if s.Code() == Unschedulable {
			hasUnschedulable = true
		}
		finalStatus.code = s.Code()
		for _, r := range s.Reasons() {
			finalStatus.AppendReason(r)
		}
	}

	if finalStatus.err != nil {
		finalStatus.code = Error
	} else if hasUnschedulableAndUnresolvable {
		finalStatus.code = UnschedulableAndUnresolvable
	} else if hasUnschedulable {
		finalStatus.code = Unschedulable
	}
	return finalStatus
}

// FilterPlugin is an interface for Filter plugins. These plugins are called at the
// filter extension point for filtering out hosts that cannot run a pod.
// This concept used to be called 'predicate' in the original scheduler.
// These plugins should return "Success", "Unschedulable" or "Error" in Status.code.
// However, the scheduler accepts other valid codes as well.
// Anything other than "Success" will lead to exclusion of the given host from
// running the pod.
type FilterPlugin interface {
	Plugin
	// Filter is called by the scheduling framework.
	// All FilterPlugins should return "Success" to declare that
	// the given node fits the pod. If Filter doesn't return "Success",
	// please refer scheduler/algorithm/predicates/error.go
	// to set error message.
	// For the node being evaluated, Filter plugins should look at the passed
	// nodeInfo reference for this particular node's information (e.g., pods
	// considered to be running on the node) instead of looking it up in the
	// NodeInfoSnapshot because we don't guarantee that they will be the same.
	// For example, during preemption, we may pass a copy of the original
	// nodeInfo object that has some pods removed from it to evaluate the
	// possibility of preempting them to schedule the target pod.
	Filter(ctx context.Context, state *CycleState, pod *v1.Pod, nodeInfo NodeInfo) *Status
}

// PreScorePlugin is an interface for Pre-score plugin. Pre-score is an
// informational extension point. Plugins will be called with a list of nodes
// that passed the filtering phase. A plugin may use this data to update internal
// state or to generate logs/metrics.
type PreScorePlugin interface {
	Plugin
	// PreScore is called by the scheduling framework after a list of nodes
	// passed the filtering phase. All prescore plugins must return success or
	// the pod will be rejected
	PreScore(ctx context.Context, state *CycleState, pod *v1.Pod, nodes []NodeInfo) *Status
}

// ScoreExtensions is an interface for Score extended functionality.
type ScoreExtensions interface {
	// NormalizeScore is called for all node scores produced by the same plugin's "Score"
	// method. A successful run of NormalizeScore will update the scores list and return
	// a success status.
	NormalizeScore(ctx context.Context, state *CycleState, p *v1.Pod, scores NodeScoreList) *Status
}

// ScorePlugin is an interface that must be implemented by "score" plugins to rank
// nodes that passed the filtering phase.
type ScorePlugin interface {
	Plugin
	// Score is called on each filtered node. It must return success and an integer
	// indicating the rank of the node. All scoring plugins must return success or
	// the pod will be rejected.
	Score(ctx context.Context, state *CycleState, p *v1.Pod, nodeName string) (int64, *Status)

	// ScoreExtensions returns a ScoreExtensions interface if it implements one, or nil if it does not.
	ScoreExtensions() ScoreExtensions
}

type CrossNodesPlugin interface {
	Plugin
	HasCrossNodesConstraints(ctx context.Context, p *v1.Pod) bool
}

// CheckConflictsPlugin is an interface for CheckConflicts plugins. These plugins are called at the
// CheckConflicts extension point by Binder to check whether the node selected by scheduler can run the pod.
// These plugins should return "Success", "Unschedulable" or "Error" in Status.code.
// However, Binder accepts other valid codes as well.
// Anything other than "Success" will lead to exclusion of the given host from
// running the pod.
type CheckConflictsPlugin interface {
	Plugin
	// CheckConflicts is called by framework in Binder.
	// All CheckConflictsPlugin should return "Success" to declare that
	// the given node fits the pod. If CheckConflicts doesn't return "Success",
	// CheckConflictsPlugin set proper error message.
	// For the node being evaluated, CheckConflicts plugins should look at the passed
	// nodeInfo reference for this particular node's information (e.g., pods
	// considered to be running on the node) instead of looking it up in the
	// NodeInfoSnapshot because we don't guarantee that they will be the same.
	// For example, during preemption, we may pass a copy of the original
	// nodeInfo object that has some pods removed from it to evaluate the
	// possibility of preempting them to schedule the target pod.
	CheckConflicts(ctx context.Context, state *CycleState, pod *v1.Pod, nodeInfo NodeInfo) *Status
}

type CheckTopologyPlugin interface {
	Plugin
	CheckTopology(ctx context.Context, state *CycleState, pod *v1.Pod, nodeInfo NodeInfo) *Status
}

// PreBindPlugin is an interface that must be implemented by "PreBind" plugins.
// These plugins are called before a pod being scheduled.
type PreBindPlugin interface {
	Plugin
	// PreBind is called before binding a pod. All binder plugins must return
	// success or the pod will be rejected and won't be sent for binding.
	PreBind(ctx context.Context, state *CycleState, p *v1.Pod, nodeName string) *Status
}

// PostBindPlugin is an interface that must be implemented by "PostBind" plugins.
// These plugins are called after a pod is successfully bound to a node.
type PostBindPlugin interface {
	Plugin
	// PostBind is called after a pod is successfully bound. These plugins are
	// informational. A common application of this extension point is for cleaning
	// up. If a plugin needs to clean-up its state after a pod is scheduled and
	// bound, PostBind is the extension point that it should register.
	PostBind(ctx context.Context, state *CycleState, p *v1.Pod, nodeName string)
}

// BindPlugin is an interface that must be implemented by "Bind" plugins. Bind
// plugins are used to bind a pod to a Node.
type BindPlugin interface {
	Plugin
	// Bind plugins will not be called until all pre-bind plugins have completed. Each
	// bind plugin is called in the configured order. A bind plugin may choose whether
	// to handle the given Pod. If a bind plugin chooses to handle a Pod, the
	// remaining bind plugins are skipped. When a bind plugin does not handle a pod,
	// it must return Skip in its Status code. If a bind plugin returns an Error, the
	// pod is rejected and will not be bound.
	Bind(ctx context.Context, state *CycleState, p *v1.Pod, nodeName string) *Status
}

// ReservePlugin is an interface for plugins with Reserve and Unreserve
// methods. These are meant to update the state of the plugin. This concept
// used to be called 'assume' in the original scheduler. These plugins should
// return only Success or Error in Status.code. However, the scheduler accepts
// other valid codes as well. Anything other than Success will lead to
// rejection of the pod.
type ReservePlugin interface {
	Plugin
	// Reserve is called by the scheduling framework when the scheduler cache is
	// updated. If this method returns a failed Status, the scheduler will call
	// the Unreserve method for all enabled ReservePlugins.
	Reserve(ctx context.Context, state *CycleState, p *v1.Pod, nodeName string) *Status
	// Unreserve is called by the scheduling framework when a reserved pod was
	// rejected, an error occurred during reservation of subsequent plugins, or
	// in a later phase. The Unreserve method implementation must be idempotent
	// and may be called by the scheduler even if the corresponding Reserve
	// method for the same plugin was not called.
	Unreserve(ctx context.Context, state *CycleState, p *v1.Pod, nodeName string)
}

// PermitPlugin is an interface that must be implemented by "Permit" plugins.
// These plugins are called before a pod is bound to a node.
type PermitPlugin interface {
	Plugin
	// TODO: remove the second return value
	// Permit is called before binding a pod (and before binder plugins). Permit
	// plugins are used to prevent or delay the binding of a Pod. A permit plugin
	// must return success or wait with timeout duration, or the pod will be rejected.
	// The pod will also be rejected if the wait timeout or the pod is rejected while
	// waiting. Note that if the plugin returns "wait", the framework will wait only
	// after running the remaining plugins given that no other plugin rejects the pod.
	Permit(ctx context.Context, state *CycleState, p *v1.Pod, nodeName string) (*Status, time.Duration)
}

// SchedulerFramework manages the set of plugins in use by Scheduler.
// Configured plugins are called at specified points in a scheduling context.
type SchedulerFramework interface {
	// RunPreFilterPlugins runs the set of configured prefilter plugins. It returns
	// *Status and its code is set to non-success if any of the plugins returns
	// anything but Success. If a non-success status is returned, then the scheduling
	// cycle is aborted.
	RunPreFilterPlugins(ctx context.Context, state *CycleState, pod *v1.Pod) *Status

	// RunFilterPlugins runs the set of configured filter plugins for pod on
	// the given node. Note that for the node being evaluated, the passed nodeInfo
	// reference could be different from the one in NodeInfoSnapshot map (e.g., pods
	// considered to be running on the node could be different). For example, during
	// preemption, we may pass a copy of the original nodeInfo object that has some pods
	// removed from it to evaluate the possibility of preempting them to
	// schedule the target pod.
	RunFilterPlugins(ctx context.Context, state *CycleState, pod *v1.Pod, nodeInfo NodeInfo, skipPlugins ...string) PluginToStatus

	// RunPreFilterExtensionAddPod calls the AddPod interface for the set of configured
	// PreFilter plugins. It returns directly if any of the plugins return any
	// status other than Success.
	RunPreFilterExtensionAddPod(ctx context.Context, state *CycleState, podToSchedule *v1.Pod, podToAdd *v1.Pod, nodeInfo NodeInfo) *Status

	// RunPreFilterExtensionRemovePod calls the RemovePod interface for the set of configured
	// PreFilter plugins. It returns directly if any of the plugins return any
	// status other than Success.
	RunPreFilterExtensionRemovePod(ctx context.Context, state *CycleState, podToSchedule *v1.Pod, podToAdd *v1.Pod, nodeInfo NodeInfo) *Status

	// RunPreScorePlugins runs the set of configured pre-score plugins. If any
	// of these plugins returns any status other than "Success", the given pod is rejected.
	RunPreScorePlugins(ctx context.Context, state *CycleState, pod *v1.Pod, nodes []NodeInfo) *Status

	// RunScorePlugins runs the set of configured scoring plugins. It returns a map that
	// stores for each scoring plugin name the corresponding NodeScoreList(s).
	// It also returns *Status, which is set to non-success if any of the plugins returns
	// a non-success status.
	RunScorePlugins(ctx context.Context, state *CycleState, pod *v1.Pod, nodeNames []string) (PluginToNodeScores, *Status)

	// HasFilterPlugins returns true if at least one filter plugin is defined.
	HasFilterPlugins() bool

	// HasScorePlugins returns true if at least one score plugin is defined.
	HasScorePlugins() bool

	// ListPlugins returns a map of extension point name to list of configured Plugins.
	ListPlugins() map[string]sets.String

	// InitCycleState returns a new cycle state to be used in scheduling process, storing default common data from pod annotation in CycleState
	// This function is used to reduce unmarshalling operations in plugins for pod annotations.
	InitCycleState(pod *v1.Pod) (*CycleState, error)
	SetPotentialVictims(node string, potentialVictims []string)
	GetPotentialVictims(node string) []string

	HasCrossNodesConstraints(ctx context.Context, pod *v1.Pod) bool
}

type SchedulerPreemptionFramework interface {
	ListPlugins() map[string]sets.String
	HasVictimSearchingPlugin(string) bool
	RunClusterPrePreemptingPlugins(preemptor *v1.Pod, state, commonState *CycleState) *Status
	RunNodePrePreemptingPlugins(preemptor *v1.Pod, nodeInfo NodeInfo, state *CycleState, preemptorState *CycleState) *Status
	RunVictimSearchingPlugins(preemptor *v1.Pod, podInfo *PodInfo, state, preemptionState *CycleState, victimState *VictimState) (Code, string)
	RunPostVictimSearchingPlugins(preemptor *v1.Pod, podInfo *PodInfo, state, preemptionState *CycleState, victimState *VictimState) *Status
	RunNodePostPreemptingPlugins(preemptor *v1.Pod, victims []*v1.Pod, state, commonState *CycleState) *Status

	RunCandidatesSortingPlugins(candidates []*Candidate, candidate *Candidate) []*Candidate
}

// SchedulerFrameworkHandle provides data and some tools that plugins can use in Scheduler. It is
// passed to the plugin factories at the time of plugin initialization. Plugins
// must store and use this handle to call framework functions.
type SchedulerFrameworkHandle interface {
	// SwitchType indicates the cluster binary code corresponding to the current workflow.
	// It will be used to resolve BE/GT qos.
	SwitchType() SwitchType
	SubCluster() string
	SchedulerName() string

	// SnapshotSharedLister returns listers from the latest NodeInfo Snapshot. The snapshot
	// is taken at the beginning of a scheduling cycle and remains unchanged until
	// a pod finishes "Permit" point. There is no guarantee that the information
	// remains unchanged in the binding phase of scheduling, so plugins in the binding
	// cycle (pre-bind/bind/post-bind/un-reserve plugin) should not use it,
	// otherwise a concurrent read/write error might occur, they should use scheduler
	// cache instead.
	SnapshotSharedLister() SharedLister

	// ClientSet returns a kubernetes clientSet.
	ClientSet() clientset.Interface
	SharedInformerFactory() informers.SharedInformerFactory
	CRDSharedInformerFactory() crdinformers.SharedInformerFactory
	GetFrameworkForPod(*v1.Pod) (SchedulerFramework, error)

	// Note: The function's underlying access is Snapshot, Snapshot operations are lock-free.
	GetPodGroupInfo(podGroupName string) (*schedulingv1a1.PodGroup, error)
	// Note: The function's underlying access is Snapshot, Snapshot operations are lock-free.
	SetPotentialVictims(node string, potentialVictims []string)
	// Note: The function's underlying access is Snapshot, Snapshot operations are lock-free.
	GetPotentialVictims(node string) []string

	// Note: The function's underlying access is Snapshot, Snapshot operations are lock-free.
	GetPDBItemList() []PDBItem
	// Note: The function's underlying access is Snapshot, Snapshot operations are lock-free.
	GetPDBItemListForOwner(ownerType, ownerKey string) (bool, bool, []string)
	// Note: The function's underlying access is Snapshot, Snapshot operations are lock-free.
	GetOwnerLabels(ownerType, ownerKey string) map[string]string

	GetPreemptionFrameworkForPod(*v1.Pod) SchedulerPreemptionFramework
	GetPreemptionPolicy(deployName string) string
	CachePreemptionPolicy(deployName string, policyName string)
	CleanupPreemptionPolicyForPodOwner()
}

// BinderFramework manages the set of plugins in use by the scheduling framework.
// Configured plugins are called at specified points in a scheduling context.
type BinderFramework interface {
	RunCheckTopologyPlugins(ctx context.Context, state *CycleState, pod *v1.Pod, nodeInfo NodeInfo) PluginToStatus

	// RunCheckConflictsPlugins runs the set of configured CheckConflicts plugins for pod on
	// the given node. Note that for the node being evaluated, the passed nodeInfo
	// reference could be different from the one in NodeInfoSnapshot map (e.g., pods
	// considered to be running on the node could be different). For example, during
	// preemption, we may pass a copy of the original nodeInfo object that has some pods
	// removed from it to evaluate the possibility of preempting them to
	// schedule the target pod.
	RunCheckConflictsPlugins(ctx context.Context, state *CycleState, pod *v1.Pod, nodeInfo NodeInfo) PluginToStatus

	// RunPreBindPlugins runs the set of configured PreBind plugins. It returns
	// *Status and its code is set to non-success if any of the plugins returns
	// anything but Success. If the Status code is "Unschedulable", it is
	// considered as a scheduling check failure, otherwise, it is considered as an
	// internal error. In either case the pod is not going to be bound.
	RunPreBindPlugins(ctx context.Context, state *CycleState, pod *v1.Pod, nodeName string) *Status

	// RunPostBindPlugins runs the set of configured PostBind plugins.
	RunPostBindPlugins(ctx context.Context, state *CycleState, pod *v1.Pod, nodeName string)

	// RunBindPlugins runs the set of configured Bind plugins. A Bind plugin may choose
	// whether to handle the given Pod. If a Bind plugin chooses to skip the
	// binding, it should return code=5("skip") status. Otherwise, it should return "Error"
	// or "Success". If none of the plugins handled binding, RunBindPlugins returns
	// code=5("skip") status.
	RunBindPlugins(ctx context.Context, state *CycleState, pod *v1.Pod, nodeName string) *Status

	// RunReservePluginsReserve runs the Reserve method of the set of
	// configured Reserve plugins. If any of these calls returns an error, it
	// does not continue running the remaining ones and returns the error. In
	// such case, pod will not be scheduled.
	RunReservePluginsReserve(ctx context.Context, state *CycleState, pod *v1.Pod, nodeName string) *Status

	// RunReservePluginsUnreserve runs the Unreserve method of the set of
	// configured Reserve plugins.
	RunReservePluginsUnreserve(ctx context.Context, state *CycleState, pod *v1.Pod, nodeName string)

	// RunPermitPlugins runs the set of configured Permit plugins. If any of these
	// plugins returns a status other than "Success" or "Wait", it does not continue
	// running the remaining plugins and returns an error. Otherwise, if any of the
	// plugins returns "Wait", then this function will create and add waiting pod
	// to a map of currently waiting pods and return status with "Wait" code.
	// Pod will remain waiting pod for the minimum duration returned by the Permit plugins.
	RunPermitPlugins(ctx context.Context, state *CycleState, pod *v1.Pod, nodeName string) *Status

	// WaitOnPermit will block, if the pod is a waiting pod, until the waiting pod is rejected or allowed.
	WaitOnPermit(ctx context.Context, pod *v1.Pod) *Status

	// HasCheckConflictsPlugins returns true if at least one CheckConflicts plugin is defined.
	HasCheckConflictsPlugins() bool

	// HasCheckTopologyPlugins returns true if at least one CheckTopology plugin is defined.
	HasCheckTopologyPlugins() bool

	HasPlugin(pluginName string) bool

	// ListPlugins returns a map of extension point name to list of configured Plugins.
	ListPlugins() map[string]sets.String

	// InitCycleState returns a new cycle state to be used in scheduling process, storing default common data from pod annotation in CycleState
	// This function is used to reduce unmarshalling operations in plugins for pod annotations.
	InitCycleState(pod *v1.Pod) (*CycleState, error)

	RunClusterPrePreemptingPlugins(preemptor *v1.Pod, state, preemptionState *CycleState) *Status
	RunVictimCheckingPlugins(preemptor, pod *v1.Pod, state, preemptionState *CycleState) *Status
	RunPostVictimCheckingPlugins(preemptor, pod *v1.Pod, state, preemptionState *CycleState) *Status
}

// BinderFrameworkHandle provides data and some tools that plugins can use. It is
// passed to the plugin factories at the time of plugin initialization. Plugins
// must store and use this handle to call framework functions.
type BinderFrameworkHandle interface {
	GetPodGroupPods(podGroupName string) []*v1.Pod
	GetPodGroupInfo(podGroupName string) (*schedulingv1a1.PodGroup, error)

	// ClientSet returns a kubernetes clientSet.
	ClientSet() clientset.Interface
	CRDClientSet() crdclientset.Interface
	SharedInformerFactory() informers.SharedInformerFactory
	CRDSharedInformerFactory() crdinformers.SharedInformerFactory
	GetFrameworkForPod(*v1.Pod) (BinderFramework, error)
	VolumeBinder() scheduling.GodelVolumeBinder
	GetPDBItemList() []PDBItem

	GetNode(string) (NodeInfo, error)
}

// PluginsRunner abstracts operations to run some plugins.
// This is used by preemption PostFilter plugins when evaluating the feasibility of
// scheduling the pod on nodes when certain running pods get evicted.
type PluginsRunner interface {
	// RunFilterPlugins runs the set of configured filter plugins for pod on the given node.
	RunFilterPlugins(context.Context, *CycleState, *v1.Pod, NodeInfo) PluginToStatus
	// RunPreFilterExtensionAddPod calls the AddPod interface for the set of configured PreFilter plugins.
	RunPreFilterExtensionAddPod(ctx context.Context, state *CycleState, podToSchedule *v1.Pod, podToAdd *v1.Pod, nodeInfo NodeInfo) *Status
	// RunPreFilterExtensionRemovePod calls the RemovePod interface for the set of configured PreFilter plugins.
	RunPreFilterExtensionRemovePod(ctx context.Context, state *CycleState, podToSchedule *v1.Pod, podToRemove *v1.Pod, nodeInfo NodeInfo) *Status
}

// These are predefined phases used in Scheduler and Binder.
const (
	PreFilterPhase         = "PreFilter"
	FilterPhase            = "Filter"
	PreScorePhase          = "PreScore"
	ScorePhase             = "Score"
	CrossNodesCheckerPhase = "CrossNodesChecker"

	ClusterPrePreemptingPhase = "ClusterPrePreempting"
	NodePrePreemptingPhase    = "NodePrePreempting"
	VictimSearchingPhase      = "VictimSearching"
	PostVictimSearchingPhase  = "PostVictimSearching"
	NodePostPreemptingPhase   = "NodePostPreempting"

	CheckTopologyPhase = "CheckTopology"
	CheckConflictPhase = "CheckConflict"
	PermitPhase        = "Permit"
	PreBindPhase       = "PreBind"
	PostBindPhase      = "PostBind"
	BindPhase          = "Bind"
	ReservePhase       = "Reserve"
)

// ScheduleUnitType describes the type of the unit.
type ScheduleUnitType string

const (
	PodGroupUnitType  ScheduleUnitType = "PodGroupUnit"
	SinglePodUnitType ScheduleUnitType = "SinglePodUnit"
)

// ScheduleUnit is an interface that must be implemented by `podGroup` or other plugins.
// These plugins are called in the middle of the scheduling cycle.
type ScheduleUnit interface {
	// Name is schedule unit name.
	// Name() string
	// Type is the schedule unit type. e.g. PodGroupUnit, DeploymentUnit, etc
	Type() ScheduleUnitType
	// GetKey returns the key of unit. This should be unique globally.
	GetKey() string
	// TODO: Should GetName()、GetNamespace() be there?
	// GetName returns the name of unit.
	GetName() string
	// GetNamespace returns the namespace of unit.
	GetNamespace() string
	// ReadyToBePopulated is used to determine whether the unit is ready to be populated.
	// This is a precondition check to schedule corresponding pods belonging to the unit.
	ReadyToBePopulated() bool
	// PodBelongToUnit is used in event handling to determine if a pod is a naked pod, or it belongs to unit.
	// If it does belong to the unit, the pods will be put into dedicated scheduling queue.
	PodBelongToUnit(pod *v1.Pod) bool
	// GetCreationTimestamp returns the creation timestamp of the original object.
	GetCreationTimestamp() *metav1.Time
	// GetEnqueuedTimestamp returns the timestamp when the unit is added to pending queue.
	GetEnqueuedTimestamp() *metav1.Time
	// GetPriority returns the priority score which is used to rank units in the readyQ
	GetPriority() int32
	// GetPods returns all pods belongs to this unit
	GetPods() []*QueuedPodInfo
	// ValidatePodCount checks if the podCount is a valid number in a batch operation for this unit
	ValidatePodCount(podCount int) bool
	// NumPods return the number of QueuedPodInfos in the unit
	NumPods() int
	// GetPod return a QueuedPodInfo with the same pod.UID
	GetPod(pod *QueuedPodInfo) *QueuedPodInfo
	// AddPod adds QueuedPodInfo into the Unit
	AddPod(pod *QueuedPodInfo) error
	// AddPods adds QueuedPodInfo into the Unit
	AddPods(pods []*QueuedPodInfo) error
	// UpdatePod updates QueuedPodInfo in the Unit
	UpdatePod(pod *QueuedPodInfo) error
	// DeletePod deletes QueuedPodInfo from the Unit
	DeletePod(pod *QueuedPodInfo) error
	// AddPodsIfNotPresent adds pods not existed in the Unit
	AddPodsIfNotPresent(pods ...*QueuedPodInfo) error
	GetTimeoutPeriod() int32
	// GetAnnotations returns the annotations of the Unit
	GetAnnotations() map[string]string
	// GetMinMember gets the min member value
	GetMinMember() (int, error)
	// GetRequiredAffinity returns required affinity scheduling rules, which
	// must be met in scheduling.
	GetRequiredAffinity() ([]UnitAffinityTerm, error)
	// GetPreferredAffinity returns preferred affinity scheduling rules, which
	// don't necessarily have to be satisfied but scheduler will prefer to schedule pods
	// to nodes that satisfy the affinity rules.
	GetPreferredAffinity() ([]UnitAffinityTerm, error)
	// GetAffinityNodeSelector returns the nodeSelector in affinity which defines the specific affinity rules.
	GetAffinityNodeSelector() (*v1.NodeSelector, error)
	// GetSortRulesForAffinity return the rules that indicate how the nodeGroups are sorted.
	// The rule's index in slice is the sort sequence.
	GetSortRulesForAffinity() ([]SortRule, error)
	// IsDebugModeOn checks whether the debug mode is set to on
	IsDebugModeOn() bool
	// SetEnqueuedTimeStamp set the timestamp when the unit is added to pending queue
	SetEnqueuedTimeStamp(time.Time)
	// GetUnitProperty returns unit property to be used by metrics and tracing
	GetUnitProperty() UnitProperty
	// ResetPods will empty all pods for the unit
	// It will be used by binder when we want to re-enqueue the unit (empty pods and then add waiting pods back)
	ResetPods()
}

type PDBItem interface {
	GetPDB() *policy.PodDisruptionBudget
	SetPDB(*policy.PodDisruptionBudget)
	GetPDBSelector() labels.Selector
	SetPDBSelector(labels.Selector)
	RemoveAllOwners()
	AddOwner(string, string)
	RemoveOwner(string, string)
	GetRelatedOwnersByType(string) []string
	RemovePDBFromOwner(func(string, string))
	GetGeneration() int64
	SetGeneration(int64)
	Replace(PDBItem)
	Clone() PDBItem
}

type OwnerItem interface {
	GetOwnerLabels() map[string]string
	AddPDB(string)
	RemovePDB(string)
	GetRelatedPDBs() []string
	SetPDBUpdated()
	GetPDBUpdated() bool
	Replace(OwnerItem)
	Clone() OwnerItem
	GetGeneration() int64
	SetGeneration(int64)
}

type PluginList interface {
	List() []string
}

type OrderedPluginRegistry struct {
	Plugins []string
}

var _ PluginList = &OrderedPluginRegistry{}

func (fpo *OrderedPluginRegistry) List() []string {
	if fpo == nil {
		return []string{}
	}
	return fpo.Plugins
}

type PluginOrder map[string]int
