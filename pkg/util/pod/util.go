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
	"strconv"
	"strings"
	"time"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/features"
)

var applicationKind = "Application"

const (
	AlignedResources = "godel.bytedance.com/aligned-resources"
)

// FindPort locates the container port for the given pod and portName.  If the
// targetPort is a number, use that.  If the targetPort is a string, look that
// string up in all named ports in all containers in the target pod.  If no
// match is found, fail.
func FindPort(pod *v1.Pod, svcPort *v1.ServicePort) (int, error) {
	portName := svcPort.TargetPort
	switch portName.Type {
	case intstr.String:
		name := portName.StrVal
		for _, container := range pod.Spec.Containers {
			for _, port := range container.Ports {
				if port.Name == name && port.Protocol == svcPort.Protocol {
					return int(port.ContainerPort), nil
				}
			}
		}
	case intstr.Int:
		return portName.IntValue(), nil
	}

	return 0, fmt.Errorf("no suitable port for manifest: %s", pod.UID)
}

// ContainerType signifies container type
type ContainerType int

const (
	// Containers is for normal containers
	Containers ContainerType = 1 << iota
	// InitContainers is for init containers
	InitContainers
	// EphemeralContainers is for ephemeral containers
	EphemeralContainers
)

// AllContainers specifies that all containers be visited
const AllContainers = InitContainers | Containers | EphemeralContainers

// AllFeatureEnabledContainers returns a ContainerType mask which includes all container
// types except for the ones guarded by feature gate.
func AllFeatureEnabledContainers() ContainerType {
	containerType := AllContainers
	if !utilfeature.DefaultFeatureGate.Enabled(features.EphemeralContainers) {
		containerType &= ^EphemeralContainers
	}
	return containerType
}

// ContainerVisitor is called with each container spec, and returns true
// if visiting should continue.
type ContainerVisitor func(container *v1.Container, containerType ContainerType) (shouldContinue bool)

// Visitor is called with each object name, and returns true if visiting should continue
type Visitor func(name string) (shouldContinue bool)

// VisitContainers invokes the visitor function with a pointer to every container
// spec in the given pod spec with type set in mask. If visitor returns false,
// visiting is short-circuited. VisitContainers returns true if visiting completes,
// false if visiting was short-circuited.
func VisitContainers(podSpec *v1.PodSpec, mask ContainerType, visitor ContainerVisitor) bool {
	if mask&InitContainers != 0 {
		for i := range podSpec.InitContainers {
			if !visitor(&podSpec.InitContainers[i], InitContainers) {
				return false
			}
		}
	}
	if mask&Containers != 0 {
		for i := range podSpec.Containers {
			if !visitor(&podSpec.Containers[i], Containers) {
				return false
			}
		}
	}
	if mask&EphemeralContainers != 0 {
		for i := range podSpec.EphemeralContainers {
			if !visitor((*v1.Container)(&podSpec.EphemeralContainers[i].EphemeralContainerCommon), EphemeralContainers) {
				return false
			}
		}
	}
	return true
}

// VisitPodSecretNames invokes the visitor function with the name of every secret
// referenced by the pod spec. If visitor returns false, visiting is short-circuited.
// Transitive references (e.g. pod -> pvc -> pv -> secret) are not visited.
// Returns true if visiting completed, false if visiting was short-circuited.
func VisitPodSecretNames(pod *v1.Pod, visitor Visitor) bool {
	for _, reference := range pod.Spec.ImagePullSecrets {
		if !visitor(reference.Name) {
			return false
		}
	}
	VisitContainers(&pod.Spec, AllContainers, func(c *v1.Container, containerType ContainerType) bool {
		return visitContainerSecretNames(c, visitor)
	})
	var source *v1.VolumeSource

	for i := range pod.Spec.Volumes {
		source = &pod.Spec.Volumes[i].VolumeSource
		switch {
		case source.AzureFile != nil:
			if len(source.AzureFile.SecretName) > 0 && !visitor(source.AzureFile.SecretName) {
				return false
			}
		case source.CephFS != nil:
			if source.CephFS.SecretRef != nil && !visitor(source.CephFS.SecretRef.Name) {
				return false
			}
		case source.Cinder != nil:
			if source.Cinder.SecretRef != nil && !visitor(source.Cinder.SecretRef.Name) {
				return false
			}
		case source.FlexVolume != nil:
			if source.FlexVolume.SecretRef != nil && !visitor(source.FlexVolume.SecretRef.Name) {
				return false
			}
		case source.Projected != nil:
			for j := range source.Projected.Sources {
				if source.Projected.Sources[j].Secret != nil {
					if !visitor(source.Projected.Sources[j].Secret.Name) {
						return false
					}
				}
			}
		case source.RBD != nil:
			if source.RBD.SecretRef != nil && !visitor(source.RBD.SecretRef.Name) {
				return false
			}
		case source.Secret != nil:
			if !visitor(source.Secret.SecretName) {
				return false
			}
		case source.ScaleIO != nil:
			if source.ScaleIO.SecretRef != nil && !visitor(source.ScaleIO.SecretRef.Name) {
				return false
			}
		case source.ISCSI != nil:
			if source.ISCSI.SecretRef != nil && !visitor(source.ISCSI.SecretRef.Name) {
				return false
			}
		case source.StorageOS != nil:
			if source.StorageOS.SecretRef != nil && !visitor(source.StorageOS.SecretRef.Name) {
				return false
			}
		case source.CSI != nil:
			if source.CSI.NodePublishSecretRef != nil && !visitor(source.CSI.NodePublishSecretRef.Name) {
				return false
			}
		}
	}
	return true
}

