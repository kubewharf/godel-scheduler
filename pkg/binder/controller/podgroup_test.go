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
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"

	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util/controller"
	podAnnotations "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	jobOwnerApiVersion = "batch.volcano.sh/v1alpha1"
	jobOwnerKind       = "Job"
	jobOwnerName       = "trialrun-3961258"
)

func Test_PodGroupRun(t *testing.T) {
	ctx := context.TODO()
	createTimeForCustomize := metav1.Time{Time: time.Now().Add(-301 * time.Second)} // custom timeout is 300s
	createTimeForDefault := metav1.Time{Time: time.Now().Add(-6 * time.Minute)}     // default is 5 min
	durationInSeconds := int32(300)
	noConditionChange := 0
	occupiedObjName := makeOccupiedObj(jobOwnerKind, "default", jobOwnerName)

	nodeName := "n"
	noNodeName := ""

	cases := []struct {
		name                   string
		pgName                 string
		minMember              uint32
		podNames               []string
		podPhase               v1.PodPhase
		podPhaseOverride       v1.PodPhase
		podNodeName            *string
		podNodeNameOverride    *string
		previousPhase          v1alpha1.PodGroupPhase
		desiredGroupPhase      v1alpha1.PodGroupPhase
		podGroupCreateTime     *metav1.Time
		scheduleTimeoutSeconds *int32
		desiredConditionDelta  *int
		desiredOccupiedObjName *string
	}{
		{
			name:              "Group status empty and not enough pods created",
			pgName:            "pg0",
			minMember:         3,
			podNames:          []string{"pod1", "pod2"},
			podNodeName:       &noNodeName,
			previousPhase:     "",
			desiredGroupPhase: v1alpha1.PodGroupPending,
		},
		{
			name:                  "Group pending and not enough pods created",
			pgName:                "pg1",
			minMember:             3,
			podNames:              []string{"pod1", "pod2"},
			podNodeName:           &noNodeName,
			previousPhase:         v1alpha1.PodGroupPending,
			desiredGroupPhase:     v1alpha1.PodGroupPending,
			desiredConditionDelta: &noConditionChange,
		},
		{
			name:                   "Group prescheduling",
			pgName:                 "pg2",
			minMember:              2,
			podNames:               []string{"pod1", "pod2"},
			podNodeName:            &noNodeName,
			previousPhase:          v1alpha1.PodGroupPending,
			desiredGroupPhase:      v1alpha1.PodGroupPreScheduling,
			desiredOccupiedObjName: &occupiedObjName,
		},
		{
			name:                  "Group prescheduling even one pod bound",
			pgName:                "pg3",
			minMember:             2,
			podNames:              []string{"pod1", "pod2"},
			podNodeName:           &nodeName, // one pending, one bound
			podNodeNameOverride:   &noNodeName,
			previousPhase:         v1alpha1.PodGroupPreScheduling,
			desiredGroupPhase:     v1alpha1.PodGroupPreScheduling,
			desiredConditionDelta: &noConditionChange,
		},
		{
			name:              "Group scheduled cause min-member pods bound",
			pgName:            "pg4",
			minMember:         2,
			podNames:          []string{"pod1", "pod2"},
			podNodeName:       &nodeName,
			previousPhase:     v1alpha1.PodGroupPreScheduling,
			desiredGroupPhase: v1alpha1.PodGroupScheduled,
		},
		{
			name:              "Group scheduled cause one pod running",
			pgName:            "pg3",
			minMember:         2,
			podNames:          []string{"pod1", "pod2"},
			podNodeName:       &noNodeName, // one pending, one bound
			podPhase:          v1.PodPending,
			podPhaseOverride:  v1.PodRunning,
			previousPhase:     v1alpha1.PodGroupPreScheduling,
			desiredGroupPhase: v1alpha1.PodGroupScheduled,
		},
		{
			name:                   "Group group should timeout, not enough pods",
			pgName:                 "pg5",
			minMember:              3,
			podNames:               []string{"pod1", "pod2"},
			podNodeName:            &noNodeName,
			previousPhase:          v1alpha1.PodGroupPending,
			desiredGroupPhase:      v1alpha1.PodGroupTimeout,
			scheduleTimeoutSeconds: &durationInSeconds,
			podGroupCreateTime:     &createTimeForCustomize,
		},
		{
			name:                   "Group group should timeout, not enough resources",
			pgName:                 "pg6",
			minMember:              2,
			podNames:               []string{"pod1", "pod2"},
			podNodeName:            &noNodeName,
			previousPhase:          v1alpha1.PodGroupPreScheduling,
			desiredGroupPhase:      v1alpha1.PodGroupTimeout,
			scheduleTimeoutSeconds: &durationInSeconds,
			podGroupCreateTime:     &createTimeForCustomize,
		},
		{
			name:               "Group group should timeout, not enough pods (default timeout duration)",
			pgName:             "pg7",
			minMember:          3,
			podNames:           []string{"pod1", "pod2"},
			podNodeName:        &noNodeName,
			previousPhase:      v1alpha1.PodGroupPending,
			desiredGroupPhase:  v1alpha1.PodGroupTimeout,
			podGroupCreateTime: &createTimeForDefault,
		},
		{
			name:               "Group group should timeout, not enough resources (default timeout duration)",
			pgName:             "pg8",
			minMember:          2,
			podNames:           []string{"pod1", "pod2"},
			podNodeName:        &noNodeName,
			previousPhase:      v1alpha1.PodGroupPreScheduling,
			desiredGroupPhase:  v1alpha1.PodGroupTimeout,
			podGroupCreateTime: &createTimeForDefault,
		},
		{
			name:                   "Group group should not timeout, not enough pods but one pod running",
			pgName:                 "pg9",
			minMember:              3,
			podNames:               []string{"pod1", "pod2"},
			podNodeName:            &noNodeName,
			podPhase:               v1.PodPending,
			podPhaseOverride:       v1.PodRunning,
			previousPhase:          v1alpha1.PodGroupPending,
			desiredGroupPhase:      v1alpha1.PodGroupScheduled,
			scheduleTimeoutSeconds: &durationInSeconds,
			podGroupCreateTime:     &createTimeForCustomize,
		},
		{
			name:                   "Group group should not timeout, not enough resources but one pod running",
			pgName:                 "pg10",
			minMember:              2,
			podNames:               []string{"pod1", "pod2"},
			podNodeName:            &noNodeName,
			podPhase:               v1.PodPending,
			podPhaseOverride:       v1.PodRunning,
			previousPhase:          v1alpha1.PodGroupPreScheduling,
			desiredGroupPhase:      v1alpha1.PodGroupScheduled,
			scheduleTimeoutSeconds: &durationInSeconds,
			podGroupCreateTime:     &createTimeForCustomize,
		},
		{
			name:                   "Group group should not timeout, cause it has been scheduled before",
			pgName:                 "pg11",
			minMember:              3,
			podNames:               []string{"pod1", "pod2"},
			podNodeName:            &noNodeName,
			previousPhase:          v1alpha1.PodGroupScheduled,
			desiredGroupPhase:      v1alpha1.PodGroupScheduled,
			scheduleTimeoutSeconds: &durationInSeconds,
			podGroupCreateTime:     &createTimeForCustomize,
			desiredConditionDelta:  &noConditionChange,
		},
		{
			name:                  "Group group should not scheduled, cause it has been timeout before",
			pgName:                "pg12",
			minMember:             2,
			podNames:              []string{"pod1", "pod2"},
			podNodeName:           &nodeName,
			previousPhase:         v1alpha1.PodGroupTimeout,
			desiredGroupPhase:     v1alpha1.PodGroupTimeout,
			desiredConditionDelta: &noConditionChange,
		},
		{
			name:                  "Group group status remain same",
			pgName:                "pg13",
			minMember:             2,
			podNames:              []string{"pod1", "pod2"},
			podNodeName:           &noNodeName,
			previousPhase:         v1alpha1.PodGroupPreScheduling,
			desiredGroupPhase:     v1alpha1.PodGroupPreScheduling,
			desiredConditionDelta: &noConditionChange,
		},
		// Compatible Adaptation Testing
		{
			name:              "Group group running, should be scheduled directly anyway",
			pgName:            "pg20",
			minMember:         2,
			podNames:          []string{"pod1", "pod2"},
			podNodeName:       &noNodeName,
			previousPhase:     v1alpha1.PodGroupRunning,
			desiredGroupPhase: v1alpha1.PodGroupScheduled,
		},
		{
			name:              "Group group finished, should be scheduled directly",
			pgName:            "pg21",
			minMember:         2,
			podNames:          []string{"pod1", "pod2"},
			podNodeName:       &noNodeName,
			previousPhase:     v1alpha1.PodGroupFinished,
			desiredGroupPhase: v1alpha1.PodGroupScheduled,
		},
		{
			name:              "Group group failed, should be scheduled directly",
			pgName:            "pg22",
			minMember:         2,
			podNames:          []string{"pod1", "pod2"},
			podNodeName:       &noNodeName,
			previousPhase:     v1alpha1.PodGroupFailed,
			desiredGroupPhase: v1alpha1.PodGroupScheduled,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ps := makePods(c.podNames, c.pgName, *c.podNodeName)
			kubeClient := fake.NewSimpleClientset(ps[0], ps[1])
			pg := makePG(c.pgName, int32(c.minMember), c.previousPhase, c.scheduleTimeoutSeconds, c.podGroupCreateTime)
			pgClient := pgfake.NewSimpleClientset(pg)

			informerFactory := informers.NewSharedInformerFactory(kubeClient, controller.NoResyncPeriodFunc())
			pgInformerFactory := crdinformers.NewSharedInformerFactory(pgClient, controller.NoResyncPeriodFunc())
			pgInformer := pgInformerFactory.Scheduling().V1alpha1().PodGroups()
			SetupPodGroupController(ctx, kubeClient, pgClient, pgInformer)

			pgInformerFactory.Start(ctx.Done())
			informerFactory.Start(ctx.Done())

			// Override phase of one of the pods, choose pod with index 0.
			// This is used to create different status portfolio
			if len(c.podPhaseOverride) != 0 {
				overridePod := ps[0]
				overridePod.Status.Phase = c.podPhaseOverride
				kubeClient.CoreV1().Pods(overridePod.Namespace).UpdateStatus(ctx, overridePod, metav1.UpdateOptions{})
			}
			if c.podNodeNameOverride != nil {
				overridePod := ps[0]
				overridePod.Spec.NodeName = *c.podNodeNameOverride
				kubeClient.CoreV1().Pods(overridePod.Namespace).Update(ctx, overridePod, metav1.UpdateOptions{})
			}
			err := wait.Poll(200*time.Millisecond, 1*time.Second, func() (done bool, err error) {
				newPg, err := pgClient.SchedulingV1alpha1().PodGroups("default").Get(ctx, c.pgName, metav1.GetOptions{})
				if err != nil {
					return false, err
				}
				if newPg.Status.Phase != c.desiredGroupPhase {
					return false, fmt.Errorf("want %v, got %v", c.desiredGroupPhase, newPg.Status.Phase)
				}

				if c.desiredOccupiedObjName != nil {
					if newPg.Status.OccupiedBy != *c.desiredOccupiedObjName {
						return false, fmt.Errorf("want %v, got %v", *c.desiredOccupiedObjName, newPg.Status.OccupiedBy)
					}
				}

				// Compare conditions difference
				// by default, state change one time in each run
				delta := 1
				if c.desiredConditionDelta != nil {
					delta = *c.desiredConditionDelta
				}
				// move to next state and one more condition should be added
				if len(newPg.Status.Conditions) != len(pg.Status.Conditions)+delta {
					return false, fmt.Errorf("want %v condition, got %v condition", len(pg.Status.Conditions)+delta, len(newPg.Status.Conditions))
				}

				return true, nil
			})
			if err != nil {
				t.Fatal("Unexpected error", err)
			}
		})
	}
}

