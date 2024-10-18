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

	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
)

func TestSyncMovement(t *testing.T) {
	godelSchedulerName := "godel-scheduler-0"

	tests := []struct {
		name             string
		movementName     string
		movement         *schedulingv1a1.Movement
		expectedMovement *schedulingv1a1.Movement
	}{
		{
			name: "movement is not found",
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
			movementName:     "movement1",
			expectedMovement: nil,
		},
		{
			name: "OwnerMovements is nil",
			movement: &schedulingv1a1.Movement{
				ObjectMeta: metav1.ObjectMeta{Name: "movement"},
			},
			movementName: "movement",
			expectedMovement: &schedulingv1a1.Movement{
				ObjectMeta: metav1.ObjectMeta{Name: "movement"},
			},
		},
		{
			name: "patch movement success",
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
			movementName: "movement",
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
						},
					},
					NotifiedSchedulers: []string{godelSchedulerName},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			crdClient := godelclientfake.NewSimpleClientset(tt.movement)
			crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)

			movementInformer := crdInformerFactory.Scheduling().V1alpha1().Movements()
			movementSharedInformer := movementInformer.Informer()
			stopCh := make(chan struct{})
			defer close(stopCh)

			crdInformerFactory.Start(stopCh)
			cache.WaitForCacheSync(stopCh, movementSharedInformer.HasSynced)

			rateLimiter := workqueue.NewMaxOfRateLimiter(
				workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 500*time.Second),
				// 10 qps, 100 bucket size.  This is only for retry speed and its only the overall factor (not per item)
				&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
			)
			movementQueue := workqueue.NewNamedRateLimitingQueue(rateLimiter, "Movement")

			mq := &MovementController{
				movementQueue,
				stopCh,
				movementInformer.Lister(),
				godelSchedulerName,
				crdClient,
			}
			mq.syncMovement(tt.movementName)
			time.Sleep(time.Second)

			gotMovement, err := mq.movementLister.Get(tt.movementName)
			if err != nil {
				if errors.IsNotFound(err) {
					if tt.expectedMovement != nil {
						t.Errorf("expected get movement: %v, but got nil", tt.expectedMovement)
					}
				} else {
					t.Errorf("failed to get movement %s: %v", tt.movementName, err)
				}
			} else {
				if !reflect.DeepEqual(tt.expectedMovement, gotMovement) {
					t.Errorf("expected get movement: %v, but got: %v", tt.expectedMovement, gotMovement)
				}
			}
		})
	}
}

func TestProcessNextMovement(t *testing.T) {
	godelSchedulerName := "godel-scheduler-0"

	tests := []struct {
		name             string
		nilClient        bool
		expectedQueueLen int
	}{
		{
			name:             "patch success",
			nilClient:        false,
			expectedQueueLen: 0,
		},
		{
			name:             "patch failed",
			nilClient:        true,
			expectedQueueLen: 1,
		},
	}

	for _, tt := range tests {
		movement := &schedulingv1a1.Movement{
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
		}

		crdClient := godelclientfake.NewSimpleClientset(movement)
		crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, 0)

		movementInformer := crdInformerFactory.Scheduling().V1alpha1().Movements()
		movementSharedInformer := movementInformer.Informer()
		stopCh := make(chan struct{})
		defer close(stopCh)

		crdInformerFactory.Start(stopCh)
		cache.WaitForCacheSync(stopCh, movementSharedInformer.HasSynced)

		// process success
		rateLimiter := workqueue.NewMaxOfRateLimiter(
			workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 500*time.Second),
			// 10 qps, 100 bucket size.  This is only for retry speed and its only the overall factor (not per item)
			&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
		)
		movementQueue := workqueue.NewNamedRateLimitingQueue(rateLimiter, "Movement")
		mq := &MovementController{
			movementQueue,
			stopCh,
			movementInformer.Lister(),
			godelSchedulerName,
			crdClient,
		}
		defer mq.Close()
		if tt.nilClient {
			mq.crdClient = nil
		}
		mq.AddMovement(movement)
		mq.processNextMovement()
		time.Sleep(time.Second)
		if mq.Len() != tt.expectedQueueLen {
			t.Errorf("expected queue length: %d, but got: %d", tt.expectedQueueLen, mq.Len())
		}

	}
}