func visitContainerSecretNames(container *v1.Container, visitor Visitor) bool {
	for _, env := range container.EnvFrom {
		if env.SecretRef != nil {
			if !visitor(env.SecretRef.Name) {
				return false
			}
		}
	}
	for _, envVar := range container.Env {
		if envVar.ValueFrom != nil && envVar.ValueFrom.SecretKeyRef != nil {
			if !visitor(envVar.ValueFrom.SecretKeyRef.Name) {
				return false
			}
		}
	}
	return true
}

// VisitPodConfigmapNames invokes the visitor function with the name of every configmap
// referenced by the pod spec. If visitor returns false, visiting is short-circuited.
// Transitive references (e.g. pod -> pvc -> pv -> secret) are not visited.
// Returns true if visiting completed, false if visiting was short-circuited.
func VisitPodConfigmapNames(pod *v1.Pod, visitor Visitor) bool {
	VisitContainers(&pod.Spec, AllContainers, func(c *v1.Container, containerType ContainerType) bool {
		return visitContainerConfigmapNames(c, visitor)
	})
	var source *v1.VolumeSource
	for i := range pod.Spec.Volumes {
		source = &pod.Spec.Volumes[i].VolumeSource
		switch {
		case source.Projected != nil:
			for j := range source.Projected.Sources {
				if source.Projected.Sources[j].ConfigMap != nil {
					if !visitor(source.Projected.Sources[j].ConfigMap.Name) {
						return false
					}
				}
			}
		case source.ConfigMap != nil:
			if !visitor(source.ConfigMap.Name) {
				return false
			}
		}
	}
	return true
}

func visitContainerConfigmapNames(container *v1.Container, visitor Visitor) bool {
	for _, env := range container.EnvFrom {
		if env.ConfigMapRef != nil {
			if !visitor(env.ConfigMapRef.Name) {
				return false
			}
		}
	}
	for _, envVar := range container.Env {
		if envVar.ValueFrom != nil && envVar.ValueFrom.ConfigMapKeyRef != nil {
			if !visitor(envVar.ValueFrom.ConfigMapKeyRef.Name) {
				return false
			}
		}
	}
	return true
}

// GetContainerStatus extracts the status of container "name" from "statuses".
// It also returns if "name" exists.
func GetContainerStatus(statuses []v1.ContainerStatus, name string) (v1.ContainerStatus, bool) {
	for i := range statuses {
		if statuses[i].Name == name {
			return statuses[i], true
		}
	}
	return v1.ContainerStatus{}, false
}

// GetExistingContainerStatus extracts the status of container "name" from "statuses",
// It also returns if "name" exists.
func GetExistingContainerStatus(statuses []v1.ContainerStatus, name string) v1.ContainerStatus {
	status, _ := GetContainerStatus(statuses, name)
	return status
}

