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

package cache

import (
	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

// Dump is a dump of the cache state.
type Dump struct {
	AssumedPods map[string]bool
	Nodes       map[string]framework.NodeInfo
}

// ClusterCache collects pods' information and provides node-level aggregated information.
// It's intended for generic scheduler to do efficient lookup.
// Cache's operations are pod centric. It does incremental updates based on pod events.
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
// for a while, there might be some problems, and we shouldn't keep the pod in cache anymore.
//
// Note that "Initial", "Expired", and "Deleted" pods do not actually exist in cache.
// Based on existing use cases, we are making the following assumptions:
//   - No pod would be assumed twice
//   - A pod could be added without going through scheduler. In this case, we will see Add but not Assume event.
//   - If a pod wasn't added, it wouldn't be removed or updated.
//   - Both "Expired" and "Deleted" are valid end states. In case of some problems, e.g. network issue,
//     a pod might have changed its state (e.g. added and deleted) without delivering notification to the cache.
type ClusterCache interface {
	// AddPod either confirms a pod if it's assumed, or adds it back if it's expired.
	// If added back, the pod's information would be added again.
	AddPod(pod *v1.Pod) error
	// UpdatePod removes oldPod's information and adds newPod's information.
	UpdatePod(oldPod, newPod *v1.Pod) error
	// RemovePod removes a pod. The pod's information would be subtracted from assigned node.
	RemovePod(pod *v1.Pod) error

	// AddNode adds overall information about node.
	AddNode(node *v1.Node) error
	// UpdateNode updates overall information about node.
	UpdateNode(oldNode, newNode *v1.Node) error
	// RemoveNode removes overall information about node.
	RemoveNode(node *v1.Node) error

	// AddNMNode adds overall information about nmnode.
	AddNMNode(nmNode *nodev1alpha1.NMNode) error
	// UpdateNMNode updates overall information about nmnode.
	UpdateNMNode(oldNMNode, newNMNode *nodev1alpha1.NMNode) error
	// RemoveNMNode removes overall information about nmnode.
	RemoveNMNode(nmNode *nodev1alpha1.NMNode) error

	// AddCNR adds custom resource information about node
	AddCNR(cnr *katalystv1alpha1.CustomNodeResource) error
	// UpdateCNR updates custom resource information about node.
	UpdateCNR(oldCNR, newCNR *katalystv1alpha1.CustomNodeResource) error
	// RemoveCNR removes custom resource information about node.
	RemoveCNR(cnr *katalystv1alpha1.CustomNodeResource) error

	// AddPodGroup adds overall information about pod group.
	AddPodGroup(podGroup *schedulingv1a1.PodGroup) error
	// UpdatePodGroup update information about pod group.
	UpdatePodGroup(oldPodGroup, newPodGroup *schedulingv1a1.PodGroup) error
	// RemovePodGroup DeletePodGroup remove overall information about pod group.
	RemovePodGroup(podGroup *schedulingv1a1.PodGroup) error

	AddPDB(pdb *policy.PodDisruptionBudget) error
	UpdatePDB(oldPdb, newPdb *policy.PodDisruptionBudget) error
	DeletePDB(pdb *policy.PodDisruptionBudget) error

	AddOwner(ownerType, key string, labels map[string]string) error
	UpdateOwner(ownerType, key string, oldLabels, newLabels map[string]string) error
	DeleteOwner(ownerType, key string) error
}
