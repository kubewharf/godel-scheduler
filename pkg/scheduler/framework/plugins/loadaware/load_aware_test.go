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

package loadaware

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/loadaware/estimator"
	st "github.com/kubewharf/godel-scheduler/pkg/scheduler/testing"
	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	frameworkhelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper/framework-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func MakeNode(node string, milliCPU, memory int64) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: node},
		Status: v1.NodeStatus{
			Capacity: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(milliCPU, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(memory, resource.BinarySI),
			},
			Allocatable: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(milliCPU, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(memory, resource.BinarySI),
			},
		},
	}
}

func TestLoadAware(t *testing.T) {
	defaultResourceSpec := []config.ResourceSpec{
		{Name: string(v1.ResourceCPU), Weight: 1, ResourceType: podutil.BestEffortPod},
		{Name: string(v1.ResourceMemory), Weight: 1, ResourceType: podutil.BestEffortPod},
	}

	podRequests1 := map[v1.ResourceName]string{v1.ResourceCPU: "1", v1.ResourceMemory: "1Gi"}
	podRequests2 := map[v1.ResourceName]string{v1.ResourceCPU: "2", v1.ResourceMemory: "2Gi"}

	bePod := testinghelper.MakePod().Name("pod").UID("uid").Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.BestEffortPod)).Req(podRequests1).Obj()
	beEmptyPod := testinghelper.MakePod().Name("pod").UID("uid").Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.BestEffortPod)).Obj()

	bePod1 := testinghelper.MakePod().Name("pod1").UID("pod1").Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.BestEffortPod)).Req(podRequests1).Node("machine1").Obj()
	bePod2 := testinghelper.MakePod().Name("pod2").UID("pod2").Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.BestEffortPod)).Req(podRequests2).Node("machine2").Obj()

	gtPod1 := testinghelper.MakePod().Name("pod1").UID("pod1").Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Req(podRequests1).Node("machine1").Obj()
	gtPod2 := testinghelper.MakePod().Name("pod2").UID("pod2").Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.GuaranteedPod)).Req(podRequests2).Node("machine2").Obj()

	nodeCapacity := map[v1.ResourceName]string{v1.ResourceCPU: "4", v1.ResourceMemory: "16Gi"}

	nodeInfo1 := frameworkhelper.MakeNodeInfo().Name("machine1").Capacity(nodeCapacity).CNRCapacity(nodeCapacity)
	nodeInfo2 := frameworkhelper.MakeNodeInfo().Name("machine2").Capacity(nodeCapacity).CNRCapacity(nodeCapacity)

	frameworkhelper.MakeNodeInfo()

	tests := []struct {
		pod          *v1.Pod
		pods         []*v1.Pod
		nodeInfos    []framework.NodeInfo
		args         config.LoadAwareArgs
		wantErr      string
		expectedList framework.NodeScoreList
		name         string
	}{
		{
			// Node1 scores on 0-MaxNodeScore scale
			// CPU Score: ((4000 - 250) * MaxNodeScore) / 4000 = 93
			// Memory Score: ((16 - 0.2) * MaxNodeScore) / 10000 = 98
			// Node1 Score: (93 + 98) / 2 = 95
			// Node2 scores on 0-MaxNodeScore scale
			// CPU Score: ((4000 - 250) * MaxNodeScore) / 4000 = 93
			// Memory Score: ((16 - 0.2) * MaxNodeScore) / 10000 = 98
			// Node1 Score: (93 + 98) / 2 = 95
			pod:          beEmptyPod,
			nodeInfos:    []framework.NodeInfo{nodeInfo1.Clone(), nodeInfo2.Clone()},
			args:         config.LoadAwareArgs{Resources: defaultResourceSpec},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 97}, {Name: "machine2", Score: 97}},
			name:         "nothing scheduled, nothing requested(use least requests)",
		},
		{
			// Node1 scores on 0-MaxNodeScore scale
			// CPU Score: ((4000 - 2000) * MaxNodeScore) / 4000 = 50
			// Memory Score: ((16 - 2) * MaxNodeScore) / 16 = 87
			// Node1 Score: (50 + 87) / 2 = 68
			// Node2 scores on 0-MaxNodeScore scale
			// CPU Score: ((4000 - 3000) * MaxNodeScore) / 4000 = 25
			// Memory Score: ((16 - 3) * MaxNodeScore) / 16 = 81
			// Node2 Score: (25 + 81) / 2 = 53
			pod:          bePod,
			pods:         []*v1.Pod{bePod1, bePod2},
			nodeInfos:    []framework.NodeInfo{nodeInfo1.Clone(), nodeInfo2.Clone()},
			args:         config.LoadAwareArgs{Resources: defaultResourceSpec},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 68}, {Name: "machine2", Score: 53}},
			name:         "different size pods scheduled, be resources requested, same sized machines",
		},
		{
			// Node1 scores on 0-MaxNodeScore scale
			// CPU Score: ((4000 - 1000) * MaxNodeScore) / 4000 = 75
			// Memory Score: ((16 - 1) * MaxNodeScore) / 16 = 93
			// Node1 Score: (75 + 93) / 2 = 84
			// Node2 scores on 0-MaxNodeScore scale
			// CPU Score: ((4000 - 1000) * MaxNodeScore) / 4000 = 75
			// Memory Score: ((16 - 1) * MaxNodeScore) / 16 = 93
			// Node1 Score: (75 + 93) / 2 = 84
			pod:          bePod,
			pods:         []*v1.Pod{gtPod1, gtPod2},
			nodeInfos:    []framework.NodeInfo{nodeInfo1.Clone(), nodeInfo2.Clone()},
			args:         config.LoadAwareArgs{Resources: defaultResourceSpec},
			expectedList: []framework.NodeScore{{Name: "machine1", Score: 84}, {Name: "machine2", Score: 84}},
			name:         "different gt size pods scheduled, be resources requested, same sized machines, gt pods will be ignored",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schedulerCache := godelcache.New(commoncache.MakeCacheHandlerWrapper().
				ComponentName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				PodAssumedTTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				Obj())
			snapshot := godelcache.NewEmptySnapshot(commoncache.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				Obj())
			{
				// Prepare cache and snapshot.
				for _, p := range test.pods {
					schedulerCache.AddPod(p)
				}
				for _, n := range test.nodeInfos {
					schedulerCache.AddNode(n.GetNode())
					schedulerCache.AddCNR(n.GetCNR())
				}
				schedulerCache.UpdateSnapshot(snapshot)
			}
			fh, _ := st.NewPodFrameworkHandle(nil, nil, nil, nil, schedulerCache, snapshot, nil, nil, nil, nil)

			p, err := NewLoadAware(&test.args, fh)

			if len(test.wantErr) != 0 {
				if err != nil && test.wantErr != err.Error() {
					t.Fatalf("got err %v, want %v", err.Error(), test.wantErr)
				} else if err == nil {
					t.Fatalf("no error produced, wanted %v", test.wantErr)
				}
				return
			}

			if err != nil && len(test.wantErr) == 0 {
				t.Fatalf("failed to initialize plugin NodeResourcesLeastAllocated, got error: %v", err)
			}

			cycleState := framework.NewCycleState()

			resourceType, err := podutil.GetPodResourceType(test.pod)
			assert.NoError(t, err)

			framework.SetPodResourceTypeState(resourceType, cycleState)
			for i := range test.nodeInfos {
				hostResult, err := p.(framework.ScorePlugin).Score(context.Background(), cycleState, test.pod, test.nodeInfos[i].GetNodeName())
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if !reflect.DeepEqual(test.expectedList[i].Score, hostResult) {
					t.Errorf("expected %#v, got %#v", test.expectedList[i].Score, hostResult)
				}
			}
		})
	}
}

