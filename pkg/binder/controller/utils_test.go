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
	"fmt"
	"testing"

	v1 "k8s.io/api/core/v1"

	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	podAnnotations "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func Test_GetPodGroupNameFromAnnotation(t *testing.T) {
	pgName := "pg1"

	cases := []struct {
		name           string
		podName        string
		annotations    map[string]string
		expectedResult string
	}{
		{
			name: "pod with correct annotations",
			annotations: map[string]string{
				podAnnotations.PodGroupNameAnnotationKey: pgName,
			},
			expectedResult: pgName,
		},
		{
			name:           "pod with empty annotations",
			annotations:    map[string]string{},
			expectedResult: "",
		},
		{
			name:           "pod with unrelated annotations",
			annotations:    map[string]string{"unrelated": "unrelated"},
			expectedResult: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pod := makePod(c.annotations)
			result := getPodGroupNameFromAnnotation(pod)
			var err error
			if result != c.expectedResult {
				err = fmt.Errorf("want %v, got %v", c.expectedResult, result)
			}

			if err != nil {
				t.Fatal("Unexpected error", err)
			}
		})
	}
}

func makePod(annotations map[string]string) *v1.Pod {
	pod := testinghelper.MakePod().Namespace("default").Name("default").Obj()
	pod.Annotations = annotations
	return pod
}
