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
	"context"
	"errors"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

var (
	// ErrInvalidRequest is returned when a BindRequest fails validation.
	ErrInvalidRequest = errors.New("invalid bind request")

	// ErrBinderNotRunning is returned when BindUnit is called on a Binder that has not been started.
	ErrBinderNotRunning = errors.New("binder is not running")
)

// BinderInterface defines the contract for binding scheduling decisions to the API server.
// It abstracts the binding logic so that both standalone (shared) and embedded (per-scheduler)
// Binder implementations can be used interchangeably.
type BinderInterface interface {
	// BindUnit performs conflict checking and binds all Pods in the given BindRequest
	// to their target node. It returns a BindResult indicating which Pods succeeded
	// and which failed.
	//
	// The caller (typically the Scheduler's unit_scheduler) invokes this after a
	// successful scheduling decision. In embedded mode, this replaces the PatchPod
	// path that was previously used to communicate with the standalone Binder.
	BindUnit(ctx context.Context, req *BindRequest) (*BindResult, error)

	// Start initialises and starts the Binder's internal workers. It must be called
	// before BindUnit. Calling Start on an already-running Binder returns an error.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the Binder, draining in-flight bind operations.
	Stop()
}

// BindRequest encapsulates everything the Binder needs to execute a bind operation
// for a scheduling unit (which may contain one or more Pods).
type BindRequest struct {
	// Unit is the scheduling unit that was scheduled. Must not be nil.
	Unit *framework.QueuedUnitInfo

	// Pods is the list of Pods belonging to this unit that need to be bound.
	// Must contain at least one entry.
	Pods []*framework.QueuedPodInfo

	// NodeName is the node selected by the Scheduler for this unit's Pods.
	// Used when ALL Pods in the unit are assigned to the same node (single-pod
	// units or same-node PodGroups). Ignored when NodeNames is non-empty.
	NodeName string

	// NodeNames maps each Pod UID to its target node name. This supports
	// PodGroups where different Pods may be scheduled to different nodes.
	// When set, it takes precedence over NodeName. Every Pod in Pods must
	// have an entry in this map for validation to pass.
	NodeNames map[types.UID]string

	// Victims is the set of Pods that should be preempted to make room for
	// this unit. May be nil if no preemption is required.
	Victims []*v1.Pod

	// SchedulerName identifies the Scheduler instance that produced this request.
	// Used for logging, metrics, and partition validation.
	SchedulerName string
}

// NodeNameFor returns the target node for the given Pod UID.
// It first checks NodeNames (per-pod mapping), then falls back to
// the blanket NodeName.
func (r *BindRequest) NodeNameFor(uid types.UID) string {
	if len(r.NodeNames) > 0 {
		if n, ok := r.NodeNames[uid]; ok {
			return n
		}
	}
	return r.NodeName
}

// UniqueNodeNames returns the deduplicated set of target node names
// referenced by this request.
func (r *BindRequest) UniqueNodeNames() []string {
	seen := make(map[string]struct{})
	var names []string
	if len(r.NodeNames) > 0 {
		for _, n := range r.NodeNames {
			if _, ok := seen[n]; !ok {
				seen[n] = struct{}{}
				names = append(names, n)
			}
		}
	} else if r.NodeName != "" {
		names = append(names, r.NodeName)
	}
	return names
}

// Validate checks that the BindRequest contains all mandatory fields.
func (r *BindRequest) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: request is nil", ErrInvalidRequest)
	}
	if r.Unit == nil {
		return fmt.Errorf("%w: Unit must not be nil", ErrInvalidRequest)
	}
	if len(r.Pods) == 0 {
		return fmt.Errorf("%w: Pods must not be empty", ErrInvalidRequest)
	}
	// At least one of NodeName or NodeNames must be provided.
	if r.NodeName == "" && len(r.NodeNames) == 0 {
		return fmt.Errorf("%w: NodeName must not be empty", ErrInvalidRequest)
	}
	// When NodeNames is provided, every Pod must have an entry.
	if len(r.NodeNames) > 0 {
		for _, qpi := range r.Pods {
			if qpi == nil || qpi.Pod == nil {
				continue
			}
			if _, ok := r.NodeNames[qpi.Pod.UID]; !ok {
				return fmt.Errorf("%w: NodeNames missing entry for pod %s/%s (UID %s)",
					ErrInvalidRequest, qpi.Pod.Namespace, qpi.Pod.Name, qpi.Pod.UID)
			}
		}
	}
	return nil
}

// BindResult reports the outcome of a BindUnit call.
type BindResult struct {
	// SuccessfulPods lists the UIDs of Pods that were successfully bound.
	SuccessfulPods []types.UID

	// FailedPods maps each failed Pod UID to the error that caused the failure.
	// An empty map (or nil) means all Pods succeeded.
	FailedPods map[types.UID]error
}

// AllSucceeded returns true if every Pod in the request was bound successfully.
func (r *BindResult) AllSucceeded() bool {
	return len(r.FailedPods) == 0
}

// AllFailed returns true if no Pod was bound successfully.
func (r *BindResult) AllFailed() bool {
	return len(r.SuccessfulPods) == 0 && len(r.FailedPods) > 0
}
