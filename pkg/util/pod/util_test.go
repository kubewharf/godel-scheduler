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

package pod

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetOwnerInfo(t *testing.T) {
	p1 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "p1",
			Name:      "p1",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "DaemonSet",
					Name: "ds1",
				},
			},
		},
	}

	p2 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "p2",
			Name:      "p2",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "ReplicaSet",
					Name: "rs2",
				},
			},
		},
	}

	gotKind1, gotKey1 := GetOwnerInfo(p1)
	if gotKind1 != "DaemonSet" || gotKey1 != "p1/ds1" {
		t.Errorf("got owner error for p1")
	}
	gotKind2, gotKey2 := GetOwnerInfo(p2)
	if gotKind2 != "ReplicaSet" || gotKey2 != "p2/rs2" {
		t.Errorf("got owner error for p2")
	}
}
