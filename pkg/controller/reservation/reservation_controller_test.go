/*
Copyright 2024 The Godel Scheduler Authors.

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

package reservation

import (
	"context"
	"reflect"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util/controller"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	testNS = "default"
	ttl    = 60
)

func makePod(name string, annotations map[string]string, nodeName string) *v1.Pod {
	pod := testinghelper.MakePod().Namespace("default").Name(name).Obj()
	pod.Annotations = annotations
	pod.Spec.NodeName = nodeName
	return pod
}

func makeReservationCrd(pod *v1.Pod) *schedulingv1a1.Reservation {
	crd, _ := podutil.ConstructReservationAccordingToPod(pod, ttl)
	return crd
}

func TestCreateReservationCrdOnPodDeletion(t *testing.T) {
	tests := []struct {
		Name        string
		Pod         *v1.Pod
		ExpectedCrd *schedulingv1a1.Reservation
	}{
		{
			Name: "pod without reservation request will not trigger crd creation.",
			Pod: makePod("testPod", map[string]string{},
				testNS),
			ExpectedCrd: nil,
		},
		{
			Name: "pod with reservation request can trigger crd creation.",
			Pod: makePod("testPod", map[string]string{
				podutil.PodResourceReservationAnnotationForGodel: podutil.PodHasReservationRequirement,
			},
				testNS),
			ExpectedCrd: testinghelper.WrapReservation(
				makeReservationCrd(makePod("testPod", map[string]string{
					podutil.PodResourceReservationAnnotationForGodel: podutil.PodHasReservationRequirement,
				}, testNS))).Obj(),
		},
		{
			Name: "pod not bound can not trigger crd creation.",
			Pod: makePod("testPod", map[string]string{
				podutil.PodResourceReservationAnnotationForGodel: podutil.PodHasReservationRequirement,
			},
				""),
			ExpectedCrd: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			kubeClient := fake.NewSimpleClientset()
			godelClient := godelfake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(kubeClient, controller.NoResyncPeriodFunc())
			godelInformerFactory := crdinformers.NewSharedInformerFactory(godelClient, controller.NoResyncPeriodFunc())
			podInformer := informerFactory.Core().V1().Pods()
			deployInformer := informerFactory.Apps().V1().Deployments()
			podReservationInformer := godelInformerFactory.Scheduling().V1alpha1().Reservations()

			rc := NewReservationController(
				context.TODO(),
				godelClient,
				podInformer,
				deployInformer,
				podReservationInformer,
				1,
				60,
				60,
			)
			rc.createReservationCrdOnPodDeletion(tt.Pod)
			crd, _ := godelClient.SchedulingV1alpha1().Reservations(testNS).Get(context.TODO(), tt.Pod.Name, metav1.GetOptions{})
			if !reflect.DeepEqual(tt.ExpectedCrd, crd) {
				t.Errorf("expected: %#v, got: %#v", tt.ExpectedCrd, crd)
			}
		})
	}
}

func TestHandleReservation(t *testing.T) {
	tests := []struct {
		Name        string
		Crd         *schedulingv1a1.Reservation
		ExpectedNil bool
	}{
		{
			Name: "timeout CRD will be deleted.",
			Crd: testinghelper.WrapReservation(makeReservationCrd(makePod("testPod", map[string]string{
				podutil.PodResourceReservationAnnotationForGodel: podutil.PodHasReservationRequirement,
			}, testNS))).CreateTime(
				metav1.NewTime(time.Now().Add((-100) * time.Second))).Obj(),
			ExpectedNil: true,
		},
		{
			Name: "CRD is not timeout won't be deleted.",
			Crd: testinghelper.WrapReservation(makeReservationCrd(makePod("testPod", map[string]string{
				podutil.PodResourceReservationAnnotationForGodel: podutil.PodHasReservationRequirement,
			}, testNS))).CreateTime(
				metav1.NewTime(time.Now())).Obj(),
			ExpectedNil: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			kubeClient := fake.NewSimpleClientset()
			godelClient := godelfake.NewSimpleClientset(tt.Crd)
			informerFactory := informers.NewSharedInformerFactory(kubeClient, controller.NoResyncPeriodFunc())
			godelInformerFactory := crdinformers.NewSharedInformerFactory(godelClient, controller.NoResyncPeriodFunc())
			podInformer := informerFactory.Core().V1().Pods()
			deployInformer := informerFactory.Apps().V1().Deployments()
			podReservationInformer := godelInformerFactory.Scheduling().V1alpha1().Reservations()

			rc := NewReservationController(
				context.TODO(),
				godelClient,
				podInformer,
				deployInformer,
				podReservationInformer,
				1,
				60,
				60,
			)

			podReservationInformer.Informer().GetIndexer().Add(tt.Crd)
			rc.handleReservation(context.TODO())
			crd, _ := godelClient.SchedulingV1alpha1().Reservations(testNS).Get(context.TODO(), tt.Crd.Name, metav1.GetOptions{})
			if tt.ExpectedNil {
				if crd != nil {
					t.Errorf("expected: nil, got: %#v", crd)
				}
			} else {
				if crd == nil {
					t.Errorf("expected: not nil, but got: %#v", crd)
				}
			}
		})
	}
}
