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

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	pgfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/fake"

	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util/controller"
	podAnnotations "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// TestPodBelongsToPartition verifies the partition filtering logic.
func TestPodBelongsToPartition(t *testing.T) {
	tests := []struct {
		name          string
		schedulerName string
		podAnnotation map[string]string
		expected      bool
	}{
		{
			name:          "empty scheduler name accepts all pods",
			schedulerName: "",
			podAnnotation: map[string]string{podAnnotations.SchedulerAnnotationKey: "scheduler-A"},
			expected:      true,
		},
		{
			name:          "empty scheduler name accepts pod without annotation",
			schedulerName: "",
			podAnnotation: map[string]string{},
			expected:      true,
		},
		{
			name:          "matching scheduler name",
			schedulerName: "scheduler-A",
			podAnnotation: map[string]string{podAnnotations.SchedulerAnnotationKey: "scheduler-A"},
			expected:      true,
		},
		{
			name:          "non-matching scheduler name",
			schedulerName: "scheduler-A",
			podAnnotation: map[string]string{podAnnotations.SchedulerAnnotationKey: "scheduler-B"},
			expected:      false,
		},
		{
			name:          "pod without scheduler annotation is filtered",
			schedulerName: "scheduler-A",
			podAnnotation: map[string]string{},
			expected:      false,
		},
		{
			name:          "pod with nil annotations is filtered",
			schedulerName: "scheduler-A",
			podAnnotation: nil,
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := &PodGroupController{schedulerName: tt.schedulerName}
			pod := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pod",
					Namespace:   "default",
					Annotations: tt.podAnnotation,
				},
			}
			if got := ctrl.podBelongsToPartition(pod); got != tt.expected {
				t.Errorf("podBelongsToPartition() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestPodGroupControllerOptions validates the options struct.
func TestPodGroupControllerOptions(t *testing.T) {
	opts := PodGroupControllerOptions{SchedulerName: "scheduler-A"}
	if opts.SchedulerName != "scheduler-A" {
		t.Errorf("expected SchedulerName 'scheduler-A', got %q", opts.SchedulerName)
	}

	emptyOpts := PodGroupControllerOptions{}
	if emptyOpts.SchedulerName != "" {
		t.Errorf("expected empty SchedulerName, got %q", emptyOpts.SchedulerName)
	}
}

// makePodsWithScheduler creates test pods with the scheduler annotation set.
func makePodsWithScheduler(podNames []string, pgName, nodeName, schedulerName string) []*v1.Pod {
	pds := make([]*v1.Pod, 0)
	trueP := true
	for _, name := range podNames {
		pod := testinghelper.MakePod().Namespace("default").Name(name).Obj()
		pod.Labels = map[string]string{podAnnotations.PodGroupNameAnnotationKey: pgName}
		pod.Annotations = map[string]string{
			podAnnotations.PodGroupNameAnnotationKey: pgName,
		}
		if schedulerName != "" {
			pod.Annotations[podAnnotations.SchedulerAnnotationKey] = schedulerName
		}
		pod.Spec.NodeName = nodeName
		pod.OwnerReferences = append(pod.OwnerReferences, metav1.OwnerReference{
			APIVersion:         jobOwnerApiVersion,
			Kind:               jobOwnerKind,
			Name:               jobOwnerName,
			Controller:         &trueP,
			BlockOwnerDeletion: &trueP,
		})
		pds = append(pds, pod)
	}
	return pds
}

// TestPodGroupPartitionFilter_AllPodsMatch tests that when all pods belong to
// the scheduler's partition, the PodGroup transitions normally.
func TestPodGroupPartitionFilter_AllPodsMatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	pgName := "pg-partition-all-match"
	schedulerName := "scheduler-A"

	// Create 2 pods both belonging to scheduler-A
	ps := makePodsWithScheduler([]string{"pod1", "pod2"}, pgName, "", schedulerName)
	kubeClient := fake.NewSimpleClientset(ps[0], ps[1])

	pg := makePG(pgName, 2, v1alpha1.PodGroupPending, nil, nil)
	pgClient := pgfake.NewSimpleClientset(pg)

	pgInformerFactory := crdinformers.NewSharedInformerFactory(pgClient, controller.NoResyncPeriodFunc())
	pgInformer := pgInformerFactory.Scheduling().V1alpha1().PodGroups()

	SetupPodGroupControllerWithOptions(ctx, kubeClient, pgClient, pgInformer,
		PodGroupControllerOptions{SchedulerName: schedulerName},
	)

	pgInformerFactory.Start(ctx.Done())

	err := wait.Poll(200*time.Millisecond, 2*time.Second, func() (bool, error) {
		newPg, err := pgClient.SchedulingV1alpha1().PodGroups("default").Get(ctx, pgName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		// With 2 created pods and minMember=2, unscheduled → should move to PreScheduling
		if newPg.Status.Phase == v1alpha1.PodGroupPreScheduling {
			return true, nil
		}
		return false, fmt.Errorf("want PreScheduling, got %v", newPg.Status.Phase)
	})
	if err != nil {
		t.Fatal("Unexpected error:", err)
	}
}

// TestPodGroupPartitionFilter_NoPodsMatch tests that when no pods belong to
// the scheduler's partition, the PodGroup does not transition (stays Pending).
func TestPodGroupPartitionFilter_NoPodsMatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	pgName := "pg-partition-no-match"

	// Create 2 pods belonging to scheduler-B, but controller filters for scheduler-A
	ps := makePodsWithScheduler([]string{"pod1", "pod2"}, pgName, "", "scheduler-B")
	kubeClient := fake.NewSimpleClientset(ps[0], ps[1])

	pg := makePG(pgName, 2, v1alpha1.PodGroupPending, nil, nil)
	pgClient := pgfake.NewSimpleClientset(pg)

	pgInformerFactory := crdinformers.NewSharedInformerFactory(pgClient, controller.NoResyncPeriodFunc())
	pgInformer := pgInformerFactory.Scheduling().V1alpha1().PodGroups()

	SetupPodGroupControllerWithOptions(ctx, kubeClient, pgClient, pgInformer,
		PodGroupControllerOptions{SchedulerName: "scheduler-A"},
	)

	pgInformerFactory.Start(ctx.Done())

	// Wait a bit and verify PodGroup stays Pending (0 pods match partition)
	time.Sleep(600 * time.Millisecond)

	newPg, err := pgClient.SchedulingV1alpha1().PodGroups("default").Get(ctx, pgName, metav1.GetOptions{})
	if err != nil {
		t.Fatal("Failed to get PodGroup:", err)
	}
	if newPg.Status.Phase != v1alpha1.PodGroupPending {
		t.Errorf("expected PodGroup to stay Pending (no pods match partition), got %v", newPg.Status.Phase)
	}
}

// TestPodGroupPartitionFilter_MixedPods tests that when pods belong to
// different schedulers, only the matching ones are counted.
func TestPodGroupPartitionFilter_MixedPods(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	pgName := "pg-partition-mixed"
	schedulerName := "scheduler-A"

	// Create 3 pods: 2 for scheduler-A, 1 for scheduler-B
	// minMember = 3, so with only 2 matching pods, we get CreatedUnsatisfied → Pending
	podsA := makePodsWithScheduler([]string{"pod1", "pod2"}, pgName, "", schedulerName)
	podsB := makePodsWithScheduler([]string{"pod3"}, pgName, "", "scheduler-B")

	kubeClient := fake.NewSimpleClientset(podsA[0], podsA[1], podsB[0])

	pg := makePG(pgName, 3, v1alpha1.PodGroupPending, nil, nil)
	pgClient := pgfake.NewSimpleClientset(pg)

	pgInformerFactory := crdinformers.NewSharedInformerFactory(pgClient, controller.NoResyncPeriodFunc())
	pgInformer := pgInformerFactory.Scheduling().V1alpha1().PodGroups()

	SetupPodGroupControllerWithOptions(ctx, kubeClient, pgClient, pgInformer,
		PodGroupControllerOptions{SchedulerName: schedulerName},
	)

	pgInformerFactory.Start(ctx.Done())

	// With minMember=3 but only 2 pods matching, should stay Pending
	time.Sleep(600 * time.Millisecond)

	newPg, err := pgClient.SchedulingV1alpha1().PodGroups("default").Get(ctx, pgName, metav1.GetOptions{})
	if err != nil {
		t.Fatal("Failed to get PodGroup:", err)
	}
	if newPg.Status.Phase != v1alpha1.PodGroupPending {
		t.Errorf("expected PodGroup to stay Pending (only 2 of 3 pods match), got %v", newPg.Status.Phase)
	}
}

// TestPodGroupPartitionFilter_NoFilter tests backward compatibility:
// when SchedulerName is empty (standalone mode), all pods are counted.
func TestPodGroupPartitionFilter_NoFilter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	pgName := "pg-no-filter"

	// Create 2 pods with different schedulers
	ps1 := makePodsWithScheduler([]string{"pod1"}, pgName, "", "scheduler-A")
	ps2 := makePodsWithScheduler([]string{"pod2"}, pgName, "", "scheduler-B")
	kubeClient := fake.NewSimpleClientset(ps1[0], ps2[0])

	pg := makePG(pgName, 2, v1alpha1.PodGroupPending, nil, nil)
	pgClient := pgfake.NewSimpleClientset(pg)

	pgInformerFactory := crdinformers.NewSharedInformerFactory(pgClient, controller.NoResyncPeriodFunc())
	pgInformer := pgInformerFactory.Scheduling().V1alpha1().PodGroups()

	// No partition filter (empty SchedulerName)
	SetupPodGroupControllerWithOptions(ctx, kubeClient, pgClient, pgInformer,
		PodGroupControllerOptions{},
	)

	pgInformerFactory.Start(ctx.Done())

	err := wait.Poll(200*time.Millisecond, 2*time.Second, func() (bool, error) {
		newPg, err := pgClient.SchedulingV1alpha1().PodGroups("default").Get(ctx, pgName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		// Without filter, both pods count → 2 >= minMember(2) → PreScheduling
		if newPg.Status.Phase == v1alpha1.PodGroupPreScheduling {
			return true, nil
		}
		return false, fmt.Errorf("want PreScheduling, got %v", newPg.Status.Phase)
	})
	if err != nil {
		t.Fatal("Unexpected error:", err)
	}
}

// TestPodGroupPartitionFilter_ScheduledPodsMatch tests that partition filtering
// correctly counts bound pods toward the scheduled count.
func TestPodGroupPartitionFilter_ScheduledPodsMatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	pgName := "pg-partition-scheduled"
	schedulerName := "scheduler-A"

	// Create 2 pods for scheduler-A, both bound to a node
	ps := makePodsWithScheduler([]string{"pod1", "pod2"}, pgName, "node-1", schedulerName)
	kubeClient := fake.NewSimpleClientset(ps[0], ps[1])

	pg := makePG(pgName, 2, v1alpha1.PodGroupPreScheduling, nil, nil)
	pgClient := pgfake.NewSimpleClientset(pg)

	pgInformerFactory := crdinformers.NewSharedInformerFactory(pgClient, controller.NoResyncPeriodFunc())
	pgInformer := pgInformerFactory.Scheduling().V1alpha1().PodGroups()

	SetupPodGroupControllerWithOptions(ctx, kubeClient, pgClient, pgInformer,
		PodGroupControllerOptions{SchedulerName: schedulerName},
	)

	pgInformerFactory.Start(ctx.Done())

	err := wait.Poll(200*time.Millisecond, 2*time.Second, func() (bool, error) {
		newPg, err := pgClient.SchedulingV1alpha1().PodGroups("default").Get(ctx, pgName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		// Both pods bound → scheduled=2 >= minMember=2 → Scheduled
		if newPg.Status.Phase == v1alpha1.PodGroupScheduled {
			return true, nil
		}
		return false, fmt.Errorf("want Scheduled, got %v", newPg.Status.Phase)
	})
	if err != nil {
		t.Fatal("Unexpected error:", err)
	}
}

// TestSetupPodGroupController_BackwardCompatible verifies the original
// SetupPodGroupController function still works correctly (no partition filter).
func TestSetupPodGroupController_BackwardCompatible(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	pgName := "pg-compat"

	ps := makePodsWithScheduler([]string{"pod1", "pod2"}, pgName, "", "scheduler-A")
	kubeClient := fake.NewSimpleClientset(ps[0], ps[1])

	pg := makePG(pgName, 2, v1alpha1.PodGroupPending, nil, nil)
	pgClient := pgfake.NewSimpleClientset(pg)

	pgInformerFactory := crdinformers.NewSharedInformerFactory(pgClient, controller.NoResyncPeriodFunc())
	pgInformer := pgInformerFactory.Scheduling().V1alpha1().PodGroups()

	// Use the original function (no options)
	SetupPodGroupController(ctx, kubeClient, pgClient, pgInformer)

	pgInformerFactory.Start(ctx.Done())

	err := wait.Poll(200*time.Millisecond, 2*time.Second, func() (bool, error) {
		newPg, err := pgClient.SchedulingV1alpha1().PodGroups("default").Get(ctx, pgName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if newPg.Status.Phase == v1alpha1.PodGroupPreScheduling {
			return true, nil
		}
		return false, fmt.Errorf("want PreScheduling, got %v", newPg.Status.Phase)
	})
	if err != nil {
		t.Fatal("Unexpected error:", err)
	}
}
