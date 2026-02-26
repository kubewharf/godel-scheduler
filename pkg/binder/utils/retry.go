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

package utils

import (
	"strconv"

	v1 "k8s.io/api/core/v1"
)

const (
	// BindFailureCountAnnotationKey records the cumulative number of bind failures
	// a Pod has experienced within the same Scheduler+Binder instance.
	BindFailureCountAnnotationKey = "godel.bytedance.com/bind-failure-count"

	// LastBindFailureReasonKey records the most recent bind failure reason.
	// This information is propagated through the Scheduler → Binder → Dispatcher
	// chain to help Dispatcher make better scheduling decisions.
	LastBindFailureReasonKey = "godel.bytedance.com/last-bind-failure"
)

// GetBindFailureCount returns the cumulative bind failure count stored in the
// Pod's annotations. Returns 0 if the annotation is absent or cannot be parsed.
func GetBindFailureCount(pod *v1.Pod) int {
	if pod == nil || pod.Annotations == nil {
		return 0
	}
	val, ok := pod.Annotations[BindFailureCountAnnotationKey]
	if !ok {
		return 0
	}
	count, err := strconv.Atoi(val)
	if err != nil {
		return 0
	}
	return count
}

// SetBindFailureCount sets the bind failure count annotation on the Pod.
// It initialises the Annotations map if nil. The returned Pod is the same
// pointer that was passed in (mutated in place).
func SetBindFailureCount(pod *v1.Pod, count int) *v1.Pod {
	if pod == nil {
		return pod
	}
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[BindFailureCountAnnotationKey] = strconv.Itoa(count)
	return pod
}

// IncrementBindFailureCount reads the current failure count, adds one, and
// writes it back. The returned Pod is the same pointer (mutated in place).
func IncrementBindFailureCount(pod *v1.Pod) *v1.Pod {
	count := GetBindFailureCount(pod)
	return SetBindFailureCount(pod, count+1)
}

// ShouldDispatchToAnotherScheduler returns true when the Pod's bind failure
// count has reached or exceeded maxRetries, indicating that the Pod should be
// sent back to the Dispatcher for re-assignment to a different Scheduler.
// When maxRetries is 0, the re-dispatch mechanism is disabled (always returns false).
func ShouldDispatchToAnotherScheduler(pod *v1.Pod, maxRetries int) bool {
	if maxRetries <= 0 {
		return false
	}
	return GetBindFailureCount(pod) >= maxRetries
}

// SetLastBindFailureReason writes a human-readable failure reason into the
// Pod's annotations. The returned Pod is the same pointer (mutated in place).
func SetLastBindFailureReason(pod *v1.Pod, reason string) *v1.Pod {
	if pod == nil {
		return pod
	}
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[LastBindFailureReasonKey] = reason
	return pod
}

// GetLastBindFailureReason returns the last bind failure reason stored in the
// Pod's annotations, or an empty string if not set.
func GetLastBindFailureReason(pod *v1.Pod) string {
	if pod == nil || pod.Annotations == nil {
		return ""
	}
	return pod.Annotations[LastBindFailureReasonKey]
}
