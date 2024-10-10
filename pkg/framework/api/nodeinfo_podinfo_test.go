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

package api

import (
	"math"
	"reflect"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	"github.com/google/go-cmp/cmp"
	"github.com/kubewharf/godel-scheduler/pkg/features"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func makePriority(priority int32) *int32 {
	return &priority
}

func TestPodInfoMaintainer_GetPods(t *testing.T) {
	t0 := time.Now()
	t1 := t0.Add(time.Hour)

	p0 := NewPodInfo(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p0",
			UID:  "p0",
			Annotations: map[string]string{
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
			},
		},
		Spec: v1.PodSpec{
			Priority: makePriority(0),
			NodeName: "node",
		},
		Status: v1.PodStatus{
			StartTime: &metav1.Time{t1},
		},
	})
	// p1 should be more important than p0 cause `Priority`.
	p1 := NewPodInfo(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
			UID:  "p1",
			Annotations: map[string]string{
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
			},
		},
		Spec: v1.PodSpec{
			Priority: makePriority(5),
			NodeName: "node",
		},
		Status: v1.PodStatus{
			StartTime: &metav1.Time{t1},
		},
	})
	// p2 should be more important than p1 cause `StartTime`.
	p2 := NewPodInfo(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p2",
			UID:  "p2",
			Annotations: map[string]string{
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
			},
		},
		Spec: v1.PodSpec{
			Priority: makePriority(5),
			NodeName: "node",
		},
		Status: v1.PodStatus{
			StartTime: &metav1.Time{t0},
		},
	})
	// p3 should be more important than p8 cause `StartTime`.
	p3 := NewPodInfo(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p3",
			UID:  "p3",
			Annotations: map[string]string{
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
			},
		},
		Spec: v1.PodSpec{
			Priority: makePriority(5),
			NodeName: "node",
		},
		Status: v1.PodStatus{
			StartTime: &metav1.Time{t0},
		},
	})
	// p4 should be more important than p3 cause `PodKey`.
	p4 := NewPodInfo(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pa",
			UID:  "pa",
			Annotations: map[string]string{
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
			},
		},
		Spec: v1.PodSpec{
			Priority: makePriority(5),
			NodeName: "node",
		},
		Status: v1.PodStatus{
			StartTime: &metav1.Time{t0},
		},
	})

	tests := []struct {
		name string
		pods []*PodInfo
		want []*PodInfo
	}{
		{
			name: "test",
			pods: []*PodInfo{p4, p2, p0, p3, p1},
			want: []*PodInfo{p0, p1, p2, p3, p4},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i := 0; i < len(tt.want); i++ {
				for j := 0; j < i; j++ {
					if !tt.want[i].Compare(tt.want[j]) {
						t.Errorf("PodInfo.Compare got false for pod: %v, %v", tt.want[i].PodKey, tt.want[j].PodKey)
					}
				}
			}

			m := NewPodInfoMaintainer(tt.pods...)

			t.Logf(m.gtPodsMayBePreempted.PrintTree())
			if diff := cmp.Diff(m.GetPods(), tt.want); diff != "" {
				t.Errorf("PodInfoMaintainer.GetPods() diff = %v", diff)
			}
		})
	}
}

func TestGetMaintainableInfoByPartition(t *testing.T) {
	makePod := func(name string, priority int32, req v1.ResourceList) *v1.Pod {
		return &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				UID:  types.UID(name),
			},
			Spec: v1.PodSpec{
				Priority: &priority,
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: req,
						},
					},
				},
				NodeName: "node",
			},
		}
	}

	tests := []struct {
		name                   string
		pods                   []*PodInfo
		expectedMaintainerInfo PodMaintainableInfo
	}{
		{
			name: "could not get pod with the same priority",
			pods: []*PodInfo{
				NewPodInfo(makePod("p1", 10, v1.ResourceList{v1.ResourceCPU: resource.MustParse("1")})),
				NewPodInfo(makePod("p2", 11, v1.ResourceList{v1.ResourceCPU: resource.MustParse("2")})),
				NewPodInfo(makePod("p3", 9, v1.ResourceList{v1.ResourceCPU: resource.MustParse("4")})),
			},
			expectedMaintainerInfo: PodMaintainableInfo{
				PodInfo:  NewPodInfo(makePod("p3", 9, v1.ResourceList{v1.ResourceCPU: resource.MustParse("4")})),
				MilliCPU: 4000,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewPodInfoMaintainer(tt.pods...)
			gotInfo := m.GetMaintainableInfoByPartition(NewPartitionInfo(math.MinInt64, 10, podutil.GuaranteedPod))
			if !reflect.DeepEqual(tt.expectedMaintainerInfo, gotInfo) {
				t.Errorf("expected info: %v, but got: %v", tt.expectedMaintainerInfo, gotInfo)
			}
		})
	}
}

