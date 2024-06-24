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

package estimator

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	st "github.com/kubewharf/godel-scheduler/pkg/scheduler/testing"
	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

var (
	defaultNodeName = "n"
	now             = time.Now()

	defaultNodeMetricExpirationSeconds = int64(30)
	defaultUsageThresholds             = map[v1.ResourceName]int64{
		v1.ResourceCPU:    80, // 80%
		v1.ResourceMemory: 80, // 80%
	}
	defaultEstimatedScalingFactors = map[v1.ResourceName]int64{
		v1.ResourceCPU:    60, // 60%
		v1.ResourceMemory: 60, // 60%
	}
)

func newMetaV1Time(t time.Time) metav1.Time {
	metaTime := metav1.NewTime(t)
	return metaTime
}

func makeResource(resMap map[v1.ResourceName]string) v1.ResourceList {
	res := v1.ResourceList{}
	for k, v := range resMap {
		res[k] = resource.MustParse(v)
	}
	return res
}

func TestNodeMetricEstimatorValidateNode(t *testing.T) {
	nodeAllocatable := makeResource(map[v1.ResourceName]string{v1.ResourceCPU: "100m", v1.ResourceMemory: "100"})
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
		name string
		cnr  *katalystv1alpha1.CustomNodeResource
		want *framework.Status
	}{
		{
			name: "cann't pass validate node because expired node metric",
			cnr: cnrWithNodeMetric(&katalystv1alpha1.NodeMetricStatus{
				UpdateTime: newMetaV1Time(now.Add(-60 * time.Second)),
			}),
			want: framework.NewStatus(framework.UnschedulableAndUnresolvable, "NodeMetricInfo has expired and cannot be used"),
		},
		{
			name: "cann't pass validate node because exceed usage threshold",
			cnr: cnrWithNodeMetric(&katalystv1alpha1.NodeMetricStatus{
				UpdateTime: newMetaV1Time(now.Add(-20 * time.Second)),
				NodeMetric: &katalystv1alpha1.NodeMetricInfo{
					ResourceUsage: katalystv1alpha1.ResourceUsage{
						GenericUsage: &katalystv1alpha1.ResourceMetric{
							CPU:    resource.NewMilliQuantity(90, resource.DecimalSI),
							Memory: resource.NewQuantity(90, resource.BinarySI),
						},
					},
				},
				GroupMetric: []katalystv1alpha1.GroupMetricInfo{
					{
						QoSLevel: string(util.ReclaimedCores), // BE
						ResourceUsage: katalystv1alpha1.ResourceUsage{
							GenericUsage: &katalystv1alpha1.ResourceMetric{
								CPU:    resource.NewMilliQuantity(90, resource.DecimalSI),
								Memory: resource.NewQuantity(90, resource.BinarySI),
							},
						},
						PodList: []string{},
					},
				},
			}),
			want: framework.NewStatus(framework.UnschedulableAndUnresolvable, "NodeMetricInfo usage exceeds threshold"),
		},
		{
			name: "pass validate node",
			cnr: cnrWithNodeMetric(&katalystv1alpha1.NodeMetricStatus{
				UpdateTime: newMetaV1Time(now.Add(-20 * time.Second)),
				NodeMetric: &katalystv1alpha1.NodeMetricInfo{
					ResourceUsage: katalystv1alpha1.ResourceUsage{
						GenericUsage: &katalystv1alpha1.ResourceMetric{
							CPU:    resource.NewMilliQuantity(80, resource.DecimalSI),
							Memory: resource.NewQuantity(80, resource.BinarySI),
						},
					},
				},
				GroupMetric: []katalystv1alpha1.GroupMetricInfo{
					{
						QoSLevel: string(util.ReclaimedCores), // BE
						ResourceUsage: katalystv1alpha1.ResourceUsage{
							GenericUsage: &katalystv1alpha1.ResourceMetric{
								CPU:    resource.NewMilliQuantity(80, resource.DecimalSI),
								Memory: resource.NewQuantity(80, resource.BinarySI),
							},
						},
						PodList: []string{},
					},
				},
			}),
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadAwareSchedulingArgs := config.LoadAwareArgs{
				Resources: []config.ResourceSpec{
					{
						Name:         string(v1.ResourceCPU),
						Weight:       1,
						ResourceType: podutil.BestEffortPod,
					},
					{
						Name:         string(v1.ResourceMemory),
						Weight:       1,
						ResourceType: podutil.BestEffortPod,
					},
				},
				FilterExpiredNodeMetrics:    true,
				NodeMetricExpirationSeconds: defaultNodeMetricExpirationSeconds,
				UsageThresholds:             defaultUsageThresholds,
				EstimatedScalingFactors:     defaultEstimatedScalingFactors,
			}

			schedulerCache := godelcache.New(commoncache.MakeCacheHandlerWrapper().
				ComponentName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				PodAssumedTTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				Obj())
			snapshot := godelcache.NewEmptySnapshot(commoncache.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				Obj())
			{
				// Prepare cache and snapshot.
				schedulerCache.AddNode(&v1.Node{ObjectMeta: metav1.ObjectMeta{Name: tt.cnr.Name}})
				schedulerCache.AddCNR(tt.cnr)
				schedulerCache.UpdateSnapshot(snapshot)
			}
			fh, _ := st.NewPodFrameworkHandle(nil, nil, nil, nil, schedulerCache, snapshot, nil, nil, nil, nil)

			estimator, err := NewNodeMetricEstimator(&loadAwareSchedulingArgs, fh)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			got := estimator.ValidateNode(snapshot.GetNodeInfo(defaultNodeName), podutil.BestEffortPod)
			if diff := cmp.Diff(got.Message(), tt.want.Message()); len(diff) > 0 {
				t.Errorf("Got diff: %v", diff)
			}
		})
	}
}