// IsPodAvailable returns true if a pod is available; false otherwise.
// Precondition for an available pod is that it must be ready. On top
// of that, there are two cases when a pod can be considered available:
// 1. minReadySeconds == 0, or
// 2. LastTransitionTime (is set) + minReadySeconds < current time
func IsPodAvailable(pod *v1.Pod, minReadySeconds int32, now metav1.Time) bool {
	if !IsPodReady(pod) {
		return false
	}

	c := GetPodReadyCondition(pod.Status)
	minReadySecondsDuration := time.Duration(minReadySeconds) * time.Second
	if minReadySeconds == 0 || !c.LastTransitionTime.IsZero() && c.LastTransitionTime.Add(minReadySecondsDuration).Before(now.Time) {
		return true
	}
	return false
}

// IsPodReady returns true if a pod is ready; false otherwise.
func IsPodReady(pod *v1.Pod) bool {
	return IsPodReadyConditionTrue(pod.Status)
}

// IsPodReadyConditionTrue returns true if a pod is ready; false otherwise.
func IsPodReadyConditionTrue(status v1.PodStatus) bool {
	condition := GetPodReadyCondition(status)
	return condition != nil && condition.Status == v1.ConditionTrue
}

// GetPodReadyCondition extracts the pod ready condition from the given status and returns that.
// Returns nil if the condition is not present.
func GetPodReadyCondition(status v1.PodStatus) *v1.PodCondition {
	_, condition := GetPodCondition(&status, v1.PodReady)
	return condition
}

// GetPodCondition extracts the provided condition from the given status and returns that.
// Returns nil and -1 if the condition is not present, and the index of the located condition.
func GetPodCondition(status *v1.PodStatus, conditionType v1.PodConditionType) (int, *v1.PodCondition) {
	if status == nil {
		return -1, nil
	}
	return GetPodConditionFromList(status.Conditions, conditionType)
}

// GetPodConditionFromList extracts the provided condition from the given list of condition and
// returns the index of the condition and the condition. Returns -1 and nil if the condition is not present.
func GetPodConditionFromList(conditions []v1.PodCondition, conditionType v1.PodConditionType) (int, *v1.PodCondition) {
	if conditions == nil {
		return -1, nil
	}
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return i, &conditions[i]
		}
	}
	return -1, nil
}

// UpdatePodCondition updates existing pod condition or creates a new one. Sets LastTransitionTime to now if the
// status has changed.
// Returns true if pod condition has changed or has been added.
func UpdatePodCondition(status *v1.PodStatus, condition *v1.PodCondition) bool {
	condition.LastTransitionTime = metav1.Now()
	// Try to find this pod condition.
	conditionIndex, oldCondition := GetPodCondition(status, condition.Type)

	if oldCondition == nil {
		// We are adding new pod condition.
		status.Conditions = append(status.Conditions, *condition)
		return true
	}
	// We are updating an existing condition, so we need to check if it has changed.
	if condition.Status == oldCondition.Status {
		condition.LastTransitionTime = oldCondition.LastTransitionTime
	}

	isEqual := condition.Status == oldCondition.Status &&
		condition.Reason == oldCondition.Reason &&
		condition.Message == oldCondition.Message &&
		condition.LastProbeTime.Equal(&oldCondition.LastProbeTime) &&
		condition.LastTransitionTime.Equal(&oldCondition.LastTransitionTime)

	status.Conditions[conditionIndex] = *condition
	// Return true if one of the fields have changed.
	return !isEqual
}

// GetPodPriority returns priority of the given pod.
func GetPodPriority(pod *v1.Pod) int32 {
	if pod.Spec.Priority != nil {
		return *pod.Spec.Priority
	}
	// When priority of a running pod is nil, it means it was created at a time
	// that there was no global default priority class and the priority class
	// name of the pod was empty. So, we resolve to the static default priority.
	return 0
}

// GetPodResourceType return the resource type of the given pod
// only Guaranteed and BestEffort are allowed.
func GetPodResourceType(pod *v1.Pod) (PodResourceType, error) {
	if qosLevel, ok := pod.Annotations[util.QoSLevelKey]; ok {
		switch util.QoSLevel(qosLevel) {
		case util.DedicatedCores, util.SharedCores:
			return GuaranteedPod, nil
		case util.ReclaimedCores:
			return BestEffortPod, nil
		default:
			// TODO: revisit this.
			// Fall back to PodResourceTypeAnnotationKey?
		}
	}

	if resourceType, ok := pod.Annotations[PodResourceTypeAnnotationKey]; ok {
		switch rt := PodResourceType(resourceType); rt {
		case GuaranteedPod, BestEffortPod:
			return rt, nil
		default:
			return "", PodResourceTypeUnsupportError
		}
	}
	klog.V(6).InfoS("Resource type was not set for pod", "pod", klog.KObj(pod))
	return GuaranteedPod, nil
}

