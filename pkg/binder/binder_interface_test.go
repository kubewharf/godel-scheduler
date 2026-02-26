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

package binder

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
)

func makeTestQueuedUnitInfo() *framework.QueuedUnitInfo {
	return &framework.QueuedUnitInfo{
		UnitKey: "default/test-unit",
	}
}

func makeTestQueuedPodInfo(name string) *framework.QueuedPodInfo {
	return &framework.QueuedPodInfo{
		Pod: testing_helper.MakePod().Name(name).Namespace("default").UID(name + "-uid").Obj(),
	}
}

func TestBindRequest_Validation_EmptyUnit(t *testing.T) {
	tests := []struct {
		name    string
		req     *BindRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "nil Unit returns ErrInvalidRequest",
			req: &BindRequest{
				Unit:     nil,
				Pods:     []*framework.QueuedPodInfo{makeTestQueuedPodInfo("pod-1")},
				NodeName: "node-1",
			},
			wantErr: true,
			errMsg:  "Unit must not be nil",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			assert.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidRequest))
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestBindRequest_Validation_EmptyNodeName(t *testing.T) {
	tests := []struct {
		name    string
		req     *BindRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "empty NodeName returns ErrInvalidRequest",
			req: &BindRequest{
				Unit:     makeTestQueuedUnitInfo(),
				Pods:     []*framework.QueuedPodInfo{makeTestQueuedPodInfo("pod-1")},
				NodeName: "",
			},
			wantErr: true,
			errMsg:  "NodeName must not be empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			assert.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidRequest))
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestBindRequest_Validation_EmptyPods(t *testing.T) {
	tests := []struct {
		name   string
		req    *BindRequest
		errMsg string
	}{
		{
			name: "nil Pods returns ErrInvalidRequest",
			req: &BindRequest{
				Unit:     makeTestQueuedUnitInfo(),
				Pods:     nil,
				NodeName: "node-1",
			},
			errMsg: "Pods must not be empty",
		},
		{
			name: "empty Pods slice returns ErrInvalidRequest",
			req: &BindRequest{
				Unit:     makeTestQueuedUnitInfo(),
				Pods:     []*framework.QueuedPodInfo{},
				NodeName: "node-1",
			},
			errMsg: "Pods must not be empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			assert.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidRequest))
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestBindRequest_Validation_NilRequest(t *testing.T) {
	var req *BindRequest
	err := req.Validate()
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidRequest))
	assert.Contains(t, err.Error(), "request is nil")
}

func TestBindRequest_Validation_Valid(t *testing.T) {
	tests := []struct {
		name string
		req  *BindRequest
	}{
		{
			name: "valid request with single pod",
			req: &BindRequest{
				Unit:          makeTestQueuedUnitInfo(),
				Pods:          []*framework.QueuedPodInfo{makeTestQueuedPodInfo("pod-1")},
				NodeName:      "node-1",
				SchedulerName: "scheduler-A",
			},
		},
		{
			name: "valid request with multiple pods",
			req: &BindRequest{
				Unit: makeTestQueuedUnitInfo(),
				Pods: []*framework.QueuedPodInfo{
					makeTestQueuedPodInfo("pod-1"),
					makeTestQueuedPodInfo("pod-2"),
				},
				NodeName:      "node-2",
				SchedulerName: "scheduler-B",
			},
		},
		{
			name: "valid request with victims",
			req: &BindRequest{
				Unit:          makeTestQueuedUnitInfo(),
				Pods:          []*framework.QueuedPodInfo{makeTestQueuedPodInfo("pod-1")},
				NodeName:      "node-1",
				SchedulerName: "scheduler-A",
				Victims: []*v1.Pod{
					{ObjectMeta: metav1.ObjectMeta{Name: "victim-1", Namespace: "default"}},
				},
			},
		},
		{
			name: "valid request without SchedulerName",
			req: &BindRequest{
				Unit:     makeTestQueuedUnitInfo(),
				Pods:     []*framework.QueuedPodInfo{makeTestQueuedPodInfo("pod-1")},
				NodeName: "node-1",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			assert.NoError(t, err)
		})
	}
}

func TestBindResult_AllSuccess(t *testing.T) {
	tests := []struct {
		name   string
		result *BindResult
		want   bool
	}{
		{
			name: "all pods succeeded - single pod",
			result: &BindResult{
				SuccessfulPods: []types.UID{"uid-1"},
				FailedPods:     nil,
			},
			want: true,
		},
		{
			name: "all pods succeeded - multiple pods",
			result: &BindResult{
				SuccessfulPods: []types.UID{"uid-1", "uid-2", "uid-3"},
				FailedPods:     map[types.UID]error{},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.result.AllSucceeded())
			assert.False(t, tt.result.AllFailed())
		})
	}
}

func TestBindResult_PartialFailure(t *testing.T) {
	tests := []struct {
		name   string
		result *BindResult
	}{
		{
			name: "some pods failed, some succeeded",
			result: &BindResult{
				SuccessfulPods: []types.UID{"uid-1"},
				FailedPods: map[types.UID]error{
					"uid-2": errors.New("conflict"),
				},
			},
		},
		{
			name: "multiple successes and failures",
			result: &BindResult{
				SuccessfulPods: []types.UID{"uid-1", "uid-3"},
				FailedPods: map[types.UID]error{
					"uid-2": errors.New("conflict"),
					"uid-4": errors.New("timeout"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, tt.result.AllSucceeded(), "should not be AllSucceeded with failures")
			assert.False(t, tt.result.AllFailed(), "should not be AllFailed with successes")
			assert.NotEmpty(t, tt.result.SuccessfulPods)
			assert.NotEmpty(t, tt.result.FailedPods)
		})
	}
}

func TestBindResult_AllFailure(t *testing.T) {
	tests := []struct {
		name   string
		result *BindResult
	}{
		{
			name: "all pods failed - single pod",
			result: &BindResult{
				SuccessfulPods: []types.UID{},
				FailedPods: map[types.UID]error{
					"uid-1": errors.New("node not found"),
				},
			},
		},
		{
			name: "all pods failed - multiple pods",
			result: &BindResult{
				SuccessfulPods: nil,
				FailedPods: map[types.UID]error{
					"uid-1": errors.New("conflict"),
					"uid-2": errors.New("timeout"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.result.AllFailed())
			assert.False(t, tt.result.AllSucceeded())
		})
	}
}

func TestBindResult_EmptyResult(t *testing.T) {
	result := &BindResult{}
	// An empty result with no successes and no failures is considered AllSucceeded
	// (vacuously true: there are no failures), and not AllFailed.
	assert.True(t, result.AllSucceeded())
	assert.False(t, result.AllFailed())
}

// --- NodeNames per-pod node mapping tests ---

func TestBindRequest_Validation_NodeNamesValid(t *testing.T) {
	pod1 := makeTestQueuedPodInfo("pod-1")
	pod2 := makeTestQueuedPodInfo("pod-2")
	req := &BindRequest{
		Unit: makeTestQueuedUnitInfo(),
		Pods: []*framework.QueuedPodInfo{pod1, pod2},
		NodeNames: map[types.UID]string{
			pod1.Pod.UID: "node-A",
			pod2.Pod.UID: "node-B",
		},
		SchedulerName: "scheduler-A",
	}
	err := req.Validate()
	assert.NoError(t, err, "NodeNames with entries for all pods should be valid")
}

func TestBindRequest_Validation_NodeNamesMissingEntry(t *testing.T) {
	pod1 := makeTestQueuedPodInfo("pod-1")
	pod2 := makeTestQueuedPodInfo("pod-2")
	req := &BindRequest{
		Unit: makeTestQueuedUnitInfo(),
		Pods: []*framework.QueuedPodInfo{pod1, pod2},
		NodeNames: map[types.UID]string{
			pod1.Pod.UID: "node-A",
			// pod2 missing
		},
		SchedulerName: "scheduler-A",
	}
	err := req.Validate()
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidRequest))
	assert.Contains(t, err.Error(), "NodeNames missing entry")
}

func TestBindRequest_Validation_NodeNamesFallback(t *testing.T) {
	// When NodeNames is empty but NodeName is set, validation passes (backward compat).
	req := &BindRequest{
		Unit:     makeTestQueuedUnitInfo(),
		Pods:     []*framework.QueuedPodInfo{makeTestQueuedPodInfo("pod-1")},
		NodeName: "node-1",
	}
	err := req.Validate()
	assert.NoError(t, err)
}

func TestBindRequest_Validation_BothEmpty(t *testing.T) {
	req := &BindRequest{
		Unit: makeTestQueuedUnitInfo(),
		Pods: []*framework.QueuedPodInfo{makeTestQueuedPodInfo("pod-1")},
		// Neither NodeName nor NodeNames set
	}
	err := req.Validate()
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidRequest))
	assert.Contains(t, err.Error(), "NodeName must not be empty")
}

