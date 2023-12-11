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

package config

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	componentbaseconfig "k8s.io/component-base/config/v1alpha1"
	utilpointer "k8s.io/utils/pointer"
)

const (
	DefaultPreemptMinIntervalSeconds int64 = 0

	// NamespaceSystem is the system namespace where we place godel components.
	NamespaceSystem = "godel-system"

	DefaultLeaseLock = "leases"
)

var (
	DefaultLeaseDuration      = metav1.Duration{Duration: 45 * time.Second}
	DefaultLeaseRenewDeadline = metav1.Duration{Duration: 30 * time.Second}
	DefaultLeaseRetryPeriod   = metav1.Duration{Duration: 2 * time.Second}
)

func SetDefaultLeaderElectionConfiguration(obj *componentbaseconfig.LeaderElectionConfiguration) {
	if obj.LeaderElect == nil {
		obj.LeaderElect = utilpointer.BoolPtr(true)
	}
	if len(obj.ResourceLock) == 0 {
		// Use lease-based leader election to reduce cost.
		obj.ResourceLock = DefaultLeaseLock
	}
	if len(obj.ResourceNamespace) == 0 {
		obj.ResourceNamespace = NamespaceSystem
	}

	zero := metav1.Duration{}
	if obj.LeaseDuration == zero {
		obj.LeaseDuration = DefaultLeaseDuration
	}
	if obj.RenewDeadline == zero {
		obj.RenewDeadline = DefaultLeaseRenewDeadline
	}
	if obj.RetryPeriod == zero {
		obj.RetryPeriod = DefaultLeaseRetryPeriod
	}
}