func GetResourceTypeFromQoS(qosLevel string) PodResourceType {
	switch util.QoSLevel(qosLevel) {
	case util.DedicatedCores, util.SharedCores:
		return GuaranteedPod
	case util.ReclaimedCores:
		return BestEffortPod
	default:
	}
	return GuaranteedPod
}

func GetQoSLevelForPod(pod *v1.Pod) util.QoSLevel {
	if qos, ok := pod.Annotations[util.QoSLevelKey]; ok {
		return getQoSLevelForPod(pod, qos)
	}
	return util.SharedCores
}

func getQoSLevelForPod(pod *v1.Pod, qos string) util.QoSLevel {
	switch qos {
	case string(util.DedicatedCores), string(util.SharedCores), string(util.ReclaimedCores):
		return util.QoSLevel(qos)
	default:
		if podResourceType, _ := GetPodResourceType(pod); podResourceType == BestEffortPod {
			return util.ReclaimedCores
		}
		if podRequests := GetPodRequest(pod, util.ResourceNuma, resource.DecimalSI); podRequests != nil && podRequests.Value() > 0 {
			return util.DedicatedCores
		}
		return util.SharedCores
	}
}

// GetPodLauncher return the launcher of the given pod, only kubelet and node-manager are allowed.
func GetPodLauncher(pod *v1.Pod) (PodLauncher, error) {
	if podLauncher, ok := pod.Annotations[PodLauncherAnnotationKey]; ok {
		switch pt := PodLauncher(podLauncher); pt {
		case Kubelet, NodeManager:
			return pt, nil
		default:
			return "", PodLauncherUnsupportError
		}
	}
	klog.V(6).InfoS("Launcher was not set for pod", "pod", klog.KObj(pod))
	return Kubelet, nil
}

// IsLongRunningTask checks if this pod is long-running task
func IsLongRunningTask(pod *v1.Pod) bool {
	if pod.Annotations == nil {
		return true
	}
	if pod.Annotations[PodResourceTypeAnnotationKey] == string(BestEffortPod) {
		return false
	}
	if pod.Annotations[PodLauncherAnnotationKey] == string(NodeManager) {
		return false
	}
	return true
}

// GetDefaultPriorityForGodelPod return the default priority. used by unit creation
func GetDefaultPriorityForGodelPod(pod *v1.Pod) int32 {
	if pod.Spec.Priority != nil {
		return *pod.Spec.Priority
	}

	resourceType, err := GetPodResourceType(pod)
	if err != nil {
		return DefaultPriorityValueForGuaranteedPod
	}

	switch resourceType {
	case GuaranteedPod:
		return DefaultPriorityValueForGuaranteedPod
	case BestEffortPod:
		return DefaultPriorityValueForBestEffortPod
	default:
		return DefaultPriorityValueForGuaranteedPod
	}
}

// GetFailedSchedulersNames return failed schedulers of the given pod, from annotation failedSchedulers, in the format of sets.String
func GetFailedSchedulersNames(pod *v1.Pod) sets.String {
	if failedSchedulers, ok := pod.Annotations[FailedSchedulersAnnotationKey]; ok && failedSchedulers != "" {
		return sets.NewString(strings.Split(failedSchedulers, ",")...)
	}
	return sets.NewString()
}

// IsPodEligibleForPreemption returns false if a pod never preempts; true otherwise.
func IsPodEligibleForPreemption(pod *v1.Pod) bool {
	if pod.Spec.PreemptionPolicy != nil && *pod.Spec.PreemptionPolicy == v1.PreemptNever {
		return false
	}
	return true
}

func GetBestEffortPodAppName(pod *v1.Pod) string {
	if controllerRef := metav1.GetControllerOf(pod); controllerRef != nil {
		if controllerRef.Kind == applicationKind {
			return controllerRef.Name
		}
	}
	return ""
}