func TestGetPrioritiesForPodsMayBePreempted(t *testing.T) {
	p0 := NewPodInfo(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p0",
			UID:  "p0",
			Annotations: map[string]string{
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
			},
		},
		Spec: v1.PodSpec{
			Priority: makePriority(0),
			NodeName: "node",
		},
	})

	p1 := NewPodInfo(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
			UID:  "p1",
			Annotations: map[string]string{
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
			},
		},
		Spec: v1.PodSpec{
			Priority: makePriority(10),
			NodeName: "node",
		},
	})

	p2 := NewPodInfo(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p2",
			UID:  "p2",
			Annotations: map[string]string{
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
				podutil.AssumedNodeAnnotationKey:     "node",
			},
		},
		Spec: v1.PodSpec{
			Priority: makePriority(20),
		},
	})

	p3 := NewPodInfo(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p3",
			UID:  "p3",
			Annotations: map[string]string{
				podutil.PodResourceTypeAnnotationKey: string(podutil.BestEffortPod),
			},
		},
		Spec: v1.PodSpec{
			Priority: makePriority(30),
			NodeName: "node",
		},
	})

	p4 := NewPodInfo(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p4",
			UID:  "p4",
			Annotations: map[string]string{
				podutil.PodResourceTypeAnnotationKey: string(podutil.BestEffortPod),
			},
		},
		Spec: v1.PodSpec{
			Priority: makePriority(40),
			NodeName: "node",
		},
	})

	p5 := NewPodInfo(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p5",
			UID:  "p5",
			Annotations: map[string]string{
				podutil.PodResourceTypeAnnotationKey: string(podutil.BestEffortPod),
				podutil.AssumedNodeAnnotationKey:     "node",
			},
		},
		Spec: v1.PodSpec{
			Priority: makePriority(50),
		},
	})

	// check GuaranteedPod
	{
		m := NewPodInfoMaintainer(p0, p2, p3, p5)
		m = m.Clone()
		priorities := sets.NewInt()
		for _, priority := range m.GetPrioritiesForPodsMayBePreempted(podutil.GuaranteedPod) {
			priorities.Insert(int(priority))
		}
		expectedPriorities := sets.NewInt(0)
		if !reflect.DeepEqual(m.prioritiesForGTPodsMayBePreempted, map[int64]int{0: 1}) {
			t.Errorf("got: %v", m.prioritiesForGTPodsMayBePreempted)
		}
		if !expectedPriorities.Equal(priorities) {
			t.Errorf("expected: %v, but got: %v", expectedPriorities, priorities)
		}
		m.AddPodInfo(p1)
		m = m.Clone()
		priorities = sets.NewInt()
		for _, priority := range m.GetPrioritiesForPodsMayBePreempted(podutil.GuaranteedPod) {
			priorities.Insert(int(priority))
		}
		expectedPriorities.Insert(10)
		if !reflect.DeepEqual(m.prioritiesForGTPodsMayBePreempted, map[int64]int{0: 1, 10: 1}) {
			t.Errorf("got: %v", m.prioritiesForGTPodsMayBePreempted)
		}
		if !expectedPriorities.Equal(priorities) {
			t.Errorf("expected: %v, but got: %v", expectedPriorities, priorities)
		}
		m.RemovePodInfo(p0)
		m = m.Clone()
		priorities = sets.NewInt()
		for _, priority := range m.GetPrioritiesForPodsMayBePreempted(podutil.GuaranteedPod) {
			priorities.Insert(int(priority))
		}
		expectedPriorities.Delete(0)
		if !reflect.DeepEqual(m.prioritiesForGTPodsMayBePreempted, map[int64]int{10: 1}) {
			t.Errorf("got: %v", m.prioritiesForGTPodsMayBePreempted)
		}
		if !expectedPriorities.Equal(priorities) {
			t.Errorf("expected: %v, but got: %v", expectedPriorities, priorities)
		}
	}

	// check BestEffortPod
	{
		m := NewPodInfoMaintainer(p0, p2, p3, p5)
		m = m.Clone()
		priorities := sets.NewInt()
		for _, priority := range m.GetPrioritiesForPodsMayBePreempted(podutil.BestEffortPod) {
			priorities.Insert(int(priority))
		}
		expectedPriorities := sets.NewInt(30)
		if !reflect.DeepEqual(m.prioritiesForBEPodsMayBePreempted, map[int64]int{30: 1}) {
			t.Errorf("got: %v", m.prioritiesForBEPodsMayBePreempted)
		}
		if !expectedPriorities.Equal(priorities) {
			t.Errorf("expected: %v, but got: %v", expectedPriorities, priorities)
		}
		m.AddPodInfo(p4)
		m = m.Clone()
		priorities = sets.NewInt()
		for _, priority := range m.GetPrioritiesForPodsMayBePreempted(podutil.BestEffortPod) {
			priorities.Insert(int(priority))
		}
		expectedPriorities.Insert(40)
		if !reflect.DeepEqual(m.prioritiesForBEPodsMayBePreempted, map[int64]int{30: 1, 40: 1}) {
			t.Errorf("got: %v", m.prioritiesForBEPodsMayBePreempted)
		}
		if !expectedPriorities.Equal(priorities) {
			t.Errorf("expected: %v, but got: %v", expectedPriorities, priorities)
		}
		m.RemovePodInfo(p3)
		m = m.Clone()
		priorities = sets.NewInt()
		for _, priority := range m.GetPrioritiesForPodsMayBePreempted(podutil.BestEffortPod) {
			priorities.Insert(int(priority))
		}
		expectedPriorities.Delete(30)
		if !reflect.DeepEqual(m.prioritiesForBEPodsMayBePreempted, map[int64]int{40: 1}) {
			t.Errorf("got: %v", m.prioritiesForBEPodsMayBePreempted)
		}
		if !expectedPriorities.Equal(priorities) {
			t.Errorf("expected: %v, but got: %v", expectedPriorities, priorities)
		}
	}
}

