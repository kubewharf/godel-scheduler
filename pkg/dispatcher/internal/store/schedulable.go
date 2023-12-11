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

package store

import (
	v1 "k8s.io/api/core/v1"

	"github.com/kubewharf/godel-scheduler/pkg/dispatcher/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// Schedulable represents an entity that can be scheduled such as an application
// or a queue. It provides a common interface so that algorithms such as fair
// sharing can be applied both within a queue and across queues.
type Schedulable interface {
	// GetDemand returns the maximum number of resources required by this
	// schedulable. This is defined as number of currently utilized resources +
	// number of not launched resources (that are either not yet launched or need
	// to be speculated).
	GetDemand(resourceType podutil.PodResourceType) util.DRFResource
	// GetResourceUsage returns the aggregate amount of resources consumed by
	// the schedulable.
	GetResourceUsage(resourceType podutil.PodResourceType) util.DRFResource
	// GetMinShare returns the minimum resource share assigned to the schedulable.
	GetMinShare(resourceType podutil.PodResourceType) util.DRFResource
	// GetMaxShare returns the maximum resource share assigned to the schedulable.
	GetMaxShare(resourceType podutil.PodResourceType) util.DRFResource
	// GetFairShare returns the fair share assigned to this schedulable.
	GetFairShare(resourceType podutil.PodResourceType) util.DRFResource
	// SetFairShare assigns a fair share to this schedulable.
	SetFairShare(rName v1.ResourceName, value int64, resourceType podutil.PodResourceType)
}
