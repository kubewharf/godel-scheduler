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
	"fmt"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nodeutil "github.com/kubewharf/godel-scheduler/pkg/util/node"
)

// makeTestNode creates a simple Node with the given annotations.
func makeTestNode(name string, annotations map[string]string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: annotations,
		},
	}
}

func TestNodeValidator_Validate(t *testing.T) {
	tests := []struct {
		name          string
		schedulerName string
		nodeName      string
		nodeGetter    NodeGetter
		wantErr       bool
		wantOwnership bool // true if error should be NodeOwnershipError
		wantErrMsg    string
	}{
		{
			name:          "node belongs to current scheduler",
			schedulerName: "scheduler-A",
			nodeName:      "node-1",
			nodeGetter: func(nodeName string) (*v1.Node, error) {
				return makeTestNode(nodeName, map[string]string{
					nodeutil.GodelSchedulerNodeAnnotationKey: "scheduler-A",
				}), nil
			},
			wantErr: false,
		},
		{
			name:          "node belongs to another scheduler",
			schedulerName: "scheduler-A",
			nodeName:      "node-2",
			nodeGetter: func(nodeName string) (*v1.Node, error) {
				return makeTestNode(nodeName, map[string]string{
					nodeutil.GodelSchedulerNodeAnnotationKey: "scheduler-B",
				}), nil
			},
			wantErr:       true,
			wantOwnership: true,
			wantErrMsg:    `node "node-2" belongs to scheduler "scheduler-B", not "scheduler-A"`,
		},
		{
			name:          "node has no scheduler-name annotation",
			schedulerName: "scheduler-A",
			nodeName:      "node-3",
			nodeGetter: func(nodeName string) (*v1.Node, error) {
				return makeTestNode(nodeName, map[string]string{}), nil
			},
			wantErr:       true,
			wantOwnership: true,
			wantErrMsg:    `node "node-3" has no scheduler-name annotation (expected "scheduler-A")`,
		},
		{
			name:          "node has empty scheduler-name annotation",
			schedulerName: "scheduler-A",
			nodeName:      "node-4",
			nodeGetter: func(nodeName string) (*v1.Node, error) {
				return makeTestNode(nodeName, map[string]string{
					nodeutil.GodelSchedulerNodeAnnotationKey: "",
				}), nil
			},
			wantErr:       true,
			wantOwnership: true,
			wantErrMsg:    `node "node-4" has no scheduler-name annotation (expected "scheduler-A")`,
		},
		{
			name:          "node has nil annotations",
			schedulerName: "scheduler-A",
			nodeName:      "node-5",
			nodeGetter: func(nodeName string) (*v1.Node, error) {
				return makeTestNode(nodeName, nil), nil
			},
			wantErr:       true,
			wantOwnership: true,
		},
		{
			name:          "nodeGetter returns not found error",
			schedulerName: "scheduler-A",
			nodeName:      "node-missing",
			nodeGetter: func(nodeName string) (*v1.Node, error) {
				return nil, fmt.Errorf("node %q not found", nodeName)
			},
			wantErr:       true,
			wantOwnership: false,
		},
		{
			name:          "nodeGetter returns transient error",
			schedulerName: "scheduler-A",
			nodeName:      "node-err",
			nodeGetter: func(nodeName string) (*v1.Node, error) {
				return nil, fmt.Errorf("connection refused")
			},
			wantErr:       true,
			wantOwnership: false,
		},
		{
			name:          "nodeGetter returns nil node without error",
			schedulerName: "scheduler-A",
			nodeName:      "node-nil",
			nodeGetter: func(nodeName string) (*v1.Node, error) {
				return nil, nil
			},
			wantErr:       true,
			wantOwnership: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewNodeValidator(tt.schedulerName, tt.nodeGetter)
			err := v.Validate(tt.nodeName)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.wantOwnership {
				if !IsNodeOwnershipError(err) {
					t.Errorf("expected NodeOwnershipError, got %T: %v", err, err)
				}
				if tt.wantErrMsg != "" && err.Error() != tt.wantErrMsg {
					t.Errorf("error message = %q, want %q", err.Error(), tt.wantErrMsg)
				}
			}
			if err != nil && !tt.wantOwnership {
				if IsNodeOwnershipError(err) {
					t.Errorf("did NOT expect NodeOwnershipError, but got one: %v", err)
				}
			}
		})
	}
}

func TestNodeValidator_NilGetter(t *testing.T) {
	v := NewNodeValidator("scheduler-A", nil)
	err := v.Validate("node-1")
	if err == nil {
		t.Error("expected error when nodeGetter is nil")
	}
}

func TestNodeOwnershipError_ErrorMessage(t *testing.T) {
	tests := []struct {
		name    string
		err     *NodeOwnershipError
		wantMsg string
	}{
		{
			name: "with actual scheduler",
			err: &NodeOwnershipError{
				Node:     "node-1",
				Expected: "scheduler-A",
				Actual:   "scheduler-B",
			},
			wantMsg: `node "node-1" belongs to scheduler "scheduler-B", not "scheduler-A"`,
		},
		{
			name: "with empty actual (no annotation)",
			err: &NodeOwnershipError{
				Node:     "node-2",
				Expected: "scheduler-A",
				Actual:   "",
			},
			wantMsg: `node "node-2" has no scheduler-name annotation (expected "scheduler-A")`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", got, tt.wantMsg)
			}
		})
	}
}

func TestNodeOwnershipError_IsNodeOwnershipError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "direct NodeOwnershipError",
			err:  &NodeOwnershipError{Node: "n1", Expected: "s1", Actual: "s2"},
			want: true,
		},
		{
			name: "wrapped NodeOwnershipError",
			err:  fmt.Errorf("binding failed: %w", &NodeOwnershipError{Node: "n1", Expected: "s1", Actual: "s2"}),
			want: true,
		},
		{
			name: "generic error",
			err:  fmt.Errorf("something else"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNodeOwnershipError(tt.err)
			if got != tt.want {
				t.Errorf("IsNodeOwnershipError() = %v, want %v", got, tt.want)
			}

			// Also test errors.As extraction
			if tt.want {
				var noe *NodeOwnershipError
				if !errors.As(tt.err, &noe) {
					t.Error("errors.As failed for NodeOwnershipError")
				} else if noe.Node != "n1" {
					t.Errorf("extracted node = %q, want %q", noe.Node, "n1")
				}
			}
		})
	}
}
