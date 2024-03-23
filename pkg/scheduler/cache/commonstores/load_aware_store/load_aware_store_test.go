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

package loadawarestore

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"
	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
)

// pods on node: [p0, p1, p2, p3, p4].
// pods in metrics: [p0, p2, (p5)].

var (
	nodeName = "n"

	now = time.Now()

	p0 = testinghelper.MakePod().Name("p0").UID("p0").
		Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).Annotation(podutil.SchedulerAnnotationKey, "godel-scheduler").
		Annotation(podutil.AssumedNodeAnnotationKey, nodeName).Req(map[v1.ResourceName]string{v1.ResourceCPU: "100m", v1.ResourceMemory: "100"}).
		Obj()
	p1 = testinghelper.MakePod().Name("p1").UID("p1").
		Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).Annotation(podutil.SchedulerAnnotationKey, "godel-scheduler").
		Annotation(podutil.AssumedNodeAnnotationKey, nodeName).Req(map[v1.ResourceName]string{v1.ResourceCPU: "100m", v1.ResourceMemory: "100"}).
		Obj()
	p2 = testinghelper.MakePod().Name("p2").UID("p2").
		Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).Annotation(podutil.SchedulerAnnotationKey, "godel-scheduler").
		Annotation(podutil.AssumedNodeAnnotationKey, nodeName).Req(map[v1.ResourceName]string{v1.ResourceCPU: "100m", v1.ResourceMemory: "100"}).
		Obj()
	p3 = testinghelper.MakePod().Name("p3").UID("p3").
		Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).Annotation(podutil.SchedulerAnnotationKey, "godel-scheduler").
		Annotation(podutil.AssumedNodeAnnotationKey, nodeName).Req(map[v1.ResourceName]string{v1.ResourceCPU: "100m", v1.ResourceMemory: "100"}).
		Obj()
	p4 = testinghelper.MakePod().Name("p4").UID("p4").
		Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).Annotation(podutil.SchedulerAnnotationKey, "godel-scheduler").
		Annotation(podutil.AssumedNodeAnnotationKey, nodeName).Req(map[v1.ResourceName]string{v1.ResourceCPU: "100m", v1.ResourceMemory: "100"}).
		Obj()
	p5 = testinghelper.MakePod().Name("p5").UID("p5").
		Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).Annotation(podutil.SchedulerAnnotationKey, "godel-scheduler").
		Annotation(podutil.AssumedNodeAnnotationKey, nodeName).Req(map[v1.ResourceName]string{v1.ResourceCPU: "100m", v1.ResourceMemory: "100"}).
		Obj()

	resourceUsage = katalystv1alpha1.ResourceUsage{
		GenericUsage: &katalystv1alpha1.ResourceMetric{
			CPU:    resource.NewMilliQuantity(250, resource.DecimalSI), // 250m
			Memory: resource.NewQuantity(250, resource.BinarySI),
		},
	}

	cnr = &katalystv1alpha1.CustomNodeResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
		Status: katalystv1alpha1.CustomNodeResourceStatus{
			NodeMetricStatus: &katalystv1alpha1.NodeMetricStatus{
				UpdateTime: newMetaV1Time(now.Add(-9 * time.Second)),
				NodeMetric: &katalystv1alpha1.NodeMetricInfo{ResourceUsage: resourceUsage},
				GroupMetric: []katalystv1alpha1.GroupMetricInfo{
					{
						QoSLevel:      string(util.DedicatedCores),
						ResourceUsage: resourceUsage,
						PodList:       []string{"/p0", "/p2", "/p5"},
					},
				},
			},
		},
	}
)

func newMetaV1Time(t time.Time) metav1.Time {
	metaTime := metav1.NewTime(t)
	return metaTime
}

