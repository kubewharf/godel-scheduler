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

package binder

import (
	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/binder/metrics"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

// BindingUnitInfo contains all necessary info for unit
type bindingUnitInfo struct {
	mu        sync.Mutex
	unitKey   string
	minMember int
	// number of tasks arrives before this binding attempt
	// this may not equal to the number of all tasks belonging to this unit
	allMember int
	// everScheduled indicates whether we have ever scheduled some instances of this unit
	// if unit is pod group, this mean whether min member instances have ever been scheduled
	everScheduled bool

	// store unit info and all pods info
	// flow is:
	// queuedUnitInfo
	//         |
	//         |  [initialization]
	//         ∨
	// assumedTasks + newTasks + ignoredTasks
	//         |
	//         |  [check result]
	//         ∨
	// readyTasks + waitingTasks + failedTasks + ignoredTasks
	queuedUnitInfo *framework.QueuedUnitInfo

	// Tasks which arrived before and have already passed the conflicts check and been assumed
	// waiting for victims (of some preemptors) to be cleaned up
	assumedTasks map[types.UID]*framework.QueuedPodInfo
	// Tasks which come for the first time
	// key is node name, value are tasks scheduled to that node
	newTasks *TaskSetGroupByNode
	// tasks we don't need to do anything for them, just ignore them
	// for example: tasks which are being deleted or assumed will be added here
	// tasks are added to this when initialization (not coming from assumedTasks and newTasks)
	ignoredTasks []*framework.QueuedPodInfo

	// used for checking if victim is added before
	// all victims come from newTasks
	allVictims map[types.UID]bool

	// tasks which are ready to be bound
	// these tasks may come from two places:
	// 1. assumed tasks: all assumed tasks finished preemption (they already passed the conflict checks before)
	// 2. new tasks: all new tasks finished checks (no one needs preemption, if preemption is needed, tasks will re-enqueue and will become assumed tasks next round)
	readyTasks map[types.UID]*checkResult
	// tasks which passed the conflicts check and are waiting for victims to be cleaned up
	// these tasks may come from two places:
	// 1. assumed tasks: still waiting for victims to be cleaned up;
	// 2. new tasks: preemptors and those tasks scheduled to the same nodes with one of the preemptors (also need to wait for victims to be cleaned up)
	// TODO: figure out if we need to group waiting tasks by node name
	waitingTasks map[types.UID]*checkResult
	// tasks which don't pass the conflicts check
	// failed tasks may come from almost all phases of binder workflow, so we need to
	// remove all related info (for example: victims...) from unit info because these info may affect check results for other tasks
	failedTasks map[types.UID]*checkResult
}

type checkResult struct {
	// if this task has already passed the checks and been assumed before (not in this check attempt)
	// if this is true, that means this task is not new tasks
	assumed bool

	// if assumed is set to true, runningUnit only has queuedPodInfo
	runningUnit *runningUnitInfo

	// this will be set for failed tasks
	err error
}

type TaskSetGroupByNode struct {
	// nodes that tasks are scheduled to
	Nodes sets.String

	// first key is node name, value are tasks scheduled to that node
	// second is pod UID
	Tasks map[string]map[types.UID]*runningUnitInfo
	// first key is node name, value are victims on that node
	// second key is pod UID
	VictimsGroupByNode map[string]map[types.UID]*v1.Pod
}

func NewBindingUnitInfo(unit *framework.QueuedUnitInfo) *bindingUnitInfo {
	// We have checked the minMember when ValidateUnit.
	min, _ := unit.GetMinMember()

	return &bindingUnitInfo{
		unitKey:        unit.UnitKey,
		minMember:      min,
		allMember:      unit.NumPods(),
		queuedUnitInfo: unit,

		assumedTasks: make(map[types.UID]*framework.QueuedPodInfo),
		newTasks: &TaskSetGroupByNode{
			Tasks:              make(map[string]map[types.UID]*runningUnitInfo),
			Nodes:              sets.NewString(),
			VictimsGroupByNode: make(map[string]map[types.UID]*v1.Pod),
		},

		allVictims: make(map[types.UID]bool),

		readyTasks:   make(map[types.UID]*checkResult, 0),
		waitingTasks: make(map[types.UID]*checkResult, 0),
		failedTasks:  make(map[types.UID]*checkResult, 0),
		ignoredTasks: make([]*framework.QueuedPodInfo, 0),
	}
}

func (unitInfo *bindingUnitInfo) IsUnitFailed() bool {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	if unitInfo.queuedUnitInfo.Type() == framework.PodGroupUnitType {
		if !unitInfo.everScheduled && unitInfo.allMember-len(unitInfo.failedTasks)-len(unitInfo.ignoredTasks) < unitInfo.minMember {
			// unit is not ever scheduled, and break mim member semantic (too many tasks failed)
			return true
		}
	} else {
		// for single pod type, if failed tasks has element, it must the single pod
		if len(unitInfo.failedTasks) > 0 {
			return true
		}
	}

	return false
}

func (unitInfo *bindingUnitInfo) IsAbleToBindReadyTasks() bool {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	if unitInfo.queuedUnitInfo.Type() == framework.PodGroupUnitType {
		if unitInfo.everScheduled || len(unitInfo.readyTasks) >= unitInfo.minMember {
			// unit is not ever scheduled, and break mim member semantic (too many tasks failed)
			return true
		} else {
			return false
		}
	}

	return true
}

func (unitInfo *bindingUnitInfo) HasVictim(uid types.UID) bool {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	return unitInfo.allVictims[uid]
}

func (unitInfo *bindingUnitInfo) NoVictim() bool {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	return len(unitInfo.allVictims) == 0
}

func (unitInfo *bindingUnitInfo) GetAssumedTasks() []*framework.QueuedPodInfo {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	len := len(unitInfo.assumedTasks)
	if len == 0 {
		return nil
	}
	result := make([]*framework.QueuedPodInfo, len)
	i := 0
	for _, qp := range unitInfo.assumedTasks {
		result[i] = qp
		i++
	}
	return result
}

func (unitInfo *bindingUnitInfo) GetNewTasks() []*runningUnitInfo {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	var length int
	for _, newTasks := range unitInfo.newTasks.Tasks {
		length += len(newTasks)
	}
	if length == 0 {
		return nil
	}

	result := make([]*runningUnitInfo, length)
	i := 0
	for _, newTasks := range unitInfo.newTasks.Tasks {
		for _, newTask := range newTasks {
			result[i] = newTask
			i++
		}
	}
	return result
}

func (unitInfo *bindingUnitInfo) GetVictimsOfNewTasksOnNode(nodeName string) []*v1.Pod {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	len := len(unitInfo.newTasks.VictimsGroupByNode[nodeName])
	if len == 0 {
		return nil
	}

	result := make([]*v1.Pod, len)
	i := 0
	for _, victim := range unitInfo.newTasks.VictimsGroupByNode[nodeName] {
		result[i] = victim
		i++
	}
	return result
}

func (unitInfo *bindingUnitInfo) GetNodeListOfNewTasks() []string {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	return unitInfo.newTasks.Nodes.UnsortedList()
}

func (unitInfo *bindingUnitInfo) GetNewTasksOnNode(nodeName string) []*runningUnitInfo {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	len := len(unitInfo.newTasks.Tasks[nodeName])
	if len == 0 {
		return nil
	}
	result := make([]*runningUnitInfo, len)
	i := 0
	for _, task := range unitInfo.newTasks.Tasks[nodeName] {
		result[i] = task
		i++
	}
	return result
}

func (unitInfo *bindingUnitInfo) GetReadyAndWaitingTasks() []*checkResult {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	len := len(unitInfo.readyTasks) + len(unitInfo.waitingTasks)
	if len == 0 {
		return nil
	}

	result := make([]*checkResult, len)
	i := 0
	for _, cr := range unitInfo.readyTasks {
		result[i] = cr
		i++
	}
	for _, cr := range unitInfo.waitingTasks {
		result[i] = cr
		i++
	}
	return result
}

func (unitInfo *bindingUnitInfo) GetReadyTasks() []*checkResult {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	len := len(unitInfo.readyTasks)
	if len == 0 {
		return nil
	}

	result := make([]*checkResult, len)
	i := 0
	for _, cr := range unitInfo.readyTasks {
		result[i] = cr
		i++
	}
	return result
}

func (unitInfo *bindingUnitInfo) GetWaitingTasks() []*checkResult {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	len := len(unitInfo.waitingTasks)
	if len == 0 {
		return nil
	}

	result := make([]*checkResult, len)
	i := 0
	for _, cr := range unitInfo.waitingTasks {
		result[i] = cr
		i++
	}
	return result
}

func (unitInfo *bindingUnitInfo) GetFailedTasks() []*checkResult {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	len := len(unitInfo.failedTasks)
	if len == 0 {
		return nil
	}
	result := make([]*checkResult, len)
	i := 0
	for _, cr := range unitInfo.failedTasks {
		result[i] = cr
		i++
	}
	return result
}

// check if there is at lease one preemptor in new tasks
func (unitInfo *bindingUnitInfo) NewTasksOnNodeNeedPreemption(nodeName string) bool {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	for _, task := range unitInfo.newTasks.Tasks[nodeName] {
		if len(task.victims) > 0 {
			return true
		}
	}
	return false
}

func (unitInfo *bindingUnitInfo) AddAssumedTask(qpi *framework.QueuedPodInfo) {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	if unitInfo.assumedTasks == nil {
		unitInfo.assumedTasks = make(map[types.UID]*framework.QueuedPodInfo)
	}
	unitInfo.assumedTasks[qpi.Pod.UID] = qpi
}

// qpi is not from newTasks and assumedTasks, so do not need to remove qpi from them
func (unitInfo *bindingUnitInfo) AddIgnoredTasks(qpi *framework.QueuedPodInfo) {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	if unitInfo.ignoredTasks == nil {
		unitInfo.ignoredTasks = make([]*framework.QueuedPodInfo, 0)
	}
	unitInfo.ignoredTasks = append(unitInfo.ignoredTasks, qpi)
}

func (unitInfo *bindingUnitInfo) AddNewTask(rui *runningUnitInfo) {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	suggestedNode := rui.suggestedNode
	if len(suggestedNode) == 0 {
		// this shouldn't happen
		klog.ErrorS(nil, "Empty suggested node for running unit", "podNamespace", rui.queuedPodInfo.Pod.Namespace,
			"podName", rui.queuedPodInfo.Pod.Name)
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	if unitInfo.newTasks == nil {
		unitInfo.newTasks = &TaskSetGroupByNode{
			Tasks:              make(map[string]map[types.UID]*runningUnitInfo),
			Nodes:              sets.NewString(),
			VictimsGroupByNode: make(map[string]map[types.UID]*v1.Pod),
		}
	}

	// add running unit info to newTasks
	unitInfo.newTasks.Nodes.Insert(suggestedNode)
	if unitInfo.newTasks.Tasks[suggestedNode] == nil {
		unitInfo.newTasks.Tasks[suggestedNode] = make(map[types.UID]*runningUnitInfo)
	}
	unitInfo.newTasks.Tasks[suggestedNode][rui.queuedPodInfo.Pod.UID] = rui

	if len(rui.victims) > 0 {
		// add victims to running unit info and unit info
		if unitInfo.allVictims == nil {
			unitInfo.allVictims = make(map[types.UID]bool)
		}
		if unitInfo.newTasks.VictimsGroupByNode[suggestedNode] == nil {
			unitInfo.newTasks.VictimsGroupByNode[suggestedNode] = make(map[types.UID]*v1.Pod)
		}
		for _, victim := range rui.victims {
			// add victims to newTasks
			unitInfo.newTasks.VictimsGroupByNode[suggestedNode][victim.UID] = victim
			// add victims to unit info
			unitInfo.allVictims[victim.UID] = true
		}
	}
}

// Note: this function will only be called for new tasks
func (unitInfo *bindingUnitInfo) MoveAllNewTasksOnNodeToFailedList(nodeName string, err error) {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	for uid, rui := range unitInfo.newTasks.Tasks[nodeName] {
		unitInfo.failedTasks[uid] = &checkResult{
			runningUnit: rui,
			err:         err,
			assumed:     false,
		}

		// fail task will be rejected by binder, so we need to remove it from newTasks and assumedTasks
		// and reset the initialization (remove victims and other related info from unit info)
		unitInfo.deleteTaskFromNewOrAssumedTasks(rui, false)
	}
}

func (unitInfo *bindingUnitInfo) MoveAllNewTasksOnNodeFromWaitingToFailedList(nodeName string, err error) {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	for uid, cr := range unitInfo.waitingTasks {
		if !cr.assumed && cr.runningUnit.suggestedNode == nodeName {
			unitInfo.failedTasks[uid] = &checkResult{
				assumed:     cr.assumed,
				runningUnit: cr.runningUnit,
				err:         err,
			}
			delete(unitInfo.waitingTasks, uid)
		}
	}
}

func (unitInfo *bindingUnitInfo) MoveTasksFromReadyToFailedList(failedTasks map[types.UID]error) {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	for uid, err := range failedTasks {
		cr := unitInfo.readyTasks[uid]
		cr.err = err

		unitInfo.failedTasks[uid] = cr
		delete(unitInfo.readyTasks, uid)
	}
}

func (unitInfo *bindingUnitInfo) MoveAllTasksFromReadyToWaitingList() {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	for uid, cr := range unitInfo.readyTasks {
		unitInfo.waitingTasks[uid] = cr
		delete(unitInfo.readyTasks, uid)
	}
}

func (unitInfo *bindingUnitInfo) MoveAllNewTasksInReadyAndWaitingListToFailedList(err error) {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	// move tasks in ready and waiting list to failed list
	for uid, cr := range unitInfo.readyTasks {
		if !cr.assumed {
			unitInfo.failedTasks[uid] = &checkResult{
				assumed:     cr.assumed,
				runningUnit: cr.runningUnit,
				err:         err,
			}
			delete(unitInfo.readyTasks, uid)
		}
	}
	for uid, cr := range unitInfo.waitingTasks {
		if !cr.assumed {
			unitInfo.failedTasks[uid] = &checkResult{
				assumed:     cr.assumed,
				runningUnit: cr.runningUnit,
				err:         err,
			}
			delete(unitInfo.waitingTasks, uid)
		}
	}
}

func (unitInfo *bindingUnitInfo) MoveAllTasksToFailedList(err error) {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	// move all assumed tasks to failed list
	for uid, qpi := range unitInfo.assumedTasks {
		unitInfo.failedTasks[uid] = &checkResult{
			runningUnit: &runningUnitInfo{queuedPodInfo: qpi},
			err:         err,
			assumed:     true,
		}
		unitInfo.deleteTaskFromNewOrAssumedTasks(&runningUnitInfo{queuedPodInfo: qpi}, true)
	}
	// move all new tasks to failed list
	for _, tasks := range unitInfo.newTasks.Tasks {
		for uid, rui := range tasks {
			unitInfo.failedTasks[uid] = &checkResult{
				runningUnit: rui,
				err:         err,
				assumed:     false,
			}
			unitInfo.deleteTaskFromNewOrAssumedTasks(rui, false)
		}
	}
	// move tasks in ready and waiting list to failed list
	for uid, cr := range unitInfo.readyTasks {
		unitInfo.failedTasks[uid] = &checkResult{
			assumed:     cr.assumed,
			runningUnit: cr.runningUnit,
			err:         err,
		}
		delete(unitInfo.readyTasks, uid)
	}
	for uid, cr := range unitInfo.waitingTasks {
		unitInfo.failedTasks[uid] = &checkResult{
			assumed:     cr.assumed,
			runningUnit: cr.runningUnit,
			err:         err,
		}
		delete(unitInfo.waitingTasks, uid)
	}
}

func (unitInfo *bindingUnitInfo) MoveAllNewTasksOnNodeToWaitingList(nodeName string) {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	for uid, rui := range unitInfo.newTasks.Tasks[nodeName] {
		unitInfo.waitingTasks[uid] = &checkResult{runningUnit: rui, assumed: false}

		// TODO: we don't actually need to remove all info related the these tasks from unit info
		// because this function will be called at the end of the whole workflow
		unitInfo.deleteTaskFromNewOrAssumedTasks(rui, false)
	}
}

// Note: this function will only be called for new tasks
func (unitInfo *bindingUnitInfo) MoveAllNewTasksOnNodeToReadyList(nodeName string) {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	for uid, rui := range unitInfo.newTasks.Tasks[nodeName] {
		unitInfo.readyTasks[uid] = &checkResult{runningUnit: rui, assumed: false}

		// TODO: we don't actually need to remove all info related the these tasks from unit info
		// because this function will be called at the end of the whole workflow
		unitInfo.deleteTaskFromNewOrAssumedTasks(rui, false)
	}
}

func (unitInfo *bindingUnitInfo) MoveAllAssumedTasksToReadyList() {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	for uid, qpi := range unitInfo.assumedTasks {
		unitInfo.readyTasks[uid] = &checkResult{runningUnit: &runningUnitInfo{queuedPodInfo: qpi, suggestedNode: qpi.ReservedPod.Spec.NodeName}, assumed: true}

		unitInfo.deleteTaskFromNewOrAssumedTasks(&runningUnitInfo{queuedPodInfo: qpi}, true)
	}
}

func (unitInfo *bindingUnitInfo) MoveAllAssumedTasksToWaitingList() {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	for uid, qpi := range unitInfo.assumedTasks {
		unitInfo.waitingTasks[uid] = &checkResult{runningUnit: &runningUnitInfo{queuedPodInfo: qpi, suggestedNode: qpi.ReservedPod.Spec.NodeName}, assumed: true}

		unitInfo.deleteTaskFromNewOrAssumedTasks(&runningUnitInfo{queuedPodInfo: qpi}, true)
	}
}

func (unitInfo *bindingUnitInfo) deleteTaskFromNewOrAssumedTasks(rui *runningUnitInfo, assumed bool) {
	if assumed {
		// remove task from assumed task map
		if rui.queuedPodInfo != nil && rui.queuedPodInfo.Pod != nil {
			delete(unitInfo.assumedTasks, rui.queuedPodInfo.Pod.UID)
		}
	} else if len(rui.suggestedNode) > 0 && !assumed {
		// remove failed task from newTasks
		delete(unitInfo.newTasks.Tasks[rui.suggestedNode], rui.queuedPodInfo.Pod.UID)
		if len(rui.victims) > 0 {
			// remove victims of this failed task from VictimsGroupByNode
			for _, victim := range rui.victims {
				delete(unitInfo.newTasks.VictimsGroupByNode[rui.suggestedNode], victim.UID)

				delete(unitInfo.allVictims, victim.UID)
			}
		}
		// remove node if necessary
		if len(unitInfo.newTasks.Tasks[rui.suggestedNode]) == 0 && len(unitInfo.newTasks.VictimsGroupByNode) == 0 {
			unitInfo.newTasks.Nodes.Delete(rui.suggestedNode)
			// TODO: delete VictimsGroupByNode and Tasks ?
		}
	}
}

func (unitInfo *bindingUnitInfo) AddFailedTask(rui *runningUnitInfo, err error, reason string, assumed bool) {
	unitInfo.mu.Lock()
	defer unitInfo.mu.Unlock()

	if unitInfo.failedTasks == nil {
		unitInfo.failedTasks = make(map[types.UID]*checkResult, 0)
	}

	unitInfo.failedTasks[rui.queuedPodInfo.Pod.UID] = &checkResult{
		runningUnit: rui,
		err:         err,
		assumed:     assumed,
	}

	// fail task will be rejected by binder, so we need to remove it from newTasks and assumedTasks
	// and reset the initialization (remove victims and other related info from unit info)
	unitInfo.deleteTaskFromNewOrAssumedTasks(rui, assumed)
	metrics.PodBindingFailureInc(rui.queuedPodInfo.GetPodProperty(), reason)
}

// running unit info contains all necessary info for task(pod)
type runningUnitInfo struct {
	suggestedNode string
	queuedPodInfo *framework.QueuedPodInfo

	// ctx context.Context
	// deep-copied from queuedPodInfo and store some new info, used by binding
	clonedPod *v1.Pod

	// victims of this pod
	victims []*v1.Pod

	// checkFailed shows whether this running unit is failed (no matter in which phase)
	checkFailed bool

	State     *framework.CycleState
	Framework framework.BinderFramework

	// lazy initialization, call getSchedulingTrace to get and initialize trace
	trace tracing.SchedulingTrace
}

func newRunningUnitInfo(queuedPodInfo *framework.QueuedPodInfo) *runningUnitInfo {
	return &runningUnitInfo{
		queuedPodInfo: queuedPodInfo,
		// clonedPod: queuedPodInfo.Pod.DeepCopy(),

		victims: make([]*v1.Pod, 0),
	}
}

// We create this function just try to reduce the pod deep copy overhead.
// we will call this function at the end of running unit info initialization
// no matter when we fail during initialization, we don't need to set clonedPod for the running unit info
func (rui *runningUnitInfo) clonePodForRunningUnit() {
	if rui.queuedPodInfo.Pod != nil {
		rui.clonedPod = rui.queuedPodInfo.Pod.DeepCopy()
	}
}

func (rui *runningUnitInfo) getSchedulingTrace() tracing.SchedulingTrace {
	if rui.trace != nil {
		return rui.trace
	}

	podInfo := rui.queuedPodInfo
	rui.trace = tracing.NewSchedulingTrace(
		podInfo.Pod,
		podInfo.GetPodProperty().ConvertToTracingTags(),
		tracing.WithBinderOption(),
	)
	return rui.trace
}