func TestNodeMetricEstimatorEstimatePod(t *testing.T) {
	tests := []struct {
		name string
		pod  *v1.Pod
		want *framework.Resource
	}{
		{
			name: "estimate empty be pod",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{podutil.PodResourceTypeAnnotationKey: string(podutil.BestEffortPod)},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:      "main",
							Resources: v1.ResourceRequirements{},
						},
					},
				},
			},
			want: &framework.Resource{
				MilliCPU: util.DefaultMilliCPURequest * 60 / 100,
				Memory:   util.DefaultMemoryRequest * 60 / 100,
			},
		},
		{
			name: "estimate empty gt pod",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod)},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name: "main",
							Resources: v1.ResourceRequirements{
								Limits: map[v1.ResourceName]resource.Quantity{
									v1.ResourceCPU:    resource.MustParse("1"),
									v1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: map[v1.ResourceName]resource.Quantity{
									v1.ResourceCPU:    resource.MustParse("1"),
									v1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
				},
			},
			want: &framework.Resource{},
		},
		{
			name: "estimate normal be pod",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{podutil.PodResourceTypeAnnotationKey: string(podutil.BestEffortPod)},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name: "main",
							Resources: v1.ResourceRequirements{
								Limits: map[v1.ResourceName]resource.Quantity{
									v1.ResourceCPU:    resource.MustParse("1"),
									v1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: map[v1.ResourceName]resource.Quantity{
									v1.ResourceCPU:    resource.MustParse("1"),
									v1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
				},
			},
			want: &framework.Resource{
				MilliCPU: 1000 * 60 / 100,
				Memory:   (1 << 30) * 60 / 100,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadAwareSchedulingArgs := config.LoadAwareArgs{
				Resources: []config.ResourceSpec{
					{
						Name:         string(v1.ResourceCPU),
						Weight:       1,
						ResourceType: podutil.BestEffortPod,
					},
					{
						Name:         string(v1.ResourceMemory),
						Weight:       1,
						ResourceType: podutil.BestEffortPod,
					},
				},
				FilterExpiredNodeMetrics:    true,
				NodeMetricExpirationSeconds: defaultNodeMetricExpirationSeconds,
				UsageThresholds:             defaultUsageThresholds,
				EstimatedScalingFactors:     defaultEstimatedScalingFactors,
			}
			snapshot := godelcache.NewEmptySnapshot(commoncache.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				Obj())
			fh, _ := st.NewPodFrameworkHandle(nil, nil, nil, nil, nil, snapshot, nil, nil, nil, nil)
			estimator, err := NewNodeMetricEstimator(&loadAwareSchedulingArgs, fh)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if diff := cmp.Diff(estimator.Name(), NodeMetricEstimatorName); len(diff) > 0 {
				t.Errorf("Got diff: %v", diff)
			}

			got, err := estimator.EstimatePod(tt.pod)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if diff := cmp.Diff(got, tt.want); len(diff) > 0 {
				t.Errorf("Got diff: %v", diff)
			}
		})
	}
}