func GetPodRequest(pod *v1.Pod, resourceType v1.ResourceName, format resource.Format) *resource.Quantity {
	result := resource.NewQuantity(0, format)
	for _, container := range pod.Spec.Containers {
		for key, value := range container.Resources.Requests {
			if key == resourceType {
				result.Add(value)
			}
		}
	}

	for _, container := range pod.Spec.InitContainers {
		for key, value := range container.Resources.Requests {
			if key == resourceType {
				if result.Cmp(value) < 0 {
					result.SetMilli(value.MilliValue())
				}
			}
		}
	}

	return result
}

func GetPodRequests(pod *v1.Pod) map[string]*resource.Quantity {
	reqs := v1.ResourceList{}
	for _, container := range pod.Spec.Containers {
		addResourceList(reqs, container.Resources.Requests)
	}
	// init containers define the minimum of any resource
	for _, container := range pod.Spec.InitContainers {
		maxResourceList(reqs, container.Resources.Requests)
	}

	result := make(map[string]*resource.Quantity)
	for key, quantity := range reqs {
		copy := quantity.DeepCopy()
		result[key.String()] = &copy
	}
	return result
}

// addResourceList adds the resources in newList to list
func addResourceList(list, newList v1.ResourceList) {
	for name, quantity := range newList {
		if value, ok := list[name]; !ok {
			list[name] = quantity.DeepCopy()
		} else {
			value.Add(quantity)
			list[name] = value
		}
	}
}

// maxResourceList sets list to the greater of list/newList for every resource
// either list
func maxResourceList(list, new v1.ResourceList) {
	for name, quantity := range new {
		if value, ok := list[name]; !ok {
			list[name] = quantity.DeepCopy()
			continue
		} else {
			if quantity.Cmp(value) > 0 {
				list[name] = quantity.DeepCopy()
			}
		}
	}
}

func GetPodKey(pod *v1.Pod) string {
	return pod.Namespace + "/" + pod.Name
}

func LegalPodResourceTypeAndLauncher(pod *v1.Pod) bool {
	if _, err := GetPodLauncher(pod); err != nil {
		return false
	}
	if _, err := GetPodResourceType(pod); err != nil {
		return false
	}

	return true
}

func responsibleForPod(pod *v1.Pod, schedulerName string) bool {
	return pod.Spec.SchedulerName == schedulerName ||
		pod.Spec.SchedulerName == v1.DefaultSchedulerName ||
		pod.Spec.SchedulerName == ""
}

const RSKind = "ReplicaSet"

func GetRSFromPod(pod *v1.Pod) (string, error) {
	ownerRefs := pod.GetOwnerReferences()
	for _, ref := range ownerRefs {
		if ref.Kind == RSKind {
			// use ref.UID directly ? not that human-readable
			return pod.Namespace + "/" + ref.Name, nil
		}
	}
	return "", fmt.Errorf("can not find RS info from Pod: %s/%s OwnerReferences", pod.Namespace, pod.Name)
}

func GetPodOwnerInfoKey(pod *v1.Pod) string {
	podOwnerInfo := GetPodOwnerInfo(pod)
	if podOwnerInfo == nil {
		return ""
	}
	return GetOwnerInfoKey(podOwnerInfo)
}

func GetPodTemplateKey(pod *v1.Pod) string {
	if podOwnerInfo := GetPodOwnerInfo(pod); podOwnerInfo != nil {
		return GetOwnerInfoKey(podOwnerInfo)
	}
	// By default, use Pod key directly.
	return GeneratePodKey(pod)
}

// TODO: Convergence related function calls.
// GetPodOwner will consider PodGroup as well.
func GetPodOwner(pod *v1.Pod) string {
	podOwnerInfo := GetPodOwnerInfo(pod)
	if podOwnerInfo != nil {
		return GetOwnerInfoKey(podOwnerInfo)
	}
	podOwnerInfo = GetPodGroupOwnerInfo(pod)
	if podOwnerInfo != nil {
		return GetOwnerInfoKey(podOwnerInfo)
	}
	return ""
}

func GetPodGroupOwnerInfo(pod *v1.Pod) *schedulingv1a1.OwnerInfo {
	// get pod group
	// TODO: figure out how to distinguish different roles in one pod group
	podGroupName, ok := pod.Annotations[PodGroupNameAnnotationKey]
	if ok && len(podGroupName) > 0 {
		return &schedulingv1a1.OwnerInfo{
			Type:      PodGroupKind,
			Name:      podGroupName,
			Namespace: pod.Namespace,
		}
	}
	return nil
}

