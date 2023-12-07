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

package validation

import (
	"k8s.io/apimachinery/pkg/util/validation/field"
	componentbaseconfig "k8s.io/component-base/config/v1alpha1"
)

// ValidateClientConnectionConfiguration ensures validation of the ClientConnectionConfiguration struct
func ValidateClientConnectionConfiguration(cc *componentbaseconfig.ClientConnectionConfiguration, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}
	if cc.Burst < 0 {
		errs = append(errs, field.Invalid(fldPath.Child("burst"), cc.Burst, "must be non-negative"))
	}
	return errs
}

// ValidateLeaderElectionConfiguration ensures validation of the LeaderElectionConfiguration struct
func ValidateLeaderElectionConfiguration(cc *componentbaseconfig.LeaderElectionConfiguration, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}
	if cc.LeaderElect == nil || !*cc.LeaderElect {
		return errs
	}
	if cc.LeaseDuration.Duration <= 0 {
		errs = append(errs, field.Invalid(fldPath.Child("leaseDuration"), cc.LeaseDuration, "must be greater than zero"))
	}
	if cc.RenewDeadline.Duration <= 0 {
		errs = append(errs, field.Invalid(fldPath.Child("renewDeadline"), cc.RenewDeadline, "must be greater than zero"))
	}
	if cc.RetryPeriod.Duration <= 0 {
		errs = append(errs, field.Invalid(fldPath.Child("retryPeriod"), cc.RetryPeriod, "must be greater than zero"))
	}
	if cc.LeaseDuration.Duration < cc.RenewDeadline.Duration {
		errs = append(errs, field.Invalid(fldPath.Child("leaseDuration"), cc.RenewDeadline, "LeaseDuration must be greater than RenewDeadline"))
	}
	if len(cc.ResourceLock) == 0 {
		errs = append(errs, field.Invalid(fldPath.Child("resourceLock"), cc.ResourceLock, "resourceLock is required"))
	}
	if len(cc.ResourceNamespace) == 0 {
		errs = append(errs, field.Invalid(fldPath.Child("resourceNamespace"), cc.ResourceNamespace, "resourceNamespace is required"))
	}
	if len(cc.ResourceName) == 0 {
		errs = append(errs, field.Invalid(fldPath.Child("resourceName"), cc.ResourceName, "resourceName is required"))
	}
	return errs
}
