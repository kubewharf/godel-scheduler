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
	"reflect"
	"testing"
	"time"

	binderutils "github.com/kubewharf/godel-scheduler/pkg/binder/utils"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
)

func TestUpdateAddPod(t *testing.T) {
	tests := []struct {
		name          string
		pod           *corev1.Pod
		expectedCount int
	}{
		{
			name:          "pod has no movement annotation",
			pod:           testing_helper.MakePod().Namespace("p").Name("p").UID("p").Obj(),
			expectedCount: 0,
		},
		{
			name:          "pod has movement annotation",
			pod:           testing_helper.MakePod().Namespace("p").Name("p").UID("p").Annotation(podutil.MovementNameKey, "m").Obj(),
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rateLimiter := workqueue.NewMaxOfRateLimiter(
				workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 500*time.Second),
				// 10 qps, 100 bucket size.  This is only for retry speed and its only the overall factor (not per item)
				&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
			)
			movementQueue := workqueue.NewNamedRateLimitingQueue(rateLimiter, "Movement")
			controller := &MovementController{
				queue:  movementQueue,
				stopCh: make(<-chan struct{}),
			}
			controller.AddPod(tt.pod)
			if controller.queue.Len() != tt.expectedCount {
				t.Errorf("expected count %d but got %d", tt.expectedCount, controller.queue.Len())
			}
		})
	}
}

