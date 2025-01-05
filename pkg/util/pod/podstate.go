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

package pod

import (
	"fmt"

	nodelister "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/node/v1alpha1"
	schedulerv1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	lister "k8s.io/client-go/listers/core/v1"
)

const (
	// PodStateAnnotationKey is a pod annotation key, value is the pod state
	PodStateAnnotationKey = "godel.bytedance.com/pod-state"

	// SchedulerAnnotationKey is a pod annotation key, value is the scheduler id who is responsible for scheduling this pod
	// 表示 pod 由哪个 scheudler 负责调度
	SchedulerAnnotationKey = "godel.bytedance.com/selected-scheduler"

	// FailedSchedulersAnnotationKey is a pod annotation key, value is schedulers who have already tried and failed to schedule the pod
	// this is used only when Node Partition is physical
	FailedSchedulersAnnotationKey = "godel.bytedance.com/failed-schedulers"

	// TraceContext represents the span context for the pod
	TraceContext = "trace-context"

	// AssumedNodeAnnotationKey is a pod annotation key, value is the assumed node name chosen by one scheduler
	// the scheduler will reserve the allocated resource for the pod. TODO: should all schedulers be aware of this ?
	// TODO: figure out if we can return multiple nodes ? if so how to deal with scheduler cache ?
	// scheduler 为 pod assume 的 node 的名称， scheudler 将为 pod 保留分配的资源。
	AssumedNodeAnnotationKey = "godel.bytedance.com/assumed-node"

	// TODO: figure out how to define cross node constraint and whether we need this annotation
	// AssumedCrossNodeAnnotationKey is a pod annotation key, value is the assumed node name chosen by one scheduler
	// the scheduler will reserve the allocated resource for the pod.
	// Pod need to resolve cross node constraint
	AssumedCrossNodeAnnotationKey = "godel.bytedance.com/assumed-cross-node"

	// PodGroupNameAnnotationKey is pod annotation key, the value is name of PodGroup custom resource.
	// PodGroupNameAnnotationKey 是 Pod 注释键，值是 PodGroup 自定义资源的名称。
	PodGroupNameAnnotationKey = "godel.bytedance.com/pod-group-name"

	// PotentialVictimsAnnotationKey is a pod annotation key, value is the victims chosen by dispatcher
	// this is used for best effort application pods
	// values can be like: [{queue: queue1, application: app1}, {queue: queue2, application: app2}]...
	PotentialVictimsAnnotationKey = "godel.bytedance.com/potential-victims"

	// NominatedNodeAnnotationKey is a pod annotation key,
	// value is the node name chosen by scheduler for placing the pending pod by evicting others
	// value can be like: {node: node1, victims: pod1, pod2...}
	// the scheduler will reserve the allocated resource for the pod. TODO: should all schedulers be aware of this ?
	// TODO: figure out if we can return multiple nodes ? if so how to deal with scheduler cache ?
	// 表示 scheduler 为 pod 选择的抢占节点，值可以是 {node: node1, victims: pod1, pod2...}，调度器将为 pod 保留分配的资源。
	NominatedNodeAnnotationKey = "godel.bytedance.com/nominated-node"

	// ATTENTION: This annotation key will be DEPRECATED in the future and REPLACED by `QoSLevelKey=katalyst.kubewharf.io/qos_level`.
	// PodResourceTypeAnnotationKey is a pod annotation key, value is the pod resource type (guaranteed or best-effort)
	PodResourceTypeAnnotationKey = "godel.bytedance.com/pod-resource-type"

	// PodLauncherAnnotationKey is a pod annotation key, value is the launcher of this pod (kubelet or node-manager)
	// PodLauncherAnnotationKey 是一个 Pod 注解键，其值是该 Pod 的启动器（kubelet 或 node-manager）。
	PodLauncherAnnotationKey = "godel.bytedance.com/pod-launcher"

	// InitialHandledTimestampAnnotationKey is a pod annotation key, value is the timestamp when the pod is first handled by Godel Scheduler
	// InitialHandledTimestampAnnotationKey 是 pod 注释键，值是 pod 首次被 Godel 调度程序处理的时间戳。
	InitialHandledTimestampAnnotationKey = "godel.bytedance.com/initial-handled-timestamp"

	// MicroTopologyKey is an annotation key for pod micro topology assigned by scheduler&binder
	MicroTopologyKey = "godel.bytedance.com/micro-topology"

	IgnorePodsLimitAnnotationKey = "godel.bytedance.com/ignore-pods-limit"

	ProtectionDurationFromPreemptionKey = "godel.bytedance.com/protection-duration-from-preemption"

	UnitScheduledIndexAnnotationKey = "godel.bytedance.com/scheduled-index-in-scheduling-unit"

	// E2EExcludedPodAnnotationKey is a pod annotation key, pods with this annotation will be excluded when calculating e2e latency
	E2EExcludedPodAnnotationKey = "godel.bytedance.com/e2e-excluded"

	IncreasePercentageOfNodesToScoreAnnotationKey = "godel.bytedance.com/increase-percentage-of-nodes-to-score"

	IncreasePercentageOfNodesToScore = "true"

	MovementNameKey = "godel.bytedance.com/movement-name"

	// Pods with same request template share the same requirements.
	PodRequestTemplateAnnotationKey = "godel.bytedance.com/request-template"
	// reservation related
	MatchedReservationPlaceholderKey = "godel.bytedance.com/matched-reservation-placeholder"
	ReservationTTLKey                = "godel.bytedance.com/reservation-ttl"
)

