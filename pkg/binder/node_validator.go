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

	v1 "k8s.io/api/core/v1"

	nodeutil "github.com/kubewharf/godel-scheduler/pkg/util/node"
)

// NodeGetter is a function type that retrieves a Node by name.
// It decouples NodeValidator from any specific Node store implementation
// (e.g., informer cache, API server, fake test getter).
type NodeGetter func(nodeName string) (*v1.Node, error)

// NodeValidator checks whether a node still belongs to the current Scheduler's
// partition before allowing a bind operation to proceed. During node reshuffles,
// a previously assigned node may be re-assigned to a different Scheduler. The
// NodeValidator prevents stale bindings in such cases.
type NodeValidator struct {
	schedulerName string
	nodeGetter    NodeGetter
}

// NewNodeValidator creates a NodeValidator for the given schedulerName.
// nodeGetter is called each time Validate is invoked to fetch the latest
// node state. It must not be nil.
func NewNodeValidator(schedulerName string, nodeGetter NodeGetter) *NodeValidator {
	return &NodeValidator{
		schedulerName: schedulerName,
		nodeGetter:    nodeGetter,
	}
}

// Validate checks that the node identified by nodeName is owned by this
// Scheduler (i.e., its godel.bytedance.com/scheduler-name annotation matches
// schedulerName). Returns nil on success, or an error explaining the mismatch.
func (v *NodeValidator) Validate(nodeName string) error {
	if v.nodeGetter == nil {
		return fmt.Errorf("nodeGetter is nil in NodeValidator")
	}

	node, err := v.nodeGetter(nodeName)
	if err != nil {
		return fmt.Errorf("failed to get node %q: %w", nodeName, err)
	}

	if node == nil {
		return fmt.Errorf("node %q not found (nil)", nodeName)
	}

	owner := ""
	if node.Annotations != nil {
		owner = node.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]
	}

	// When the annotation is absent the node has not been partitioned yet
	// (e.g., single-scheduler deployment, or Dispatcher has not assigned it).
	// In that case we allow the bind to proceed — only reject when the
	// annotation is present AND belongs to a different scheduler.
	if owner == "" {
		return nil
	}

	if owner != v.schedulerName {
		return &NodeOwnershipError{
			Node:     nodeName,
			Expected: v.schedulerName,
			Actual:   owner,
		}
	}

	return nil
}

// NodeOwnershipError indicates that a node does not belong to the expected
// Scheduler instance. It is a typed error so callers can use errors.As
// to distinguish ownership failures from other errors.
type NodeOwnershipError struct {
	Node     string
	Expected string
	Actual   string
}

func (e *NodeOwnershipError) Error() string {
	if e.Actual == "" {
		return fmt.Sprintf("node %q has no scheduler-name annotation (expected %q)", e.Node, e.Expected)
	}
	return fmt.Sprintf("node %q belongs to scheduler %q, not %q", e.Node, e.Actual, e.Expected)
}

// IsNodeOwnershipError returns true if err is or wraps a NodeOwnershipError.
func IsNodeOwnershipError(err error) bool {
	var noe *NodeOwnershipError
	return errors.As(err, &noe)
}