func TestLoadAwareStore_UpdateSnapshot(t *testing.T) {
	AddPodOp, SubPodOp := "AddPod", "SubPodOp"
	AddNodeMetricOp, SubNodeMetricOp := "AddNodeMetric", "SubNodeMetric"

	type args struct {
		pod *v1.Pod
		cnr *katalystv1alpha1.CustomNodeResource
	}
	type operation struct {
		op   string
		args args
	}
	tests := []struct {
		name         string
		existingPods []*v1.Pod
		operations   []operation
		expect       *framework.LoadAwareNodeUsage
	}{
		{
			name:         "add node metrics when there are existing pods",
			existingPods: []*v1.Pod{p0, p1, p2, p3, p4},
			operations: []operation{
				{
					op:   AddNodeMetricOp,
					args: args{cnr: cnr},
				},
			},
			expect: &framework.LoadAwareNodeUsage{
				RequestMilliCPU: 300,
				RequestMEM:      300,
				ProfileMilliCPU: 250,
				ProfileMEM:      250,
			},
		},
		{
			name:         "add pods when there are not existing pods",
			existingPods: []*v1.Pod{},
			operations: []operation{
				{
					op:   AddNodeMetricOp,
					args: args{cnr: cnr},
				},
				{
					op:   AddPodOp,
					args: args{pod: p0},
				},
				{
					op:   AddPodOp,
					args: args{pod: p1},
				},
				{
					op:   AddPodOp,
					args: args{pod: p2},
				},
				{
					op:   AddPodOp,
					args: args{pod: p3},
				},
				{
					op:   AddPodOp,
					args: args{pod: p4},
				},
			},
			expect: &framework.LoadAwareNodeUsage{
				RequestMilliCPU: 300,
				RequestMEM:      300,
				ProfileMilliCPU: 250,
				ProfileMEM:      250,
			},
		},
		{
			name:         "add pod when there are existing pods and existing metrics",
			existingPods: []*v1.Pod{p0, p1, p2, p3, p4},
			operations: []operation{
				{
					op:   AddNodeMetricOp,
					args: args{cnr: cnr},
				},
				{
					op:   AddPodOp,
					args: args{pod: p5},
				},
			},
			expect: &framework.LoadAwareNodeUsage{
				RequestMilliCPU: 300,
				RequestMEM:      300,
				ProfileMilliCPU: 250,
				ProfileMEM:      250,
			},
		},
		{
			name:         "add pod when there are existing pods and existing metrics",
			existingPods: []*v1.Pod{p0, p1, p2, p3, p4},
			operations: []operation{
				{
					op:   AddNodeMetricOp,
					args: args{cnr: cnr},
				},
				{
					op:   AddPodOp,
					args: args{pod: p5},
				},
				{
					op:   SubPodOp,
					args: args{pod: p1},
				},
			},
			expect: &framework.LoadAwareNodeUsage{
				RequestMilliCPU: 200,
				RequestMEM:      200,
				ProfileMilliCPU: 250,
				ProfileMEM:      250,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeInfo := framework.NewNodeInfo()
			handler := handler.MakeCacheHandlerWrapper().
				NodeHandler(func(s string) framework.NodeInfo { return nodeInfo }).Obj()
			cache := NewCache(handler)

			for _, p := range tt.existingPods {
				cache.AddPod(p)
				nodeInfo.AddPod(p)
			}

			for _, operation := range tt.operations {
				var err error
				switch operation.op {
				case AddPodOp:
					err = cache.AddPod(operation.args.pod)
					// Considering that nodestore is not used, it is necessary to manually update nodeinfo here.
					nodeInfo.AddPod(operation.args.pod)
				case SubPodOp:
					err = cache.RemovePod(operation.args.pod)
					// Considering that nodestore is not used, it is necessary to manually update nodeinfo here.
					nodeInfo.RemovePod(operation.args.pod, false)
				case AddNodeMetricOp:
					err = cache.AddCNR(operation.args.cnr)
				case SubNodeMetricOp:
					err = cache.RemoveCNR(operation.args.cnr)
				default:
					t.Errorf("invalid operation: %v", operation.op)
				}
				if err != nil {
					t.Errorf("Operation got error: %v", err)
				}
			}

			snapshot := NewSnapshot(handler)

			if err := cache.UpdateSnapshot(snapshot); err != nil {
				t.Errorf("LoadAwareStore.UpdateSnapshot() got error = %v", err)
			}

			storedNodeMetricInfo := snapshot.(*LoadAwareStore).Store.Get(nodeName).(*NodeMetricInfo)

			t.Log(storedNodeMetricInfo.gtPodMetricInfos.ProfilePods.List())

			gotUsage := snapshot.(*LoadAwareStore).GetLoadAwareNodeUsage(nodeName, podutil.GuaranteedPod)
			if diff := cmp.Diff(gotUsage, tt.expect); len(diff) > 0 {
				t.Errorf("GetLoadAwareNodeUsage got diff: %v\n", diff)
			}
		})
	}
}
