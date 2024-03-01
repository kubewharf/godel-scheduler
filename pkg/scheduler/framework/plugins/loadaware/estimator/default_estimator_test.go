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

package estimator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func TestDefaultEstimator(t *testing.T) {
	tests := []struct {
		name          string
		pod           *corev1.Pod
		scalarFactors map[corev1.ResourceName]int64
		want          *framework.Resource
	}{
		{
			name: "estimate empty be pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{podutil.PodResourceTypeAnnotationKey: string(podutil.BestEffortPod)},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:      "main",
							Resources: corev1.ResourceRequirements{},
						},
					},
				},
			},
			want: &framework.Resource{
				MilliCPU: util.DefaultMilliCPURequest,
				Memory:   util.DefaultMemoryRequest,
			},
		},
		{
			name: "estimate empty gt pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod)},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Limits: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
				},
			},
			want: &framework.Resource{},
		},
		{
			name: "estimate normal be pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{podutil.PodResourceTypeAnnotationKey: string(podutil.BestEffortPod)},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Limits: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Requests: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
				},
			},
			want: &framework.Resource{
				MilliCPU: 1000,
				Memory:   1 << 30,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadAwareSchedulingArgs := config.LoadAwareArgs{
				Resources: []config.ResourceSpec{
					{
						Name:         string(corev1.ResourceCPU),
						Weight:       1,
						ResourceType: podutil.BestEffortPod,
					},
					{
						Name:         string(corev1.ResourceMemory),
						Weight:       1,
						ResourceType: podutil.BestEffortPod,
					},
				},
			}
			estimator, err := NewDefaultEstimator(&loadAwareSchedulingArgs, nil)
			assert.NoError(t, err)
			assert.NotNil(t, estimator)
			assert.Equal(t, DefaultEstimatorName, estimator.Name())

			got, err := estimator.EstimatePod(tt.pod)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
