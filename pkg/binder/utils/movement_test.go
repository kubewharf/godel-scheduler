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

package utils

import (
	"reflect"
	"testing"
	"time"

	"github.com/kubewharf/godel-scheduler/pkg/features"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/tools/cache"
)

func TestUpdateMovement(t *testing.T) {
	tests := []struct {
		name             string
		movement         *schedulingv1a1.Movement
		pod              *v1.Pod
		expectedMovement *schedulingv1a1.Movement
	}{
		{
			name: "schedule suceess using suggested info",
			pod: testing_helper.MakePod().Namespace("default").Name("foo").UID("foo").Node("n").
				Annotation(podutil.MovementNameKey, "movement").
				ControllerRef(metav1.OwnerReference{Kind: "ReplicaSet", Name: "rs", UID: "rs"}).Obj(),
			movement: &schedulingv1a1.Movement{
				ObjectMeta: metav1.ObjectMeta{Name: "movement"},
				Status: schedulingv1a1.MovementStatus{
					Owners: []*schedulingv1a1.Owner{
						{
							Owner: &schedulingv1a1.OwnerInfo{Type: "ReplicaSet", Namespace: "default", Name: "rs", UID: "rs"},
							RecommendedNodes: []*schedulingv1a1.RecommendedNode{
								{
									Node:            "n",
									DesiredPodCount: 1,
								},
							},
						},
					},
				},
			},
			expectedMovement: &schedulingv1a1.Movement{
				ObjectMeta: metav1.ObjectMeta{Name: "movement"},
				Status: schedulingv1a1.MovementStatus{
					Owners: []*schedulingv1a1.Owner{
						{
							Owner: &schedulingv1a1.OwnerInfo{Type: "ReplicaSet", Namespace: "default", Name: "rs", UID: "rs"},
							RecommendedNodes: []*schedulingv1a1.RecommendedNode{
								{
									Node:            "n",
									DesiredPodCount: 1,
									ActualPods: []*schedulingv1a1.TaskInfo{
										{Namespace: "default", Name: "foo", UID: "foo", Node: "n"},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "schedule suceess using suggested info, but have achieved desired pod count before",
			pod: testing_helper.MakePod().Namespace("default").Name("foo").UID("foo").Node("n").
				Annotation(podutil.MovementNameKey, "movement").
				ControllerRef(metav1.OwnerReference{Kind: "ReplicaSet", Name: "rs", UID: "rs"}).Obj(),
			movement: &schedulingv1a1.Movement{
				ObjectMeta: metav1.ObjectMeta{Name: "movement"},
				Status: schedulingv1a1.MovementStatus{
					Owners: []*schedulingv1a1.Owner{
						{
							Owner: &schedulingv1a1.OwnerInfo{Type: "ReplicaSet", Namespace: "default", Name: "rs", UID: "rs"},
							RecommendedNodes: []*schedulingv1a1.RecommendedNode{
								{
									Node:            "n",
									DesiredPodCount: 1,
									ActualPods: []*schedulingv1a1.TaskInfo{
										{Namespace: "default", Name: "p1", UID: "p1"},
									},
								},
							},
						},
					},
				},
			},
			expectedMovement: &schedulingv1a1.Movement{
				ObjectMeta: metav1.ObjectMeta{Name: "movement"},
				Status: schedulingv1a1.MovementStatus{
					Owners: []*schedulingv1a1.Owner{
						{
							Owner: &schedulingv1a1.OwnerInfo{Type: "ReplicaSet", Namespace: "default", Name: "rs", UID: "rs"},
							RecommendedNodes: []*schedulingv1a1.RecommendedNode{
								{
									Node:            "n",
									DesiredPodCount: 1,
									ActualPods: []*schedulingv1a1.TaskInfo{
										{Namespace: "default", Name: "p1", UID: "p1"},
									},
								},
							},
							MismatchedTasks: []*schedulingv1a1.TaskInfo{
								{Namespace: "default", Name: "foo", UID: "foo", Node: "n"},
							},
						},
					},
				},
			},
		},
		{
			name: "schedule success, using suggested info failed",
			pod: testing_helper.MakePod().Namespace("default").Name("foo").UID("foo").Node("n1").
				Annotation(podutil.MovementNameKey, "movement").
				ControllerRef(metav1.OwnerReference{Kind: "ReplicaSet", Name: "rs", UID: "rs"}).Obj(),
			movement: &schedulingv1a1.Movement{
				ObjectMeta: metav1.ObjectMeta{Name: "movement"},
				Status: schedulingv1a1.MovementStatus{
					Owners: []*schedulingv1a1.Owner{
						{
							Owner: &schedulingv1a1.OwnerInfo{Type: "ReplicaSet", Namespace: "default", Name: "rs", UID: "rs"},
							RecommendedNodes: []*schedulingv1a1.RecommendedNode{
								{
									Node:            "n",
									DesiredPodCount: 1,
								},
							},
						},
					},
				},
			},
			expectedMovement: &schedulingv1a1.Movement{
				ObjectMeta: metav1.ObjectMeta{Name: "movement"},
				Status: schedulingv1a1.MovementStatus{
					Owners: []*schedulingv1a1.Owner{
						{
							Owner: &schedulingv1a1.OwnerInfo{Type: "ReplicaSet", Namespace: "default", Name: "rs", UID: "rs"},
							RecommendedNodes: []*schedulingv1a1.RecommendedNode{
								{
									Node:            "n",
									DesiredPodCount: 1,
								},
							},
							MismatchedTasks: []*schedulingv1a1.TaskInfo{
								{Namespace: "default", Name: "foo", UID: "foo", Node: "n1"},
							},
						},
					},
				},
			},
		},
		{
			name: "schedule success, using suggested info failed, but failed pod have achieved desired pod count before",
			pod: testing_helper.MakePod().Namespace("default").Name("foo").UID("foo").Node("n").
				Annotation(podutil.MovementNameKey, "movement").
				ControllerRef(metav1.OwnerReference{Kind: "ReplicaSet", Name: "rs", UID: "rs"}).Obj(),
			movement: &schedulingv1a1.Movement{
				ObjectMeta: metav1.ObjectMeta{Name: "movement"},
				Status: schedulingv1a1.MovementStatus{
					Owners: []*schedulingv1a1.Owner{
						{
							Owner: &schedulingv1a1.OwnerInfo{Type: "ReplicaSet", Namespace: "default", Name: "rs", UID: "rs"},
							RecommendedNodes: []*schedulingv1a1.RecommendedNode{
								{
									Node:            "n1",
									DesiredPodCount: 1,
								},
								{
									Node:            "n2",
									DesiredPodCount: 1,
								},
							},
							MismatchedTasks: []*schedulingv1a1.TaskInfo{
								{Namespace: "default", Name: "p1", UID: "p1"},
								{Namespace: "default", Name: "p2", UID: "p2"},
							},
						},
					},
				},
			},
			expectedMovement: &schedulingv1a1.Movement{
				ObjectMeta: metav1.ObjectMeta{Name: "movement"},
				Status: schedulingv1a1.MovementStatus{
					Owners: []*schedulingv1a1.Owner{
						{
							Owner: &schedulingv1a1.OwnerInfo{Type: "ReplicaSet", Namespace: "default", Name: "rs", UID: "rs"},
							RecommendedNodes: []*schedulingv1a1.RecommendedNode{
								{
									Node:            "n1",
									DesiredPodCount: 1,
								},
								{
									Node:            "n2",
									DesiredPodCount: 1,
								},
							},
							MismatchedTasks: []*schedulingv1a1.TaskInfo{
								{Namespace: "default", Name: "p1", UID: "p1"},
								{Namespace: "default", Name: "p2", UID: "p2"},
							},
						},
					},
				},
			},
		},
		{
			name: "movement not exist",
			pod: testing_helper.MakePod().Namespace("default").Name("foo").UID("foo").Node("n").
				Annotation(podutil.MovementNameKey, "movement").
				ControllerRef(metav1.OwnerReference{Kind: "ReplicaSet", Name: "rs", UID: "rs"}).Obj(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(features.SupportRescheduling): true})

			crdClient := godelclientfake.NewSimpleClientset()
			if tt.movement != nil {
				crdClient = godelclientfake.NewSimpleClientset(tt.movement)
			}

			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)

			movementInformer := crdInformerFactory.Scheduling().V1alpha1().Movements()
			movementSharedInformer := movementInformer.Informer()
			stopCh := make(chan struct{})
			defer close(stopCh)

			crdInformerFactory.Start(stopCh)
			cache.WaitForCacheSync(stopCh, movementSharedInformer.HasSynced)

			movementLister := crdInformerFactory.Scheduling().V1alpha1().Movements().Lister()

			needReEnqueue := UpdateMovement(crdClient, movementLister, tt.pod)
			if needReEnqueue {
				t.Errorf("expected no need to re-enqueue")
			}
			time.Sleep(time.Second)
			if tt.expectedMovement != nil {
				gotMovement, err := movementLister.Get(tt.movement.Name)
				if err != nil {
					t.Errorf("get movement error: %v", err)
				}
				if !reflect.DeepEqual(tt.expectedMovement, gotMovement) {
					t.Errorf("expected movement: %#v, but got: %#v", tt.expectedMovement, gotMovement)
				}
			}
		})
	}
}