const PodKeySeperator string = "/"

func GeneratePodKey(pod *v1.Pod) string {
	return pod.GetNamespace() + PodKeySeperator + pod.GetName() + PodKeySeperator + string(pod.GetUID())
}

func ParsePodKey(str string) (string, string, types.UID, error) {
	strArr := strings.Split(str, PodKeySeperator)
	if len(strArr) != 3 {
		return "", "", types.UID(""), fmt.Errorf("failed to parse pod key '%s'", str)
	}
	return strArr[0], strArr[1], types.UID(strArr[2]), nil
}

func GetPodFullKey(namespace, name, uid string) string {
	return namespace + PodKeySeperator + name + PodKeySeperator + uid
}

func GetUIDFromPodFullKey(key string) string {
	keySlice := strings.Split(key, PodKeySeperator)
	if len(keySlice) != 3 {
		return ""
	}
	return keySlice[2]
}

func GetProtectionDuration(objectKey string, annotations map[string]string) (int64, bool) {
	var minIntervalStr string
	if minInterval, ok := annotations[ProtectionDurationFromPreemptionKey]; ok {
		minIntervalStr = minInterval
	} else {
		return 0, false
	}

	if minIntervalSeconds, err := strconv.ParseInt(minIntervalStr, 10, 64); err != nil {
		klog.InfoS("Failed to parse the annotation value for the object", "annotation", minIntervalStr, "objectKey", objectKey, "err", err)
		return 0, false
	} else {
		return minIntervalSeconds, true
	}
}

func ConvertToPod(obj interface{}) (*v1.Pod, error) {
	var pod *v1.Pod
	switch t := obj.(type) {
	case *v1.Pod:
		pod = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		pod, ok = t.Obj.(*v1.Pod)
		if !ok {
			return nil, fmt.Errorf("unable to convert object %T to *v1.Pod", obj)
		}
	default:
		return nil, fmt.Errorf("unable to handle object: %T", obj)
	}
	return pod, nil
}

// GetPodGroupName return pod group name
func GetPodGroupName(pod *v1.Pod) string {
	pgName := pod.GetAnnotations()[PodGroupNameAnnotationKey]
	if len(pgName) != 0 {
		return pgName
	}

	return ""
}

// CanPodBePreempted indicates whether the pod can be preempted
// -1: not pass
// 0: not sure
// 1: pass
func CanPodBePreempted(pod *v1.Pod) int {
	// TODO: remove this policy: pod without priorityclass could not be preempted
	if len(pod.Spec.PriorityClassName) == 0 {
		return -1
	}

	podAnnotations := pod.Annotations
	canBePreemptedVal := podAnnotations[util.CanBePreemptedAnnotationKey]
	if canBePreemptedVal == util.CanBePreempted {
		return 1
	}
	if len(canBePreemptedVal) == 0 {
		return 0
	}
	return -1
}

func GetOwnerInfo(pod *v1.Pod) (string, string) {
	for _, or := range pod.OwnerReferences {
		switch or.Kind {
		case util.OwnerTypeDaemonSet:
			return or.Kind, pod.Namespace + "/" + or.Name
		case util.OwnerTypeReplicaSet:
			return or.Kind, pod.Namespace + "/" + or.Name
		}
	}
	return "", ""
}

func FilteringUpdate(
	filterFn func(*v1.Pod) bool,
	addFunc func(*v1.Pod) error,
	updateFunc func(*v1.Pod, *v1.Pod) error,
	deleteFunc func(*v1.Pod) error,
	oldPod, newPod *v1.Pod,
) {
	older := filterFn(oldPod)
	newer := filterFn(newPod)
	switch {
	case newer && older:
		updateFunc(oldPod, newPod)
	case newer && !older:
		addFunc(newPod)
	case !newer && older:
		deleteFunc(oldPod)
	default:
		// do nothing
	}
}

const PvcSelectedNodeAnnotation = "volume.kubernetes.io/selected-node"

