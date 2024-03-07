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
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"

	godelcache "github.com/kubewharf/godel-scheduler/pkg/binder/cache"
	pt "github.com/kubewharf/godel-scheduler/pkg/binder/testing"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
)

func TestPDBChecker(t *testing.T) {
	tests := []struct {
		name                  string
		victims               []*v1.Pod
		pdbs                  []*policy.PodDisruptionBudget
		expectedPreemptResult []*framework.Status
		expectedPDBAllowed    []int32
	}{
		{
			name: "violating pdb",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			crdClient := godelclientfake.NewSimpleClientset()
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			cache := godelcache.New(30*time.Second, make(chan struct{}), "binder")
			fh, err := pt.NewBinderFrameworkHandle(client, crdClient, informerFactory, crdInformerFactory, cache)
			if err != nil {
				t.Fatal(err)
			}
			for _, pdb := range tt.pdbs {
				cache.AddPDB(pdb)
			}

			commonState := framework.NewCycleState()
			checker := &PDBChecker{
				handle: fh,
			}
			if status := checker.ClusterPrePreempting(nil, nil, commonState); status != nil {
				t.Errorf("failed to prepare preemption: %v", status)
			}
			for i, pod := range tt.victims {
				gotCode, gotMsg := checker.VictimChecking(nil, pod, nil, commonState)
				gotPreemptResult := framework.NewStatus(gotCode, gotMsg)
				if !reflect.DeepEqual(tt.expectedPreemptResult[i], gotPreemptResult) {
					t.Errorf("index %d, expected preemption result: %v, but got: %v", i, tt.expectedPreemptResult[i], gotPreemptResult)
				}
				if gotPreemptResult.Code() == framework.PreemptionFail {
					continue
				}
				gotPostPreemptResult := checker.PostVictimChecking(nil, pod, nil, commonState)
				if gotPostPreemptResult != nil {
					t.Errorf("index: %d, get post preemption result error: %v", i, gotPostPreemptResult)
				}
			}
			pdbAllowed, err := framework.GetPDBsAllowed(commonState)
			if err != nil {
				t.Errorf("failed to get pdb allowed: %v", err)
			}
			if !cmp.Equal(tt.expectedPDBAllowed, pdbAllowed) {
				t.Errorf("expected pdb allowed %v, bu got %v", tt.expectedPDBAllowed, pdbAllowed)
			}
		})
	}
}
