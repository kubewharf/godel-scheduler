/*
Copyright 2018 The Kubernetes Authors.

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

package cache

import (
	v1 "k8s.io/api/core/v1"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

// SchedulerCache collects pods' information and provides node-level aggregated information.
// It's intended for generic scheduler to do efficient lookup.
// SchedulerCache's operations are pod centric. It does incremental updates based on pod events.
// Pod events are sent via network. We don't have guaranteed delivery of all events:
// We use Reflector to list and watch from remote.
// Reflector might be slow and do a relist, which would lead to missing events.
//
// State Machine of a pod's events in scheduler's cache:
//
//	+-------------------------------------------+  +----+
//	|                            Add            |  |    |
//	|                                           |  |    | Update
//	+      Assume                Add            v  v    |
//
// Initial +--------> Assumed +------------+---> Added <--+
//
//	^                +   +               |       +
//	|                |   |               |       |
//	|                |   |           Add |       | Remove
//	|                |   |               |       |
//	|                |   |               +       |
//	+----------------+   +-----------> Expired   +----> Deleted
//	      Forget             Expire
//
// Note that an assumed pod can expire, because if we haven't received Add event notifying us
// for a while, there might be some problems and we shouldn't keep the pod in cache anymore.
//
// Note that "Initial", "Expired", and "Deleted" pods do not actually exist in cache.
// Based on existing use cases, we are making the following assumptions:
//   - No pod would be assumed twice
//   - A pod could be added without going through scheduler. In this case, we will see Add but not Assume event.
//   - If a pod wasn't added, it wouldn't be removed or updated.
//   - Both "Expired" and "Deleted" are valid end states. In case of some problems, e.g. network issue,
//     a pod might have changed its state (e.g. added and deleted) without delivering notification to the cache.
type SchedulerCache interface {
	commoncache.ClusterCache

	// Dump takes a snapshot of the current cache. This is used for debugging
	// purposes only and shouldn't be confused with UpdateSnapshot function.
	// This method is expensive, and should be only used in non-critical path.
	Dump() *commoncache.Dump

	// PodCount returns the number of pods in the cache (including those from deleted nodes).
	PodCount() (int, error)

	// GetPod returns the pod from the cache with the same namespace and the
	// same name of the specified pod.
	GetPod(pod *v1.Pod) (*v1.Pod, error)

	// IsAssumedPod returns true if the pod is assumed and not expired.
	IsAssumedPod(pod *v1.Pod) (bool, error)

	// SetNodeInPartition sets node in partition of scheduler
	SetNodeInPartition(nodeName string) error

	// SetNodeOutOfPartition sets node out of partition of scheduler
	SetNodeOutOfPartition(nodeName string) error

	// NodeInThisPartition returns whether this node is in this scheduler's partition
	NodeInThisPartition(nodeName string) bool

	// UpdateSnapshot updates the passed infoSnapshot to the current contents of SchedulerCache.
	// The node info contains aggregated information of pods scheduled (including assumed to be)
	// on this node.
	// The snapshot only includes Nodes that are not deleted at the time this function is called.
	// nodeinfo.GetNode() is guaranteed to be not nil for all the nodes in the snapshot.
	UpdateSnapshot(snapshot *Snapshot) error

	// GetUnitStatus return the UnitStatus
	GetUnitStatus(unitKey string) unitstatus.UnitStatus
	// SetUnitSchedulingStatus get the scheduling status
	GetUnitSchedulingStatus(unitKey string) unitstatus.SchedulingStatus
	// SetUnitSchedulingStatus set the scheduling status
	SetUnitSchedulingStatus(unitKey string, status unitstatus.SchedulingStatus)

	// AssumePod assumes a pod scheduled and aggregates the pod's information into its node.
	// The implementation also decides the policy to expire pod before being confirmed (receiving Add event).
	// After expiration, its information would be subtracted.
	AssumePod(podInfo *framework.CachePodInfo) error
	// ForgetPod removes an assumed pod from cache.
	ForgetPod(podInfo *framework.CachePodInfo) error
	// FinishReserving signals that cache for assumed pod can be expired
	FinishReserving(pod *v1.Pod) error

	// ScrapeCollectable updates store with cache.nodeStore incrementally
	ScrapeCollectable(store generationstore.RawStore)
}
