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
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"
	schedulertesting "github.com/kubewharf/godel-scheduler/pkg/scheduler/testing"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
)

func TestPDBChecker(t *testing.T) {
	tests := []struct {
		name                  string
		victims               []*v1.Pod
		pdbs                  []*policy.PodDisruptionBudget
		oldReplicas           []*appsv1.ReplicaSet
		newReplicas           []*appsv1.ReplicaSet
		expectedPreemptResult []*framework.Status
		expectedPDBAllowed    []int32
	}{
		{
			name: "violating pdb, check pdb in preemption stage",
			victims: []*v1.Pod{
				testing_helper.MakePod().Name("p1").UID("p1").Label("k1", "v1").Label("k2", "v2").Obj(),
				testing_helper.MakePod().Name("p2").UID("p2").Label("k1", "v1").Label("k2", "v2").Obj(),
				testing_helper.MakePod().Name("p3").UID("p3").Label("k1", "v1").Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testing_helper.MakePdb().Name("pdb1").Label("k1", "v1").DisruptionsAllowed(2).Obj(),
				testing_helper.MakePdb().Name("pdb2").Label("k2", "v2").DisruptionsAllowed(1).Obj(),
				testing_helper.MakePdb().Name("pdb3").Label("k3", "v3").DisruptionsAllowed(0).Obj(),
			},
			expectedPreemptResult: []*framework.Status{
				framework.NewStatus(framework.PreemptionNotSure, ""),
				framework.NewStatus(framework.PreemptionFail, "violating pdb"),
				framework.NewStatus(framework.PreemptionNotSure, ""),
			},
			expectedPDBAllowed: []int32{0, 0, 0},
		},
		{
			name: "violating pdb, check pdb in eventhandler stage",
			victims: []*v1.Pod{
				testing_helper.MakePod().Name("p1").UID("p1").Label("k1", "v1").Label("k2", "v2").
					ControllerRef(metav1.OwnerReference{Kind: util.OwnerTypeReplicaSet, Name: "rs1"}).Obj(),
				testing_helper.MakePod().Name("p2").UID("p2").Label("k1", "v1").Label("k2", "v2").
					ControllerRef(metav1.OwnerReference{Kind: util.OwnerTypeReplicaSet, Name: "rs1"}).Obj(),
				testing_helper.MakePod().Name("p3").UID("p3").Label("k1", "v1").
					ControllerRef(metav1.OwnerReference{Kind: util.OwnerTypeReplicaSet, Name: "rs2"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testing_helper.MakePdb().Name("pdb1").Label("k1", "v1").DisruptionsAllowed(2).Obj(),
				testing_helper.MakePdb().Name("pdb2").Label("k2", "v2").DisruptionsAllowed(1).Obj(),
				testing_helper.MakePdb().Name("pdb3").Label("k3", "v3").DisruptionsAllowed(0).Obj(),
			},
			oldReplicas: []*appsv1.ReplicaSet{
				testing_helper.MakeReplicaSet().Name("rs1").Label("k1", "v1").Label("k2", "v2").Obj(),
				testing_helper.MakeReplicaSet().Name("rs2").Label("k1", "v1").Obj(),
			},
			expectedPreemptResult: []*framework.Status{
				framework.NewStatus(framework.PreemptionNotSure, ""),
				framework.NewStatus(framework.PreemptionFail, "violating pdb"),
				framework.NewStatus(framework.PreemptionNotSure, ""),
			},
			expectedPDBAllowed: []int32{0, 0, 0},
		},
		{
			name: "pod violating pdb, cached owner not violating pdb",
			victims: []*v1.Pod{
				testing_helper.MakePod().Name("p1").UID("p1").Label("k1", "v1").Label("k2", "v2").
					ControllerRef(metav1.OwnerReference{Kind: util.OwnerTypeReplicaSet, Name: "rs1"}).Obj(),
				testing_helper.MakePod().Name("p2").UID("p2").Label("k1", "v1").Label("k2", "v2").
					ControllerRef(metav1.OwnerReference{Kind: util.OwnerTypeReplicaSet, Name: "rs1"}).Obj(),
				testing_helper.MakePod().Name("p3").UID("p3").Label("k1", "v1").
					ControllerRef(metav1.OwnerReference{Kind: util.OwnerTypeReplicaSet, Name: "rs2"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testing_helper.MakePdb().Name("pdb1").Label("k1", "v1").DisruptionsAllowed(2).Obj(),
				testing_helper.MakePdb().Name("pdb2").Label("k2", "v2").DisruptionsAllowed(1).Obj(),
				testing_helper.MakePdb().Name("pdb3").Label("k3", "v3").DisruptionsAllowed(0).Obj(),
			},
			oldReplicas: []*appsv1.ReplicaSet{
				testing_helper.MakeReplicaSet().Name("rs1").Label("k1", "v1").Label("k2", "v2").Obj(),
				testing_helper.MakeReplicaSet().Name("rs2").Label("k1", "v1").Obj(),
			},
			newReplicas: []*appsv1.ReplicaSet{
				testing_helper.MakeReplicaSet().Name("rs1").Label("k3", "v3").Obj(),
				testing_helper.MakeReplicaSet().Name("rs2").Label("k3", "v3").Obj(),
			},
			expectedPreemptResult: []*framework.Status{
				framework.NewStatus(framework.PreemptionNotSure, ""),
				framework.NewStatus(framework.PreemptionFail, "violating pdb"),
				framework.NewStatus(framework.PreemptionNotSure, ""),
			},
			expectedPDBAllowed: []int32{0, 0, 0},
		},
		{
			name: "pod not violating pdb, cached owner violating pdb",
			victims: []*v1.Pod{
				testing_helper.MakePod().Name("p1").UID("p1").
					ControllerRef(metav1.OwnerReference{Kind: util.OwnerTypeReplicaSet, Name: "rs1"}).Obj(),
				testing_helper.MakePod().Name("p2").UID("p2").
					ControllerRef(metav1.OwnerReference{Kind: util.OwnerTypeReplicaSet, Name: "rs1"}).Obj(),
				testing_helper.MakePod().Name("p3").UID("p3").
					ControllerRef(metav1.OwnerReference{Kind: util.OwnerTypeReplicaSet, Name: "rs2"}).Obj(),
			},
			pdbs: []*policy.PodDisruptionBudget{
				testing_helper.MakePdb().Name("pdb1").Label("k1", "v1").DisruptionsAllowed(2).Obj(),
				testing_helper.MakePdb().Name("pdb2").Label("k2", "v2").DisruptionsAllowed(1).Obj(),
				testing_helper.MakePdb().Name("pdb3").Label("k3", "v3").DisruptionsAllowed(0).Obj(),
			},
			oldReplicas: []*appsv1.ReplicaSet{
				testing_helper.MakeReplicaSet().Name("rs1").Obj(),
				testing_helper.MakeReplicaSet().Name("rs2").Obj(),
			},
			newReplicas: []*appsv1.ReplicaSet{
				testing_helper.MakeReplicaSet().Name("rs1").Label("k1", "v1").Label("k2", "v2").Obj(),
				testing_helper.MakeReplicaSet().Name("rs2").Label("k1", "v1").Obj(),
			},
			expectedPreemptResult: []*framework.Status{
				framework.NewStatus(framework.PreemptionNotSure, ""),
				framework.NewStatus(framework.PreemptionNotSure, ""),
				framework.NewStatus(framework.PreemptionNotSure, ""),
			},
			expectedPDBAllowed: []int32{2, 1, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			crdClient := godelclientfake.NewSimpleClientset()
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			schedulerCache := cache.New(handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj())
			snapshot := cache.NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				EnableStore("PreemptionStore").
				Obj())
			for _, pdb := range tt.pdbs {
				schedulerCache.AddPDB(pdb)
			}
			for i, rs := range tt.oldReplicas {
				ownerKey := util.GetReplicaSetKey(rs)
				schedulerCache.AddOwner(util.OwnerTypeReplicaSet, ownerKey, rs.GetLabels())
				if len(tt.newReplicas) > i && tt.newReplicas[i] != nil {
					schedulerCache.UpdateOwner(util.OwnerTypeReplicaSet, ownerKey, rs.GetLabels(), tt.newReplicas[i].GetLabels())
				}
			}
			schedulerCache.UpdateSnapshot(snapshot)
			fh, err := schedulertesting.NewSchedulerFrameworkHandle(client, crdClient, informerFactory, crdInformerFactory, schedulerCache, snapshot, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}

			state := framework.NewCycleState()
			preemptionState := framework.NewCycleState()
			checker := &PDBChecker{
				handle: fh,
			}
			commonState := framework.NewCycleState()
			checker.ClusterPrePreempting(nil, state, commonState)
			if status := checker.NodePrePreempting(nil, nil, state, preemptionState); status != nil {
				t.Errorf("failed to prepare preemption: %v", status)
			}
			var gotVictims []*v1.Pod
			for i, pod := range tt.victims {
				podInfo := framework.NewPodInfo(pod)
				victimState := framework.NewVictimState()
				gotCode, gotMsg := checker.VictimSearching(nil, podInfo, state, preemptionState, victimState)
				gotPreemptResult := framework.NewStatus(gotCode, gotMsg)
				if !reflect.DeepEqual(tt.expectedPreemptResult[i], gotPreemptResult) {
					t.Errorf("index %d, expected preemption result: %v, but got: %v", i, tt.expectedPreemptResult[i], gotPreemptResult)
				}
				if gotPreemptResult.Code() == framework.PreemptionFail {
					continue
				}
				gotPostPreemptResult := checker.PostVictimSearching(nil, podInfo, state, preemptionState, victimState)
				if gotPostPreemptResult != nil {
					t.Errorf("index: %d, get post preemption result error: %v", i, gotPostPreemptResult)
					continue
				}
				gotVictims = append(gotVictims, pod)
			}
			gotCompletePreemptResult := checker.NodePostPreempting(nil, gotVictims, nil, nil)
			if gotCompletePreemptResult != nil {
				t.Errorf("get complete preemption result error: %v", gotCompletePreemptResult)
			}

			expectedPDBAllowedMap := map[string]int32{}
			for i, pdb := range tt.pdbs {
				expectedPDBAllowedMap[pdb.Name] = tt.expectedPDBAllowed[i]
			}
			pdbItems := checker.pdbItems
			// check pdb allowed in each node
			{
				pdbPreemptionState, err := getPDBPreemptionState(preemptionState)
				if err != nil {
					t.Errorf("failed to get pdb allowed: %v", err)
				}
				pdbAllowed := pdbPreemptionState.pdbsAllowed
				pdbAllowedMap := map[string]int32{}
				for i, pdbItem := range pdbItems {
					pdbAllowedMap[pdbItem.GetPDB().Name] = pdbAllowed[i]
				}
				if !cmp.Equal(expectedPDBAllowedMap, pdbAllowedMap) {
					t.Errorf("expected pdb allowed %v, bu got %v", expectedPDBAllowedMap, pdbAllowedMap)
				}
			}

			// check pdb allowed in global
			{
				pdbAllowed := checker.pdbsAllowed
				pdbAllowedMap := map[string]int32{}
				for i, pdbItem := range pdbItems {
					pdbAllowedMap[pdbItem.GetPDB().Name] = pdbAllowed[i]
				}
				if !cmp.Equal(expectedPDBAllowedMap, pdbAllowedMap) {
					t.Errorf("expected pdb allowed %v, bu got %v", expectedPDBAllowedMap, pdbAllowedMap)
				}
			}
		})
	}
}

func TestPreemptionStatePDB(t *testing.T) {
	pdbStateNew := newPDBState()
	state := framework.NewCycleState()
	state.Write(SearchingPDBCheckKey, pdbStateNew)
	pdbStateGot, _ := getPDBState(state)
	pdbStateGot.matchedIndexes["a:a"] = []int{0}
	pdbStateGot2, _ := getPDBState(state)
	pdbStateExpected := &pdbState{
		matchedIndexes: map[string][]int{
			"a:a": {0},
		},
	}
	if !reflect.DeepEqual(pdbStateExpected, pdbStateGot2) {
		t.Errorf("expected state: %v, but got: %v", pdbStateExpected, pdbStateGot2)
	}
}