func TestGuaranteedReservedResource(t *testing.T) {
	const fakeScalarResource = "kubernetes.io/fake-resource"

	nodeName := "test-node"
	pods := []*v1.Pod{
		makeBasePod(t, nodeName, "test-1", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}),
		makeBasePod(t, nodeName, "test-2", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}),
		makeBasePod(t, nodeName, "test-2", "200m", "1Ki", "kubernetes.io/fake-resource:1", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}),
	}
	pods[2].Annotations = map[string]string{
		podutil.ReservationPlaceHolderPodAnnotation: podutil.IsReservationPlaceHolderPods,
	}

	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(features.ResourceReservation): true})

	nodeInfo := NewNodeInfo()
	nodeInfo.AddPod(pods[0])
	resources := nodeInfo.GetGuaranteedReserved()
	expected := &Resource{}
	if !reflect.DeepEqual(resources, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, resources)
	}

	// add reserved pod
	nodeInfo.AddPod(pods[2])
	resources = nodeInfo.GetGuaranteedReserved()
	expected = &Resource{
		MilliCPU: 200,
		Memory:   1024,
		ScalarResources: map[v1.ResourceName]int64{
			fakeScalarResource: 1,
		},
	}
	if !reflect.DeepEqual(resources, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, resources)
	}

	// delete common pod
	nodeInfo.RemovePod(pods[0], false)
	resources = nodeInfo.GetGuaranteedReserved()
	expected = &Resource{
		MilliCPU: 200,
		Memory:   1024,
		ScalarResources: map[v1.ResourceName]int64{
			fakeScalarResource: 1,
		},
	}
	if !reflect.DeepEqual(resources, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, resources)
	}

	// delete reserved pod
	nodeInfo.RemovePod(pods[2], false)
	resources = nodeInfo.GetGuaranteedReserved()
	expected = &Resource{
		ScalarResources: map[v1.ResourceName]int64{
			fakeScalarResource: 0,
		},
	}
	if !reflect.DeepEqual(resources, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, resources)
	}
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(features.ResourceReservation): false})
}