func makePods(podNames []string, pgName string, nodeName string) []*v1.Pod {
	pds := make([]*v1.Pod, 0)
	trueP := true
	for _, name := range podNames {
		pod := testinghelper.MakePod().Namespace("default").Name(name).Obj()
		pod.Labels = map[string]string{podAnnotations.PodGroupNameAnnotationKey: pgName}
		pod.Annotations = map[string]string{podAnnotations.PodGroupNameAnnotationKey: pgName}
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

func makePG(pgName string, minMember int32, previousPhase v1alpha1.PodGroupPhase, scheduleTimeoutSeconds *int32, createTime *metav1.Time) *v1alpha1.PodGroup {
	pg := &v1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              pgName,
			Namespace:         "default",
			CreationTimestamp: metav1.Time{Time: time.Now()},
		},
		Spec: v1alpha1.PodGroupSpec{
			MinMember:              minMember,
			ScheduleTimeoutSeconds: scheduleTimeoutSeconds,
		},
		Status: v1alpha1.PodGroupStatus{
			Phase: previousPhase,
		},
	}

	if len(previousPhase) != 0 {
		pg.Status.Conditions = append(pg.Status.Conditions,
			v1alpha1.PodGroupCondition{
				Phase:  previousPhase,
				Status: v1.ConditionTrue,
			},
		)
	}

	if createTime != nil {
		pg.CreationTimestamp = *createTime
	}
	return pg
}

func makeOccupiedObj(kind, namespace, name string) string {
	return fmt.Sprintf("%s/%s/%s", kind, namespace, name)
}