func TestBindRequest_NodeNameFor(t *testing.T) {
	tests := []struct {
		name     string
		req      *BindRequest
		uid      types.UID
		expected string
	}{
		{
			name: "from NodeNames map",
			req: &BindRequest{
				NodeNames: map[types.UID]string{
					"uid-A": "node-1",
					"uid-B": "node-2",
				},
				NodeName: "fallback-node",
			},
			uid:      "uid-B",
			expected: "node-2",
		},
		{
			name: "fallback to NodeName when not in map",
			req: &BindRequest{
				NodeNames: map[types.UID]string{
					"uid-A": "node-1",
				},
				NodeName: "fallback-node",
			},
			uid:      "uid-missing",
			expected: "fallback-node",
		},
		{
			name: "fallback to NodeName when NodeNames is nil",
			req: &BindRequest{
				NodeName: "single-node",
			},
			uid:      "uid-any",
			expected: "single-node",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.req.NodeNameFor(tt.uid))
		})
	}
}

func TestBindRequest_UniqueNodeNames(t *testing.T) {
	tests := []struct {
		name     string
		req      *BindRequest
		expected int // number of unique nodes
	}{
		{
			name: "from NodeNames map with duplicates",
			req: &BindRequest{
				NodeNames: map[types.UID]string{
					"uid-1": "node-A",
					"uid-2": "node-B",
					"uid-3": "node-A", // duplicate
				},
			},
			expected: 2,
		},
		{
			name: "fallback to single NodeName",
			req: &BindRequest{
				NodeName: "node-single",
			},
			expected: 1,
		},
		{
			name:     "both empty",
			req:      &BindRequest{},
			expected: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Len(t, tt.req.UniqueNodeNames(), tt.expected)
		})
	}
}