func TestPodNumChangeInReservation(t *testing.T) {
	nodeName := "test-node"
	pods := []*v1.Pod{
		makeBasePod(t, nodeName, "test-1", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}),
		makeBasePod(t, nodeName, "test-2", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}),
		makeBasePod(t, nodeName, "test-3", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}),
	}
	pods[2].Annotations = map[string]string{
		podutil.ReservationPlaceHolderPodAnnotation: podutil.IsReservationPlaceHolderPods,
	}

	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(features.ResourceReservation): true})

	nodeInfo := NewNodeInfo()
	nodeInfo.AddPod(pods[0])
	resources := nodeInfo.GetGuaranteedReserved()
	expected := &Resource{}
	if !reflect.DeepEqual(resources, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, resources)
	}
	if nodeInfo.NumPods() != 1 {
		t.Errorf("expected pods num %v, got %v", 1, nodeInfo.NumPods())
	}
	if nodeInfo.NumPlaceholderPods() != 0 {
		t.Errorf("expected pods num %v, got %v", 0, nodeInfo.NumPlaceholderPods())
	}

	// add reserved pod
	nodeInfo.AddPod(pods[2])
	resources = nodeInfo.GetGuaranteedReserved()
	expected = &Resource{
		MilliCPU: 200,
		Memory:   1024,
	}
	if !reflect.DeepEqual(resources, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, resources)
	}
	if nodeInfo.NumPods() != 2 {
		t.Errorf("expected pods num %v, got %v", 2, nodeInfo.NumPods())
	}
	if nodeInfo.NumPlaceholderPods() != 1 {
		t.Errorf("expected pods num %v, got %v", 1, nodeInfo.NumPlaceholderPods())
	}

	// delete common pod
	nodeInfo.RemovePod(pods[0], false)
	resources = nodeInfo.GetGuaranteedReserved()
	expected = &Resource{
		MilliCPU: 200,
		Memory:   1024,
	}
	if !reflect.DeepEqual(resources, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, resources)
	}
	if nodeInfo.NumPods() != 1 {
		t.Errorf("expected pods num %v, got %v", 1, nodeInfo.NumPods())
	}
	if nodeInfo.NumPlaceholderPods() != 1 {
		t.Errorf("expected pods num %v, got %v", 1, nodeInfo.NumPlaceholderPods())
	}

	// delete reserved pod
	nodeInfo.RemovePod(pods[2], false)
	resources = nodeInfo.GetGuaranteedReserved()
	expected = &Resource{}
	if !reflect.DeepEqual(resources, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, resources)
	}
	if nodeInfo.NumPods() != 0 {
		t.Errorf("expected pods num %v, got %v", 0, nodeInfo.NumPods())
	}
	if nodeInfo.NumPlaceholderPods() != 0 {
		t.Errorf("expected pods num %v, got %v", 0, nodeInfo.NumPlaceholderPods())
	}
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(features.ResourceReservation): false})
}

func TestResourceChangeInReservation(t *testing.T) {
	nodeName := "test-node"
	pods := []*v1.Pod{
		makeBasePod(t, nodeName, "test-1", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}),
		makeBasePod(t, nodeName, "test-2", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}),
		makeBasePod(t, nodeName, "test-3", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}),
	}
	pods[2].Annotations = map[string]string{
		podutil.ReservationPlaceHolderPodAnnotation: podutil.IsReservationPlaceHolderPods,
	}

	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(features.ResourceReservation): true})

	nodeInfo := NewNodeInfo()
	nodeInfo.AddPod(pods[0])
	resources := nodeInfo.GetGuaranteedRequested()
	expected := &Resource{
		MilliCPU: 100,
		Memory:   500,
	}
	if !reflect.DeepEqual(resources, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, resources)
	}

	// add reserved pod
	nodeInfo.AddPod(pods[2])
	resources = nodeInfo.GetGuaranteedRequested()
	expected = &Resource{
		MilliCPU: 300,
		Memory:   1524,
	}
	if !reflect.DeepEqual(resources, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, resources)
	}

	// delete common pod
	nodeInfo.RemovePod(pods[0], false)
	resources = nodeInfo.GetGuaranteedRequested()
	expected = &Resource{
		MilliCPU: 200,
		Memory:   1024,
	}
	if !reflect.DeepEqual(resources, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, resources)
	}

	// delete reserved pod
	nodeInfo.RemovePod(pods[2], false)
	resources = nodeInfo.GetGuaranteedRequested()
	expected = &Resource{}
	if !reflect.DeepEqual(resources, expected) {
		t.Errorf("expected: %#v, got: %#v", expected, resources)
	}
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(features.ResourceReservation): false})
}