func TestProcessNextPod(t *testing.T) {
	tests := []struct {
		name             string
		pod              *corev1.Pod
		movement         *schedulingv1a1.Movement
		expectedMovement *schedulingv1a1.Movement
	}{
		{
			name: "movement not exist",
			pod: testing_helper.MakePod().Namespace("default").Name("p").UID("p").Node("n").
				ControllerRef(metav1.OwnerReference{Kind: "ReplicaSet", Name: "rs", UID: "rs"}).
				Annotation(podutil.MovementNameKey, "m").Obj(),
			movement: &schedulingv1a1.Movement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "m1",
				},
				Status: schedulingv1a1.MovementStatus{
					Owners: []*schedulingv1a1.Owner{
						{
							Owner: &schedulingv1a1.OwnerInfo{
								Type:      "ReplicaSet",
								Namespace: "default",
								Name:      "rs",
								UID:       "rs",
							},
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
		},
		{
			name: "update successfully",
			pod: testing_helper.MakePod().Namespace("default").Name("p").UID("p").Node("n").
				ControllerRef(metav1.OwnerReference{Kind: "ReplicaSet", Name: "rs", UID: "rs"}).
				Annotation(podutil.MovementNameKey, "m").Obj(),
			movement: &schedulingv1a1.Movement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "m",
				},
				Status: schedulingv1a1.MovementStatus{
					Owners: []*schedulingv1a1.Owner{
						{
							Owner: &schedulingv1a1.OwnerInfo{
								Type:      "ReplicaSet",
								Namespace: "default",
								Name:      "rs",
								UID:       "rs",
							},
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
				ObjectMeta: metav1.ObjectMeta{
					Name: "m",
				},
				Status: schedulingv1a1.MovementStatus{
					Owners: []*schedulingv1a1.Owner{
						{
							Owner: &schedulingv1a1.OwnerInfo{
								Type:      "ReplicaSet",
								Namespace: "default",
								Name:      "rs",
								UID:       "rs",
							},
							RecommendedNodes: []*schedulingv1a1.RecommendedNode{
								{
									Node:            "n",
									DesiredPodCount: 1,
									ActualPods: []*schedulingv1a1.TaskInfo{
										{
											Namespace: "default",
											Name:      "p",
											UID:       "p",
											Node:      "n",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stop := make(chan struct{})
			defer close(stop)

			crdClient := godelclientfake.NewSimpleClientset(tt.movement)
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			crdInformerFactory.Scheduling().V1alpha1().Movements().Informer()
			crdInformerFactory.Start(stop)
			crdInformerFactory.WaitForCacheSync(stop)

			movementLister := crdInformerFactory.Scheduling().V1alpha1().Movements().Lister()

			rateLimiter := workqueue.NewMaxOfRateLimiter(
				workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 500*time.Second),
				// 10 qps, 100 bucket size.  This is only for retry speed and its only the overall factor (not per item)
				&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
			)
			movementQueue := workqueue.NewNamedRateLimitingQueue(rateLimiter, "Movement")
			movementController := &MovementController{
				queue:          movementQueue,
				stopCh:         make(<-chan struct{}),
				movementLister: movementLister,
				crdClient:      crdClient,
			}
			movementController.syncFunc = func(pod *corev1.Pod) bool {
				// update movement status if necessary
				return binderutils.UpdateMovement(crdClient, movementLister, pod)
			}
			movementController.AddPod(tt.pod)
			movementController.processNextPod()
			time.Sleep(time.Second)

			if movementController.queue.Len() > 0 {
				t.Errorf("expected count 0 but got %d", movementController.queue.Len())
			}

			if gotMovement, err := movementLister.Get("m"); err != nil {
				if tt.expectedMovement != nil {
					t.Errorf("expected movemnet %v but got nil: %v", tt.expectedMovement, err)
				}
			} else {
				if tt.expectedMovement == nil {
					t.Errorf("expected nil movement but got %v", gotMovement)
				} else {
					if !reflect.DeepEqual(tt.expectedMovement, gotMovement) {
						t.Errorf("expected movement %#v but got %#v", tt.expectedMovement, gotMovement)
					}
				}
			}
		})
	}
}

func TestRunMovementWorker(t *testing.T) {
	attempts := 0
	tests := []struct {
		name             string
		syncFunc         func(pod *corev1.Pod) bool
		expectedAttempts int
	}{
		{
			name: "no need to re-enqueue",
			syncFunc: func(pod *corev1.Pod) bool {
				attempts++
				return false
			},
			expectedAttempts: 1,
		},
		{
			name: "need re-enqueue, reach max retry attempts",
			syncFunc: func(pod *corev1.Pod) bool {
				attempts++
				return true
			},
			expectedAttempts: 11,
		},
		{
			name: "return nil error",
			syncFunc: func(pod *corev1.Pod) bool {
				attempts++
				return false
			},
			expectedAttempts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stop := make(chan struct{})
			defer close(stop)

			attempts = 0
			crdClient := godelclientfake.NewSimpleClientset()
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)
			crdInformerFactory.Scheduling().V1alpha1().Movements().Informer()
			crdInformerFactory.Start(stop)
			crdInformerFactory.WaitForCacheSync(stop)

			movementLister := crdInformerFactory.Scheduling().V1alpha1().Movements().Lister()

			rateLimiter := workqueue.NewMaxOfRateLimiter(
				workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 10*time.Millisecond),
				// 10 qps, 100 bucket size.  This is only for retry speed and its only the overall factor (not per item)
				&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
			)
			movementQueue := workqueue.NewNamedRateLimitingQueue(rateLimiter, "Movement")
			movementController := &MovementController{
				queue:          movementQueue,
				stopCh:         stop,
				movementLister: movementLister,
				crdClient:      crdClient,
			}
			movementController.syncFunc = tt.syncFunc
			pod := testing_helper.MakePod().Namespace("default").Name("p").UID("p").Node("n").
				ControllerRef(metav1.OwnerReference{Kind: "ReplicaSet", Name: "rs", UID: "rs"}).
				Annotation(podutil.MovementNameKey, "m").Obj()
			movementController.AddPod(pod)
			go movementController.runMovementWorker()
			time.Sleep(time.Second)
			stop <- struct{}{}

			if movementController.queue.Len() > 0 {
				t.Errorf("expected count 0 but got %d", movementController.queue.Len())
			}

			if attempts != tt.expectedAttempts {
				t.Errorf("expected attempt %d times but attempt %d times", tt.expectedAttempts, attempts)
			}
		})
	}
}
