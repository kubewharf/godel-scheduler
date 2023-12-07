/*
Copyright 2019 The Kubernetes Authors.

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

package util

// Events that trigger scheduler queue to change.
const (
	// Unknown event
	Unknown = "Unknown"
	// PodAdd is the event when a new pod is added to API server.
	PodAdd = "PodAdd"
	// NodeAdd is the event when a new node is added to the cluster.
	NodeAdd = "NodeAdd"
	// NMNodeAdd is the event when a new NMNode is added to the cluster.
	NMNodeAdd = "NMNodeAdd"
	// CNRAdd is the event when a new CNR is added to the cluster.
	CNRAdd = "CNRAdd"
	// NodePartitionScaleOut is the event when scheduler's node partition changes from Physical to Logical.
	NodePartitionScaleOut = "NodePartitionScaleOut"
	// ScheduleAttemptFailure is the event when a schedule attempt fails.
	ScheduleAttemptFailure = "ScheduleAttemptFailure"
	// CreateUnitSucceed
	CreateUnitSucceed = "CreateUnitSucceed"
	// WaitingComplete is the event when a pod finishes waiting.
	WaitingComplete = "WaitingComplete"
	// BackoffComplete is the event when a pod finishes backoff.
	BackoffComplete = "BackoffComplete"
	// UnitReady is the event when an unit's ready to be scheduled.
	UnitReady = "UnitReady"
	// UnschedulableTimeout is the event when a pod stays in unschedulable for longer than timeout.
	UnschedulableTimeout = "UnschedulableTimeout"
	// AssignedPodAdd is the event when a pod is added that causes pods with matching affinity terms
	// to be more schedulable.
	AssignedPodAdd = "AssignedPodAdd"
	// AssignedPodUpdate is the event when a pod is updated that causes pods with matching affinity
	// terms to be more schedulable.
	AssignedPodUpdate = "AssignedPodUpdate"
	// AssignedPodDelete is the event when a pod is deleted that causes pods with matching affinity
	// terms to be more schedulable.
	AssignedPodDelete = "AssignedPodDelete"
	// PvAdd is the event when a persistent volume is added in the cluster.
	PvAdd = "PvAdd"
	// PvUpdate is the event when a persistent volume is updated in the cluster.
	PvUpdate = "PvUpdate"
	// PvcAdd is the event when a persistent volume claim is added in the cluster.
	PvcAdd = "PvcAdd"
	// PvcUpdate is the event when a persistent volume claim is updated in the cluster.
	PvcUpdate = "PvcUpdate"
	// StorageClassAdd is the event when a StorageClass is added in the cluster.
	StorageClassAdd = "StorageClassAdd"
	// ServiceAdd is the event when a service is added in the cluster.
	ServiceAdd = "ServiceAdd"
	// ServiceUpdate is the event when a service is updated in the cluster.
	ServiceUpdate = "ServiceUpdate"
	// ServiceDelete is the event when a service is deleted in the cluster.
	ServiceDelete = "ServiceDelete"
	// CSINodeAdd is the event when a CSI node is added in the cluster.
	CSINodeAdd = "CSINodeAdd"
	// CSINodeUpdate is the event when a CSI node is updated in the cluster.
	CSINodeUpdate = "CSINodeUpdate"
	// NodeSpecUnschedulableChange is the event when unschedulable node spec is changed.
	NodeSpecUnschedulableChange = "NodeSpecUnschedulableChange"
	// NodeAllocatableChange is the event when node allocatable is changed.
	NodeAllocatableChange = "NodeAllocatableChange"
	// NodeLabelsChange is the event when node label is changed.
	NodeLabelChange = "NodeLabelChange"
	// NodeTaintsChange is the event when node taint is changed.
	NodeTaintChange = "NodeTaintChange"
	// NodeConditionChange is the event when node condition is changed.
	NodeConditionChange = "NodeConditionChange"
	// PodGroupAdd is the event when a pod group is added in the cluster.
	PodGroupAdd = "PodGroupAdd"
	// PodGroupUpdate is the event when a pod group is updated in the cluster.
	PodGroupUpdate = "PodGroupUpdate"
)
