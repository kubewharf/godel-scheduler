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

package validation

import (
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/kubewharf/godel-scheduler/pkg/binder/apis/config"
	godelvalidation "github.com/kubewharf/godel-scheduler/pkg/util/validation"
)

func ValidateGodelBinderConfiguration(cc *config.GodelBinderConfiguration) field.ErrorList {
	errs := field.ErrorList{}
	errs = append(errs, godelvalidation.ValidateClientConnectionConfiguration(&cc.ClientConnection, field.NewPath("clientConnection"))...)
	errs = append(errs, godelvalidation.ValidateLeaderElectionConfiguration(&cc.LeaderElection, field.NewPath("leaderElection"))...)

	if cc.SchedulerName == nil {
		errs = append(errs, field.Invalid(field.NewPath("SchedulerName"),
			cc.SchedulerName, "can not be nil"))
	}

	for _, msg := range validation.IsValidSocketAddr(cc.HealthzBindAddress) {
		errs = append(errs, field.Invalid(field.NewPath("healthzBindAddress"), cc.HealthzBindAddress, msg))
	}
	for _, msg := range validation.IsValidSocketAddr(cc.MetricsBindAddress) {
		errs = append(errs, field.Invalid(field.NewPath("metricsBindAddress"), cc.MetricsBindAddress, msg))
	}

	if cc.VolumeBindingTimeoutSeconds <= 0 {
		errs = append(errs, field.Invalid(field.NewPath("volumeBindingTimeoutSeconds"),
			cc.VolumeBindingTimeoutSeconds, "must be greater than 0"))
	}

	return errs
}
