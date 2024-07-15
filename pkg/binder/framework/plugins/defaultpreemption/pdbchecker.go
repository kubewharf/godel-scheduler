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

package defaultpreemption

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	pdbstore "github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores/pdb_store"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/preempting/pdbchecker"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const PDBCheckerName string = "PDBChecker"

type PDBChecker struct {
	handle       handle.BinderFrameworkHandle
	pluginHandle pdbstore.StoreHandle
}

var (
	_ framework.ClusterPrePreemptingPlugin = &PDBChecker{}
	_ framework.VictimCheckingPlugin       = &PDBChecker{}
	_ framework.PostVictimCheckingPlugin   = &PDBChecker{}
)

// NewPDBChecker initializes a new PDBChecker plugin and returns it.
func NewPDBChecker(_ runtime.Object, handle handle.BinderFrameworkHandle) (framework.Plugin, error) {
	var pluginHandle pdbstore.StoreHandle
	if store := handle.FindStore(pdbstore.Name); store != nil {
		pluginHandle = store.(pdbstore.StoreHandle)
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

func (pdb *PDBChecker) ClusterPrePreempting(_ *v1.Pod, _, commonState *framework.CycleState) *framework.Status {
	// only need to exec once in a unit
	_, err := framework.GetPDBItems(commonState)
	if err == nil {
		return nil
	}

	pdbItems := pdb.pluginHandle.GetPDBItemList()
	pdbsAllowed := make([]int32, len(pdbItems))
	for i, pdbItem := range pdbItems {
		pdb := pdbItem.GetPDB()
		pdbsAllowed[i] = pdb.Status.DisruptionsAllowed
	}
	if err := framework.SetPDBsAllowed(pdbsAllowed, commonState); err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	if err := framework.SetPDBItems(pdbItems, commonState); err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	return nil
}

func (pdb *PDBChecker) VictimChecking(_, pod *v1.Pod, _, commonState *framework.CycleState) (framework.Code, string) {
	pdbsAllowed, _ := framework.GetPDBsAllowed(commonState)
	pdbItems, _ := framework.GetPDBItems(commonState)
	violating, matchedPDBIndexes := pdbchecker.CheckPodDisruptionBudgetViolation(pod, pdbsAllowed, pdbItems)
	if violating {
		return framework.PreemptionFail, "violating pdb"
	}
	victimKey := podutil.GeneratePodKey(pod)
	framework.SetMatchedPDBIndexes(victimKey, matchedPDBIndexes, commonState)
	return framework.PreemptionNotSure, ""
}

func (pdb *PDBChecker) PostVictimChecking(_, pod *v1.Pod, _, commonState *framework.CycleState) *framework.Status {
	podKey := podutil.GeneratePodKey(pod)
	matchedPDBIndexesMap, _ := framework.GetMatchedPDBIndexes(commonState)
	pdbsAllowed, err := framework.GetPDBsAllowed(commonState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	pdbItems, err := framework.GetPDBItems(commonState)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	var matchedPDBIndexes []int
	if _, ok := matchedPDBIndexesMap[podKey]; !ok {
		_, matchedPDBIndexes = pdbchecker.CheckPodDisruptionBudgetViolation(pod, pdbsAllowed, pdbItems)
	} else {
		matchedPDBIndexes = matchedPDBIndexesMap[podKey]
	}
	for _, index := range matchedPDBIndexes {
		pdbsAllowed[index]--
	}

	return nil
}