type PodState string

// please refer to the file: pod_state_machine.go in the same package for pod state change diagram.
const (
	PodNotInit    PodState = ""
	PodPending    PodState = "pending"
	PodDispatched PodState = "dispatched"
	PodAssumed    PodState = "assumed"
)

func GetPodState(annotations map[string]string) PodState {
	if annotations == nil {
		return PodNotInit
	}
	val, ok := annotations[PodStateAnnotationKey]
	if !ok {
		return PodNotInit
	}
	return PodState(val)
}

type PodLauncher string

const (
	Kubelet     PodLauncher = "kubelet"
	NodeManager PodLauncher = "node-manager"
)

var (
	PodLauncherUnsupportError = fmt.Errorf("pod launcher only allow %v", []PodLauncher{Kubelet, NodeManager})
	PodLauncherMissedError    = fmt.Errorf("missing pod launcher")
)

type PodResourceType string

const (
	GuaranteedPod PodResourceType = "guaranteed"
	BestEffortPod PodResourceType = "best-effort"
	// UndefinedPod only used in metrics label
	UndefinedPod PodResourceType = "undefined"

	// Default priority values for pods with different resource types
	DefaultPriorityValueForGuaranteedPod int32 = 100
	DefaultPriorityValueForBestEffortPod int32 = 40
)

var (
	PodResourceTypeUnsupportError = fmt.Errorf("pod resource type only allow %v", []PodResourceType{GuaranteedPod, BestEffortPod})
	PodResourceTypeMissedError    = fmt.Errorf("missing pod resource type")
)

// PendingPod checks if the given pod is in pending state
// 1. godel.bytedance.com/pod-state == pending 或者 "" && (表示pod 为 pending 状态)
// 2. godel.bytedance.com/selected-scheduler 为 "" && （表示没有 scheudler 负责调度这个 pod）
// 3. godel.bytedance.com/assumed-node 为 "" && （表示 pod 没有被 scheduler assume 到某个节点上）
// 4. godel.bytedance.com/nominated-node 为 "" && （表示 scheduler 没有为 pod 选择抢占节点）
// 5. pod.Spec.NodeName 为 ""
func PendingPod(pod *v1.Pod) bool {
	if pod.Annotations != nil &&
		(pod.Annotations[PodStateAnnotationKey] == string(PodPending) || len(pod.Annotations[PodStateAnnotationKey]) == 0) &&
		len(pod.Annotations[SchedulerAnnotationKey]) == 0 &&
		len(pod.Annotations[AssumedNodeAnnotationKey]) == 0 &&
		len(pod.Annotations[NominatedNodeAnnotationKey]) == 0 &&
		len(pod.Spec.NodeName) == 0 {
		return true
	}
	return false
}

// DispatchedPod checks if the given pod is in dispatched state
// godel.bytedance.com/pod-state == dispatched && (表示 pod 为 dispatched 状态)
// godel.bytedance.com/selected-scheduler 不为 "" && （表示选定了 scheudler 为这个 pod 负责调度）
// godel.bytedance.com/assumed-node 为 "" && （表示 pod 没有被 scheduler assume 到某个节点上）
// godel.bytedance.com/nominated-node 为 "" && （表示 scheduler 没有为 pod 选择抢占节点）
// pod.Spec.NodeName 为 ""
func DispatchedPod(pod *v1.Pod) bool {
	if pod.Annotations != nil &&
		pod.Annotations[PodStateAnnotationKey] == string(PodDispatched) &&
		len(pod.Annotations[SchedulerAnnotationKey]) != 0 &&
		len(pod.Annotations[AssumedNodeAnnotationKey]) == 0 &&
		len(pod.Annotations[NominatedNodeAnnotationKey]) == 0 &&
		len(pod.Spec.NodeName) == 0 {
		return true
	}
	return false
}

func DispatchedPodOfGodel(pod *v1.Pod, schedulerName string) bool {
	if LegalPodResourceTypeAndLauncher(pod) &&
		responsibleForPod(pod, schedulerName) &&
		DispatchedPod(pod) {
		return true
	}
	return false
}

