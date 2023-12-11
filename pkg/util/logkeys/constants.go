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

package logkeys

const (
	// Godel-related
	AssumedNode               = "assumedNode"
	ClusterIndex              = "clusterIndex"
	ConstraintType            = "constraintType"
	CurrentNode               = "currentNode"
	DeviceInfo                = "deviceInfo"
	EventMsg                  = "eventMsg"
	FakePod                   = "fakePod"
	MemoryEnhancementAnnValue = "memoryEnhancementAnnValue"
	MicroTopologyAnnValue     = "microTopologyAnnValue"
	MinMember                 = "minMember"
	MovementName              = "movementName"
	MovementObject            = "movementObject"
	Namespaces                = "namespaces"
	Plugin                    = "plugin"
	PluginArgs                = "pluginArgs"
	NodeGroup                 = "nodeGroup"
	PlaceholderName           = "placeholderName"
	PodDeletionTimestamp      = "podDeletionTimestamp"
	PodGroup                  = "podGroup"
	PodGroupKey               = "podGroupKey"
	PodKey                    = "podKey"
	PodPhase                  = "podPhase"
	PreemptionPolicyName      = "preemptionPolicyName"
	QueueInfo                 = "queueInfo"
	ReservationCreateTime     = "reservationCreateTime"
	ResourceType              = "resourceType"
	SchedulerName             = "schedulerName"
	ShareGPUDeviceInfo        = "shareGPUDeviceInfo"
	ShareGPUIDIndexAnnValue   = "shareGPUIDIndexAnnValue"
	SubCluster                = "subCluster"
	SubClusterConfig          = "subClusterConfig"

	// K8s-related
	Annotation          = "annotation"
	AvailableResources  = "availableResources"
	ControllerKey       = "controllerKey"
	DeletedKeys         = "deletedKeys"
	Labels              = "labels"
	Node                = "node"
	NodeIP              = "nodeIP"
	NodeInfo            = "nodeInfo"
	NodeName            = "nodeName"
	NumberOfNodes       = "numberOfNodes"
	MissedNodes         = "missedNodes"
	MissedPods          = "missedPods"
	NewPod              = "newPod"
	NumberOfPods        = "numberOfPods"
	OldPod              = "oldPod"
	PVCName             = "pvcName"
	Pod                 = "pod"
	PodAntiAffinityTerm = "podAntiAffinityTerm"
	PodOwnerName        = "podOwnerName"
	PodUID              = "podUID"
	RedundantNodes      = "redundantNodes"
	RedundantPods       = "redundantPods"
	ReplicaSet          = "replicaSet"
	Revision            = "revision"
	Taints              = "taints"
	PodState            = "podState"

	// Metrics-related
	CurrentSize = "currentSize"
	MetricName  = "metricName"
	SizeLimit   = "sizeLimit"

	// Error-related
	Error       = "err"
	PreempErr   = "preempErr"
	ScheduleErr = "scheduleErr"

	// Others
	Component   = "component"
	Config      = "config"
	Debug       = "debug"
	DefaultHost = "defaultHost"
	Expectation = "expectation"
	File        = "file"
	GinkgoNode  = "ginkgoNode"
	Item        = "item"
	LengthLimit = "lengthLimit"
	Message     = "message"
	NewObject   = "newObject"
	NMNode      = "nmNode"
	Object      = "object"
	ObjectKey   = "objectKey"
	OldObject   = "oldObject"
	Provider    = "provider"
	Response    = "response"
	StatusCode  = "statusCode"
	TimedOut    = "timedOut"
)
