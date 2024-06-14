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
	"fmt"
	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/preempting/pdbchecker"
	pdbstore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/pdb_store"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/util"
)

const (
	PDBCheckerName       = "PDBChecker"
	SearchingPDBCheckKey = "Searching-" + PDBCheckerName
)

type PDBChecker struct {
	handle            handle.PodFrameworkHandle
	pluginHandle      pdbstore.StoreHandle
	pdbsNameToIndexes map[string]int
	pdbItems          []framework.PDBItem
	pdbsAllowed       []int32
}

var (
	_ framework.ClusterPrePreemptingPlugin = &PDBChecker{}
	_ framework.NodePrePreemptingPlugin    = &PDBChecker{}
	_ framework.VictimSearchingPlugin      = &PDBChecker{}
	_ framework.PostVictimSearchingPlugin  = &PDBChecker{}
	_ framework.NodePostPreemptingPlugin   = &PDBChecker{}
)

// New initializes a new plugin and returns it.
func NewPDBChecker(_ runtime.Object, handle handle.PodFrameworkHandle) (framework.Plugin, error) {
	var pluginHandle pdbstore.StoreHandle
	if ins := handle.FindStore(pdbstore.Name); ins != nil {
		pluginHandle = ins.(pdbstore.StoreHandle)
	}
	checker := &PDBChecker{
		handle:       handle,
		pluginHandle: pluginHandle,
	}
	return checker, nil
}

func (pdb *PDBChecker) Name() string {
	return PDBCheckerName
}

func (pdb *PDBChecker) ClusterPrePreempting(_ *v1.Pod, state, commonState *framework.CycleState) *framework.Status {
	s := newPDBState()
	state.Write(SearchingPDBCheckKey, s)

	// get from common state first
	if pdbCommon, exist, err := GetPDBCommonState(commonState); err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	} else if exist {
		pdb.pdbItems = pdbCommon.pdbItems
		pdb.pdbsAllowed = pdbCommon.pdbsAllowed
		pdb.pdbsNameToIndexes = pdbCommon.pdbsNameToIndexes
		return nil
	}

	pdbItems := pdb.pluginHandle.GetPDBItemList()
	pdbsAllowed := make([]int32, len(pdbItems))
	pdbsNameToIndexes := map[string]int{}
	for i, pdbItem := range pdbItems {
		pdb := pdbItem.GetPDB()
		pdbsAllowed[i] = pdb.Status.DisruptionsAllowed
		pdbsNameToIndexes[util.GetPDBKey(pdb)] = i
	}
	pdb.pdbsNameToIndexes = pdbsNameToIndexes
	pdb.pdbItems = pdbItems
	pdb.pdbsAllowed = pdbsAllowed

	setPDBCommonState(&PDBCommonState{pdbsNameToIndexes, pdbItems, pdbsAllowed}, commonState)

	return nil
}

func (pdb *PDBChecker) NodePrePreempting(_ *v1.Pod, _ framework.NodeInfo, state, preemptionState *framework.CycleState) *framework.Status {
	pdbsAllowed := pdb.pdbsAllowed
	pdbsAllowedCopy := make([]int32, len(pdbsAllowed))
	copy(pdbsAllowedCopy, pdbsAllowed)
	pdbPreemptionState := newPDBPreemptionState(pdbsAllowedCopy)
	preemptionState.Write(SearchingPDBCheckKey, pdbPreemptionState)
	return nil
}