func makeBasicPod(key string) *v1.Pod {
	p := testinghelper.MakePod().Name(key).UID(key).
		Annotation(podutil.PodResourceTypeAnnotationKey, string(podutil.BestEffortPod)).
		Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).Annotation(podutil.SchedulerAnnotationKey, "godel-scheduler").
		Annotation(podutil.AssumedNodeAnnotationKey, defaultNodeName).Req(map[v1.ResourceName]string{v1.ResourceCPU: "100m", v1.ResourceMemory: "100"}).
		Obj()
	return p
}

func TestNodeMetricEstimatorEstimateNode(t *testing.T) {
	/*
		pods on node: [p0, p1, p2, p3, p4] sorted by StartTime.
		pods in metrics: [p0, p2, (p5)] sorted by StartTime.
	*/
	p0 := makeBasicPod("p0")
	p1 := makeBasicPod("p1")
	p2 := makeBasicPod("p2")
	p3 := makeBasicPod("p3")
	p4 := makeBasicPod("p4")
	// p5 := makeBasicPod("p5", now.Add(-12*time.Second))
	podsOnNode := []*v1.Pod{p0, p1, p2, p3, p4}

	nodeAllocatable := makeResource(map[v1.ResourceName]string{v1.ResourceCPU: "100m", v1.ResourceMemory: "100"})
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
		name string
		cnr  *katalystv1alpha1.CustomNodeResource
		want *framework.Resource
	}{
		{
			name: "normal case",
			cnr: cnrWithNodeMetric(&katalystv1alpha1.NodeMetricStatus{
				UpdateTime: newMetaV1Time(now.Add(-9 * time.Second)),
				NodeMetric: &katalystv1alpha1.NodeMetricInfo{
					ResourceUsage: katalystv1alpha1.ResourceUsage{
						GenericUsage: &katalystv1alpha1.ResourceMetric{
							CPU:    resource.NewMilliQuantity(250, resource.DecimalSI),
							Memory: resource.NewQuantity(250, resource.BinarySI),
						},
					},
				},
				GroupMetric: []katalystv1alpha1.GroupMetricInfo{
					{
						QoSLevel: string(util.ReclaimedCores),
						ResourceUsage: katalystv1alpha1.ResourceUsage{
							GenericUsage: &katalystv1alpha1.ResourceMetric{
								CPU:    resource.NewMilliQuantity(250, resource.DecimalSI),
								Memory: resource.NewQuantity(250, resource.BinarySI),
							},
						},
						PodList: []string{"/p0", "/p2", "p5"},
					},
				},
			}),
			want: &framework.Resource{
				MilliCPU: 430,
				Memory:   430,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadAwareSchedulingArgs := config.LoadAwareArgs{
				Resources: []config.ResourceSpec{
					{
						Name:         string(v1.ResourceCPU),
						Weight:       1,
						ResourceType: podutil.BestEffortPod,
					},
					{
						Name:         string(v1.ResourceMemory),
						Weight:       1,
						ResourceType: podutil.BestEffortPod,
					},
				},
				FilterExpiredNodeMetrics:    true,
				NodeMetricExpirationSeconds: defaultNodeMetricExpirationSeconds,
				UsageThresholds:             defaultUsageThresholds,
				EstimatedScalingFactors:     defaultEstimatedScalingFactors,
			}

			schedulerCache := godelcache.New(commoncache.MakeCacheHandlerWrapper().
				ComponentName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				PodAssumedTTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				Obj())
			snapshot := godelcache.NewEmptySnapshot(commoncache.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				Obj())
			{
				// Prepare cache and snapshot.
				schedulerCache.AddNode(&v1.Node{ObjectMeta: metav1.ObjectMeta{Name: tt.cnr.Name}})
				schedulerCache.AddCNR(tt.cnr)
				for _, p := range podsOnNode {
					schedulerCache.AddPod(p)
				}
				schedulerCache.UpdateSnapshot(snapshot)
			}

			fh, _ := st.NewPodFrameworkHandle(nil, nil, nil, nil, nil, snapshot, nil, nil, nil, nil)

			estimator, err := NewNodeMetricEstimator(&loadAwareSchedulingArgs, fh)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			got, err := estimator.EstimateNode(snapshot.GetNodeInfo(defaultNodeName), podutil.BestEffortPod)
			if err != nil {
				t.Errorf("Unexpected got err: %+v", err)
			}

			if diff := cmp.Diff(got, tt.want); len(diff) > 0 {
				t.Errorf("Got diff: %v", diff)
			}
		})
	}
}