func IsPvcVolumeLocalPV(pvcLister corelisters.PersistentVolumeClaimLister, pvc string, pod *v1.Pod) (bool, string) {
	if pod == nil {
		return false, ""
	}

	claim, err := pvcLister.PersistentVolumeClaims(pod.GetNamespace()).Get(pvc)
	if err != nil {
		klog.InfoS("Failed to get pvc", "pvcName", pvc, "err", err)
		return false, ""
	}

	if anno, ok := claim.Annotations[PvcSelectedNodeAnnotation]; !ok {
		klog.InfoS("WARN: No selected-node annotation found on pvc", "pvcName", pvc)
		return false, ""
	} else if pod.Spec.NodeName != "" && anno != pod.Spec.NodeName {
		// how to deal with this case?
		klog.InfoS("WARN: PVC SelectedNode did not match with the current node")
		return false, ""
	} else {
		return true, anno
	}
}

func GetSchedulerNameForPod(pod *v1.Pod) string {
	return pod.GetAnnotations()[SchedulerAnnotationKey]
}

const (
	ReplicaSetKind       = "ReplicaSet"
	StatefulSetKind      = "StatefulSet"
	StatefulSetExtension = "StatefulSetExtension"
	RequestClassKind     = "RequestClass"
	PodGroupKind         = "PodGroup"
	DaemonSetKind        = "DaemonSet"
	RequestTemplateKind  = "RequestTemplate"

	KeySeperator string = "/"
)

func GetPodOwnerInfo(pod *v1.Pod) *schedulingv1a1.OwnerInfo {
	// try to get replicaset/statefulset from pod owner reference
	ownerRef, err := getOwnerReferenceFromPod(pod)
	if err == nil {
		return &schedulingv1a1.OwnerInfo{
			Type:      ownerRef.Kind,
			Namespace: pod.Namespace,
			Name:      ownerRef.Name,
			UID:       ownerRef.UID,
		}
	}
	// get pod request template
	requestTemplate, ok := pod.Annotations[PodRequestTemplateAnnotationKey]
	if ok && len(requestTemplate) > 0 {
		return &schedulingv1a1.OwnerInfo{
			Type:      RequestTemplateKind,
			Name:      requestTemplate,
			Namespace: pod.Namespace,
		}
	}
	return nil
}

func GetOwnerInfoKey(ownerInfo *schedulingv1a1.OwnerInfo) string {
	return ownerInfo.Type + KeySeperator + ownerInfo.Namespace + KeySeperator + ownerInfo.Name + KeySeperator + string(ownerInfo.UID)
}

func getOwnerReferenceFromPod(pod *v1.Pod) (metav1.OwnerReference, error) {
	ownerRefs := pod.GetOwnerReferences()
	for _, ref := range ownerRefs {
		if ref.Kind == ReplicaSetKind || ref.Kind == StatefulSetKind || ref.Kind == StatefulSetExtension {
			// use ref.UID directly ? not that human-readable
			return ref, nil
		}
	}
	return metav1.OwnerReference{}, fmt.Errorf("can not find Owner info from Pod: %s/%s OwnerReferences", pod.Namespace, pod.Name)
}

// TODO: revisit this.
func PodHasDaemonSetOwnerReference(pod *v1.Pod) bool {
	ownerRefs := pod.GetOwnerReferences()
	for _, ref := range ownerRefs {
		if ref.Kind == DaemonSetKind {
			return true
		}
	}
	return false
}

func GetMovementNameFromPod(pod *v1.Pod) string {
	return pod.Annotations[MovementNameKey]
}

func IsSharedCores(pod *v1.Pod) bool {
	numaBinding, _ := util.NeedConsiderTopology(pod)
	return !numaBinding
}

func GetPodAlignedResources(pod *v1.Pod) ([]string, bool) {
	resources, ok := pod.Annotations[AlignedResources]
	if !ok {
		return nil, false
	}
	return strings.Split(resources, ","), true
}

func CleanupPodAnnotations(client clientset.Interface, pod *v1.Pod) error {
	podCopy := pod.DeepCopy()
	if podCopy.Annotations == nil {
		podCopy.Annotations = map[string]string{}
	}

	delete(podCopy.Annotations, AssumedNodeAnnotationKey)
	delete(podCopy.Annotations, AssumedCrossNodeAnnotationKey)
	delete(podCopy.Annotations, NominatedNodeAnnotationKey)
	delete(podCopy.Annotations, FailedSchedulersAnnotationKey)
	delete(podCopy.Annotations, MicroTopologyKey)

	// reset pod state to dispatched
	podCopy.Annotations[PodStateAnnotationKey] = string(PodDispatched)

	return util.PatchPod(client, pod, podCopy)
}