func (pdb *PDBChecker) VictimSearching(_ *v1.Pod, podInfo *framework.PodInfo, state, preemptionState *framework.CycleState, victimState *framework.VictimState) (framework.Code, string) {
	pdbPreemptionState, _ := getPDBPreemptionState(preemptionState)
	pdbsAllowed := pdbPreemptionState.pdbsAllowed

	// check pdbs for owner
	exist, matchedPDBIndexes := pdb.getMatchedPDBIndexesFromOwner(podInfo)
	if exist {
		violating := checkPodDisruptionBudgetViolation(pdbsAllowed, matchedPDBIndexes)
		victimState.MatchedPDBIndexes = matchedPDBIndexes
		victimState.MatchedPDBIndexesExist = true
		if violating {
			return framework.PreemptionFail, "violating pdb"
		}
		return framework.PreemptionNotSure, ""
	}

	// check cached pdbs for pod label
	labels := podInfo.PodPreemptionInfo.Labels
	s, err := getPDBState(state)
	if err != nil {
		return framework.Error, err.Error()
	}
	if matchedPDBIndexes, ok := s.GetMatchedPDBIndexes(labels); ok {
		violating := checkPodDisruptionBudgetViolation(pdbsAllowed, matchedPDBIndexes)
		victimState.MatchedPDBIndexes = matchedPDBIndexes
		victimState.MatchedPDBIndexesExist = true
		if violating {
			return framework.PreemptionFail, "violating pdb"
		}
		return framework.PreemptionNotSure, ""
	}

	// traverse all pdbs
	violating, matchedPDBIndexes := pdbchecker.CheckPodDisruptionBudgetViolation(podInfo.Pod, pdbsAllowed, pdb.pdbItems)
	victimState.MatchedPDBIndexes = matchedPDBIndexes
	victimState.MatchedPDBIndexesExist = true
	s.StoreMatchedPDBIndexes(labels, matchedPDBIndexes)
	if violating {
		return framework.PreemptionFail, "violating pdb"
	}
	return framework.PreemptionNotSure, ""
}

func (pdb *PDBChecker) PostVictimSearching(_ *v1.Pod, podInfo *framework.PodInfo, state, preemptionState *framework.CycleState, victimState *framework.VictimState) *framework.Status {
	pdbPreemptionState, _ := getPDBPreemptionState(preemptionState)
	pdbsAllowed := pdbPreemptionState.pdbsAllowed

	var matchedPDBIndexes []int
	if victimState.MatchedPDBIndexesExist {
		matchedPDBIndexes = victimState.MatchedPDBIndexes
	} else {
		if exist, matchedPDBIndexesFromOwner := pdb.getMatchedPDBIndexesFromOwner(podInfo); exist {
			matchedPDBIndexes = matchedPDBIndexesFromOwner
		} else {
			labels := podInfo.PodPreemptionInfo.Labels
			s, err := getPDBState(state)
			if err != nil {
				return framework.NewStatus(framework.Error, err.Error())
			}
			if matchedPDBIndexesFromLabels, ok := s.GetMatchedPDBIndexes(labels); ok {
				matchedPDBIndexes = matchedPDBIndexesFromLabels
			} else {
				_, matchedPDBIndexes = pdbchecker.CheckPodDisruptionBudgetViolation(podInfo.Pod, pdbsAllowed, pdb.pdbItems)
			}
		}
	}

	for _, index := range matchedPDBIndexes {
		pdbsAllowed[index]--
	}
	return nil
}

func (pdb *PDBChecker) NodePostPreempting(_ *v1.Pod, victims []*v1.Pod, _, _ *framework.CycleState) *framework.Status {
	for _, victim := range victims {
		for i, pdbItem := range pdb.pdbItems {
			if !pdbchecker.PDBMatched(victim, pdbItem) {
				continue
			}
			pdb.pdbsAllowed[i]--
		}
	}
	return nil
}

type PDBCommonState struct {
	pdbsNameToIndexes map[string]int
	pdbItems          []framework.PDBItem
	pdbsAllowed       []int32
}

func (s *PDBCommonState) Clone() framework.StateData {
	return s
}

func (s *PDBCommonState) GetPDBsAllowed() []int32 {
	return s.pdbsAllowed
}

func (s *PDBCommonState) GetPDBItems() []framework.PDBItem {
	return s.pdbItems
}

func GetPDBCommonState(commonState *framework.CycleState) (*PDBCommonState, bool, error) {
	data, err := commonState.Read(SearchingPDBCheckKey)
	if err != nil {
		return nil, false, nil
	}

	s, ok := data.(*PDBCommonState)
	if !ok {
		return nil, false, fmt.Errorf("%+v convert to PDBChecker.pdbCommonState error", data)
	}

	return s, true, nil
}

