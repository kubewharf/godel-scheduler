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
	"fmt"
	"reflect"
	"testing"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	v1 "k8s.io/api/core/v1"
)

func makeFakePod(name, ns, placeholder, node string) *v1.Pod {
	return testing_helper.MakePod().Name(name).Namespace(ns).
		Annotation(podutil.ReservationIndexAnnotation, placeholder).
		Annotation(podutil.ReservationPlaceHolderPodAnnotation, "true").
		Node(node).Obj()
}

func mustNewReservationInfo(placeholderPod, matchedPod *v1.Pod) *ReservationInfo {
	info, err := NewReservationInfo(placeholderPod, matchedPod)
	if err != nil {
		panic(fmt.Errorf("failed to create reservation info, %v", err))
	}
	return info
}

func mustNewIdentifier(placeholderPod *v1.Pod) *ReservationIdentifier {
	id, err := GetReservationIdentifier(placeholderPod)
	if err != nil {
		panic(fmt.Errorf("failed to create identifier, %v", err))
	}
	return id
}

func TestSetAndResetMatchedPod(t *testing.T) {
	placeholderPod := makeFakePod("p1", "n1", "ph1", "node1")
	matchedPod := testing_helper.MakePod().Name("p2").Namespace("n2").Obj()

	tests := []struct {
		Name           string
		matchedPod     *v1.Pod
		placeholderPod *v1.Pod
		initial        *ReservationInfo
		want           *ReservationInfo
	}{
		{
			Name:       "matched Pod can be set",
			matchedPod: matchedPod,
			initial:    mustNewReservationInfo(placeholderPod, nil),
			want:       mustNewReservationInfo(placeholderPod, matchedPod),
		},
		{
			Name:       "matched Pod can be reset",
			matchedPod: nil,
			initial:    mustNewReservationInfo(placeholderPod, matchedPod),
			want:       mustNewReservationInfo(placeholderPod, nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			store := NewCacheNodeReservationStore()
			store.AddReservationInfo(tt.initial)

			id := mustNewIdentifier(placeholderPod)
			store.SetMatchedPodForReservation(id, tt.matchedPod)

			u := store.GetReservationInfo(id)
			if !reflect.DeepEqual(tt.want.MatchedPod, u.MatchedPod) {
				t.Fatalf("expected unit %v, got %v", tt.want, u)
			}
		})
	}
}

func TestNodeReservationStore_CRUDReservationInfo(t *testing.T) {
	tests := []struct {
		name   string
		add    []*ReservationInfo
		delete []*ReservationIdentifier
		want   []*ReservationIdentifier
	}{
		{
			name: "add reservation info",
			add: []*ReservationInfo{
				mustNewReservationInfo(makeFakePod("p1", "n1", "ph1", "node1"), nil),
				mustNewReservationInfo(makeFakePod("p2", "n2", "ph1", "node1"), nil),
			},
			want: []*ReservationIdentifier{
				mustNewIdentifier(makeFakePod("p1", "n1", "ph1", "node1")),
				mustNewIdentifier(makeFakePod("p2", "n2", "ph1", "node1")),
			},
		},
		{
			name: "remove reservation info",
			add: []*ReservationInfo{
				mustNewReservationInfo(makeFakePod("p1", "n1", "ph1", "node1"), nil),
				mustNewReservationInfo(makeFakePod("p2", "n2", "ph1", "node1"), nil),
			},
			delete: []*ReservationIdentifier{
				mustNewIdentifier(makeFakePod("p1", "n1", "ph1", "node1")),
			},
			want: []*ReservationIdentifier{
				mustNewIdentifier(makeFakePod("p2", "n2", "ph1", "node1")),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewCacheNodeReservationStore()
			for _, info := range tt.add {
				s.AddReservationInfo(info)
			}

			for _, info := range tt.delete {
				s.DeleteReservationInfo(info)
			}

			for _, w := range tt.want {
				g := s.GetReservationInfo(w)
				if g == nil {
					t.Fatalf("failed to get reservation info, %v", w)
				}
			}
		})
	}
}

func TestNodeReservationStore_GetAvailablePlaceholdersOnNode(t *testing.T) {
	tests := []struct {
		name            string
		placeholderPods []*v1.Pod
		want            map[string]framework.ReservationPlaceholdersOfNodes
	}{
		{
			name: "add reservation info in same node and same placeholder",
			placeholderPods: []*v1.Pod{
				makeFakePod("p1", "n1", "ph1", "node1"),
				makeFakePod("p2", "n2", "ph1", "node1"),
			},
			want: map[string]framework.ReservationPlaceholdersOfNodes{
				"node1": {
					"ph1": map[string]*v1.Pod{
						"n1/p1": makeFakePod("p1", "n1", "ph1", "node1"),
						"n2/p2": makeFakePod("p2", "n2", "ph1", "node1"),
					},
				},
			},
		},
		{
			name: "remove reservation info in same placeholder but different node",
			placeholderPods: []*v1.Pod{
				makeFakePod("p1", "n1", "ph1", "node1"),
				makeFakePod("p2", "n2", "ph1", "node2"),
			},
			want: map[string]framework.ReservationPlaceholdersOfNodes{
				"node1": {
					"ph1": map[string]*v1.Pod{
						"n1/p1": makeFakePod("p1", "n1", "ph1", "node1"),
					},
				},
				"node2": {
					"ph1": map[string]*v1.Pod{
						"n2/p2": makeFakePod("p2", "n2", "ph1", "node2"),
					},
				},
			},
		},
		{
			name: "remove reservation info in different placeholder and different node ",
			placeholderPods: []*v1.Pod{
				makeFakePod("p1", "n1", "ph1", "node1"),
				makeFakePod("p2", "n2", "ph2", "node2"),
			},
			want: map[string]framework.ReservationPlaceholdersOfNodes{
				"node1": {
					"ph1": map[string]*v1.Pod{
						"n1/p1": makeFakePod("p1", "n1", "ph1", "node1"),
					},
				},
				"node2": {
					"ph2": map[string]*v1.Pod{
						"n2/p2": makeFakePod("p2", "n2", "ph2", "node2"),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewCacheNodeReservationStore()
			for _, placeholderPod := range tt.placeholderPods {
				s.AddReservationInfo(mustNewReservationInfo(placeholderPod, nil))
			}

			for nodeName, placeholderPods := range tt.want {
				for placeholder, placeholderPods := range placeholderPods {
					got, err := s.GetAvailablePlaceholderPodsOnNode(nodeName, placeholder)
					if err != nil {
						t.Fatalf("failed to get available placeholder pods, node: %s, placeholder: %s, err: %v", nodeName, placeholder, err)
					}

					if !reflect.DeepEqual(got, placeholderPods) {
						t.Fatalf("expected placeholder pods %v, got %v", placeholderPods, got)
					}
				}
			}
		})
	}
}