func TestLoadAwareNodeMetricEstimator(t *testing.T) {
	defaultResourceSpec := []config.ResourceSpec{
		{Name: string(v1.ResourceCPU), Weight: 1, ResourceType: podutil.BestEffortPod},
		{Name: string(v1.ResourceMemory), Weight: 1, ResourceType: podutil.BestEffortPod},
	}
	defaultUsageThresholds := map[v1.ResourceName]int64{
		v1.ResourceCPU:    80, // 80%
		v1.ResourceMemory: 80, // 80%
	}
	defaultEstimatedScalingFactors := map[v1.ResourceName]int64{
		v1.ResourceCPU:    70, // 70% (differ from 60%)
		v1.ResourceMemory: 70, // 70% (differ from 60%)
	}

	defaultNodeName := "n"
	makeResource := func(resMap map[v1.ResourceName]string) v1.ResourceList {
		res := v1.ResourceList{}
		for k, v := range resMap {
			res[k] = resource.MustParse(v)
		}
		return res
	}
	makeBasicPod := func(key string) *v1.Pod {
		p := testinghelper.MakePod().Name(key).UID(key).
			Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.BestEffortPod)).
			Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).Annotation(podutil.SchedulerAnnotationKey, "godel-scheduler").
			Annotation(podutil.AssumedNodeAnnotationKey, defaultNodeName).Req(map[v1.ResourceName]string{v1.ResourceCPU: "100m", v1.ResourceMemory: "100"}).
			Obj()
		return p
	}
	/*
		pods on node: [p0, p1, p2, p3, p4].
		pods in metrics: [p0, p2, (p5)].
	*/
	p0 := makeBasicPod("p0")
	p1 := makeBasicPod("p1")
	p2 := makeBasicPod("p2")
	p3 := makeBasicPod("p3")
	p4 := makeBasicPod("p4")
	// p5 := makeBasicPod("p5")

	nodeAllocatable := makeResource(map[v1.ResourceName]string{v1.ResourceCPU: "1000m", v1.ResourceMemory: "1000"})
	cnr := &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: defaultNodeName,
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			Resources: katalystv1alpha1.Resources{
				Allocatable: &nodeAllocatable,
				Capacity:    &nodeAllocatable,
			},
		},
	}
	cnrWithNodeMetric := func(nodeMetric *katalystv1alpha1.NodeMetricStatus) *katalystv1alpha1.CustomNodeResource {
		ret := cnr.DeepCopy()
		ret.Status.NodeMetricStatus = nodeMetric
		return ret
	}

	tests := []struct {
		name         string
		pod          *v1.Pod
		existingPods []*v1.Pod
		cnrs         []*katalystv1alpha1.CustomNodeResource
		expectedList framework.NodeScoreList
	}{
		{
			/*
				podResourcesUsed  = 100 * 70% = 70
				nodeResourcesUsed = 250(p0 + p2 + p5) + (100(p1) + 100(p3) + 100(p4)) * 70%
				 				  = 250 + 210 = 460
				totalUsage        = 530
				score             = (1000 - 530) / 1000 * 100 = 47
			*/
			name:         "normal case",
			pod:          makeBasicPod("p"),
			existingPods: []*v1.Pod{p0, p1, p2, p3, p4},
			cnrs: []*katalystv1alpha1.CustomNodeResource{
				cnrWithNodeMetric(
					&katalystv1alpha1.NodeMetricStatus{
						UpdateTime: metav1.Now(),
						GroupMetric: []katalystv1alpha1.GroupMetricInfo{
							{
								QoSLevel: string(util.ReclaimedCores), // BE
								ResourceUsage: katalystv1alpha1.ResourceUsage{
									GenericUsage: &katalystv1alpha1.ResourceMetric{
										CPU:    resource.NewMilliQuantity(250, resource.DecimalSI),
										Memory: resource.NewQuantity(250, resource.BinarySI),
									},
								},
								PodList: []string{"/p0", "/p2", "/p5"},
							},
						},
					}),
			},
			expectedList: []framework.NodeScore{{Name: defaultNodeName, Score: 47}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schedulerCache := godelcache.New(commoncache.MakeCacheHandlerWrapper().
				ComponentName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				PodAssumedTTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				Obj())
			snapshot := godelcache.NewEmptySnapshot(commoncache.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				Obj())
			{
				// Prepare cache and snapshot.
				for _, p := range test.existingPods {
					schedulerCache.AddPod(p)
				}
				for _, cnr := range test.cnrs {
					schedulerCache.AddNode(&v1.Node{ObjectMeta: metav1.ObjectMeta{Name: cnr.Name}})
					schedulerCache.AddCNR(cnr)
				}
				schedulerCache.UpdateSnapshot(snapshot)
			}
			fh, _ := st.NewPodFrameworkHandle(nil, nil, nil, nil, schedulerCache, snapshot, nil, nil, nil, nil)

			plugin, _ := NewLoadAware(&config.LoadAwareArgs{
				Estimator:                   estimator.NodeMetricEstimatorName,
				Resources:                   defaultResourceSpec,
				FilterExpiredNodeMetrics:    true,
				NodeMetricExpirationSeconds: 30,
				UsageThresholds:             defaultUsageThresholds,
				EstimatedScalingFactors:     defaultEstimatedScalingFactors,
			}, fh)

			cycleState := framework.NewCycleState()
			framework.SetPodResourceTypeState(podutil.BestEffortPod, cycleState)
			for i := range test.cnrs {
				got, err := plugin.(framework.ScorePlugin).Score(context.Background(), cycleState, test.pod, test.cnrs[i].GetName())
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if diff := cmp.Diff(got, test.expectedList[i].Score); len(diff) > 0 {
					t.Errorf("Unexpected diff: %+v\n", diff)
				}
			}
		})
	}
}