func AssumedPodOfGodel(pod *v1.Pod, schedulerName string) bool {
	if LegalPodResourceTypeAndLauncher(pod) &&
		responsibleForPod(pod, schedulerName) &&
		AssumedPod(pod) {
		return true
	}
	return false
}

// PendingPodOfGodel 判断 pod 是否为 godel-scheduler 待调度的 pending pod
func PendingPodOfGodel(pod *v1.Pod, schedulerName string) bool {
	if LegalPodResourceTypeAndLauncher(pod) && // 判断 pod 是否可以获取到 launcher && Qos
		responsibleForPod(pod, schedulerName) && // pod.Spec.SchedulerName 为 godel-scheduler || default-scheduler || ""
		PendingPod(pod) {
		return true
	}
	return false
}

func DispatchedPodOfThisScheduler(pod *v1.Pod, schedulerID string) bool {
	if pod.Annotations != nil &&
		pod.Annotations[SchedulerAnnotationKey] == schedulerID &&
		DispatchedPod(pod) {
		return true
	}
	return false
}

func DispatchedPodOfOtherScheduler(pod *v1.Pod, schedulerID string) bool {
	if pod.Annotations != nil &&
		pod.Annotations[SchedulerAnnotationKey] != schedulerID &&
		DispatchedPod(pod) {
		return true
	}
	return false
}

// assumedOrNominatedNodeIsSet checks if the AssumedNodeAnnotationKey or NominatedNodeAnnotationKey is set
func assumedOrNominatedNodeIsSet(pod *v1.Pod) bool {
	if pod.Annotations != nil {
		if len(pod.Annotations[AssumedNodeAnnotationKey]) == 0 && len(pod.Annotations[NominatedNodeAnnotationKey]) != 0 {
			return true
		}
		if len(pod.Annotations[AssumedNodeAnnotationKey]) != 0 && len(pod.Annotations[NominatedNodeAnnotationKey]) == 0 {
			return true
		}
	}
	return false
}

// AssumedPod checks if the given pod is in assumed state
func AssumedPod(pod *v1.Pod) bool {
	if pod.Annotations != nil &&
		pod.Annotations[PodStateAnnotationKey] == string(PodAssumed) &&
		len(pod.Annotations[SchedulerAnnotationKey]) != 0 &&
		assumedOrNominatedNodeIsSet(pod) &&
		len(pod.Spec.NodeName) == 0 {
		return true
	}
	return false
}

// BoundPod checks if the given pod is bound
func BoundPod(pod *v1.Pod) bool {
	return len(pod.Spec.NodeName) != 0
}

// AbnormalPodState checks if the given pod is in abnormal state
func AbnormalPodState(pod *v1.Pod) bool {
	// 已调度的pod，直接返回false
	if BoundPod(pod) {
		return false
	}

	// "godel.bytedance.com/pod-state"
	// 1. 为空但实际不是 pending pod ||
	// 2. 是"pending" 但实际不是 pending pod ||
	// 3. 为"dispatched" 但实际不是 Dispatched Pod ||
	// 4. 为"assumed" 但实际不是 assumed Pod，则返回true
	switch pod.Annotations[PodStateAnnotationKey] {
	case "":
		fallthrough
	case string(PodPending):
		if !PendingPod(pod) {
			return true
		} else {
			return false
		}
	case string(PodDispatched):
		if !DispatchedPod(pod) {
			return true
		} else {
			return false
		}
	case string(PodAssumed):
		if !AssumedPod(pod) {
			return true
		} else {
			return false
		}
	default:
		return true
	}
}

func AbnormalPodStateOfGodel(pod *v1.Pod, schedulerName string) bool {
	if AbnormalPodState(pod) && responsibleForPod(pod, schedulerName) {
		return true
	}
	return false
}

// NodeExists checks if the given node exists
func NodeExists(nodeName string, nodeLister lister.NodeLister, nmNodeLister nodelister.NMNodeLister) (bool, error) {
	if _, err := nodeLister.Get(nodeName); err == nil {
		return true, nil
	} else if !errors.IsNotFound(err) {
		return false, err
	}

	if _, err := nmNodeLister.Get(nodeName); err == nil {
		return true, nil
	} else if !errors.IsNotFound(err) {
		return false, err
	}
	// node is not found, do not return error here
	return false, nil
}

// SchedulerExists checks if the given scheduler exists
func SchedulerExists(schedulerName string, schedulerLister schedulerv1alpha1.SchedulerLister) (bool, error) {
	if _, err := schedulerLister.Get(schedulerName); err == nil {
		return true, nil
	} else if !errors.IsNotFound(err) {
		return false, err
	}
	// scheduler is not found, do not return error here
	return false, nil
}

func IgnorePodsLimit(pod *v1.Pod) bool {
	if pod == nil || len(pod.Annotations) == 0 {
		return false
	}

	_, exist := pod.Annotations[IgnorePodsLimitAnnotationKey]
	return exist
}
