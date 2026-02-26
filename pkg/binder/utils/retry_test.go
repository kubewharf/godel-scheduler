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
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetBindFailureCount(t *testing.T) {
	tests := []struct {
		name string
		pod  *v1.Pod
		want int
	}{
		{
			name: "nil pod returns 0",
			pod:  nil,
			want: 0,
		},
		{
			name: "no annotations returns 0",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-1"},
			},
			want: 0,
		},
		{
			name: "annotation absent returns 0",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{"other": "value"},
				},
			},
			want: 0,
		},
		{
			name: "valid value returns parsed count",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{BindFailureCountAnnotationKey: "3"},
				},
			},
			want: 3,
		},
		{
			name: "invalid value returns 0 (tolerance)",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{BindFailureCountAnnotationKey: "not-a-number"},
				},
			},
			want: 0,
		},
		{
			name: "empty string returns 0",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{BindFailureCountAnnotationKey: ""},
				},
			},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetBindFailureCount(tt.pod)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSetBindFailureCount(t *testing.T) {
	tests := []struct {
		name  string
		pod   *v1.Pod
		count int
		want  string
	}{
		{
			name: "nil pod is no-op",
			pod:  nil,
		},
		{
			name: "sets annotation on pod with nil annotations",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-1"},
			},
			count: 5,
			want:  "5",
		},
		{
			name: "sets annotation on pod with existing annotations",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{"other": "value"},
				},
			},
			count: 2,
			want:  "2",
		},
		{
			name: "overwrites existing value",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{BindFailureCountAnnotationKey: "1"},
				},
			},
			count: 10,
			want:  "10",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SetBindFailureCount(tt.pod, tt.count)
			if tt.pod == nil {
				assert.Nil(t, result)
				return
			}
			assert.Equal(t, tt.want, result.Annotations[BindFailureCountAnnotationKey])
		})
	}
}

func TestIncrementBindFailureCount(t *testing.T) {
	tests := []struct {
		name      string
		pod       *v1.Pod
		wantCount int
	}{
		{
			name: "from zero increments to 1",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-1"},
			},
			wantCount: 1,
		},
		{
			name: "from existing value increments by 1",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{BindFailureCountAnnotationKey: "4"},
				},
			},
			wantCount: 5,
		},
		{
			name: "from invalid value resets to 1",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{BindFailureCountAnnotationKey: "bad"},
				},
			},
			wantCount: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IncrementBindFailureCount(tt.pod)
			assert.Equal(t, tt.wantCount, GetBindFailureCount(result))
		})
	}
}

func TestShouldDispatchToAnotherScheduler(t *testing.T) {
	tests := []struct {
		name       string
		pod        *v1.Pod
		maxRetries int
		want       bool
	}{
		{
			name: "below max returns false",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{BindFailureCountAnnotationKey: "2"},
				},
			},
			maxRetries: 5,
			want:       false,
		},
		{
			name: "at max returns true",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{BindFailureCountAnnotationKey: "5"},
				},
			},
			maxRetries: 5,
			want:       true,
		},
		{
			name: "above max returns true",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{BindFailureCountAnnotationKey: "10"},
				},
			},
			maxRetries: 5,
			want:       true,
		},
		{
			name: "maxRetries 0 disables dispatch (always false)",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{BindFailureCountAnnotationKey: "100"},
				},
			},
			maxRetries: 0,
			want:       false,
		},
		{
			name: "negative maxRetries disables dispatch",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{BindFailureCountAnnotationKey: "100"},
				},
			},
			maxRetries: -1,
			want:       false,
		},
		{
			name: "no annotation with low max returns false",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-1"},
			},
			maxRetries: 1,
			want:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldDispatchToAnotherScheduler(tt.pod, tt.maxRetries)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSetLastBindFailureReason(t *testing.T) {
	tests := []struct {
		name   string
		pod    *v1.Pod
		reason string
	}{
		{
			name:   "nil pod is no-op",
			pod:    nil,
			reason: "conflict",
		},
		{
			name: "sets reason on pod with nil annotations",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-1"},
			},
			reason: "node ownership changed",
		},
		{
			name: "overwrites existing reason",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{LastBindFailureReasonKey: "old reason"},
				},
			},
			reason: "new conflict",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SetLastBindFailureReason(tt.pod, tt.reason)
			if tt.pod == nil {
				assert.Nil(t, result)
				return
			}
			assert.Equal(t, tt.reason, result.Annotations[LastBindFailureReasonKey])
		})
	}
}

func TestGetLastBindFailureReason(t *testing.T) {
	tests := []struct {
		name string
		pod  *v1.Pod
		want string
	}{
		{
			name: "nil pod returns empty",
			pod:  nil,
			want: "",
		},
		{
			name: "no annotation returns empty",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-1"},
			},
			want: "",
		},
		{
			name: "returns stored reason",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Annotations: map[string]string{LastBindFailureReasonKey: "resource conflict"},
				},
			},
			want: "resource conflict",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetLastBindFailureReason(tt.pod)
			assert.Equal(t, tt.want, got)
		})
	}
}