func setPDBCommonState(pdbCommonState *PDBCommonState, commonState *framework.CycleState) {
	commonState.Write(SearchingPDBCheckKey, pdbCommonState)
}

func checkPodDisruptionBudgetViolation(pdbsAllowed []int32, matchedPDBIndexes []int) bool {
	for _, index := range matchedPDBIndexes {
		if pdbsAllowed[index] <= 0 {
			return true
		}
	}
	return false
}

type pdbState struct {
	matchedIndexes map[string][]int
	mu             sync.RWMutex
}

func newPDBState() *pdbState {
	return &pdbState{
		matchedIndexes: map[string][]int{},
	}
}

func (s *pdbState) Clone() framework.StateData {
	return s
}

func getPDBState(state *framework.CycleState) (*pdbState, error) {
	c, err := state.Read(SearchingPDBCheckKey)
	if err != nil {
		// preFilterState doesn't exist, likely PreFilter wasn't invoked.
		return nil, fmt.Errorf("error reading %q from cycleState: %v", SearchingPDBCheckKey, err)
	}

	s, ok := c.(*pdbState)
	if !ok {
		return nil, fmt.Errorf("%+v convert to PDBChecker.pdbState error", c)
	}

	return s, nil
}

func (s *pdbState) GetMatchedPDBIndexes(labels string) ([]int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matched, ok := s.matchedIndexes[labels]
	return matched, ok
}

func (s *pdbState) StoreMatchedPDBIndexes(labels string, matchedIndexes []int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.matchedIndexes[labels] = matchedIndexes
}

type PDBPreemptionState struct {
	pdbsAllowed []int32
}

func (s *PDBPreemptionState) Clone() framework.StateData {
	copied := make([]int32, len(s.pdbsAllowed))
	copy(copied, s.pdbsAllowed)
	return &PDBPreemptionState{
		pdbsAllowed: copied,
	}
}

func (s *PDBPreemptionState) GetPDBsAllowed() []int32 {
	return s.pdbsAllowed
}

func newPDBPreemptionState(pdbsAllowed []int32) *PDBPreemptionState {
	return &PDBPreemptionState{
		pdbsAllowed: pdbsAllowed,
	}
}

func getPDBPreemptionState(preemptionState *framework.CycleState) (*PDBPreemptionState, error) {
	c, err := preemptionState.Read(SearchingPDBCheckKey)
	if err != nil {
		// preFilterState doesn't exist, likely PreFilter wasn't invoked.
		return nil, fmt.Errorf("error reading %q from cycleState: %v", SearchingPDBCheckKey, err)
	}

	s, ok := c.(*PDBPreemptionState)
	if !ok {
		return nil, fmt.Errorf("%+v convert to PDBChecker.pdbPreemptionState error", c)
	}

	return s, nil
}

func (pdb *PDBChecker) getMatchedPDBIndexesFromOwner(podInfo *framework.PodInfo) (bool, []int) {
	if podInfo.PodPreemptionInfo.OwnerType == "" || podInfo.PodPreemptionInfo.OwnerKey == "" {
		// pod does not have owner
		return false, nil
	}
	exist, pdbUpdated, matchedPDBNames := pdb.pluginHandle.GetPDBsForOwner(podInfo.PodPreemptionInfo.OwnerType, podInfo.PodPreemptionInfo.OwnerKey)
	if !exist {
		// pod's owner does not exist in cache
		return false, nil
	}
	if pdbUpdated && !util.EqualMap(pdb.pluginHandle.GetOwnerLabels(podInfo.PodPreemptionInfo.OwnerType, podInfo.PodPreemptionInfo.OwnerKey), podInfo.Pod.Labels) {
		// if owner's pdb has been updated, and pod's label is not equal to owner's label,
		// the cached pdb info could not be used directly
		return false, nil
	}
	var matchedPDBIndexes []int
	for _, pdbName := range matchedPDBNames {
		matchedPDBIndexes = append(matchedPDBIndexes, pdb.pdbsNameToIndexes[pdbName])
	}
	return true, matchedPDBIndexes
}
