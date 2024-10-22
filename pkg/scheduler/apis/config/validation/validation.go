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
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	godelvalidation "github.com/kubewharf/godel-scheduler/pkg/util/validation"
)

func ValidateGodelSchedulerConfiguration(cc *config.GodelSchedulerConfiguration) field.ErrorList {
	errs := field.ErrorList{}
	// 1. LeaderElection & SchedulerRenewIntervalSeconds
	{
		errs = append(errs, godelvalidation.ValidateLeaderElectionConfiguration(&cc.LeaderElection, field.NewPath("leaderElection"))...)
		if cc.SchedulerRenewIntervalSeconds <= 0 {
			errs = append(errs, field.Invalid(field.NewPath("schedulerRenewInterval"),
				cc.SchedulerRenewIntervalSeconds, "must be greater than 0"))
		}
	}

	// 2. ClientConnection and BindSetting
	{
		errs = append(errs, godelvalidation.ValidateClientConnectionConfiguration(&cc.ClientConnection, field.NewPath("clientConnection"))...)
		for _, msg := range validation.IsValidSocketAddr(cc.HealthzBindAddress) {
			errs = append(errs, field.Invalid(field.NewPath("healthzBindAddress"), cc.HealthzBindAddress, msg))
		}
		for _, msg := range validation.IsValidSocketAddr(cc.MetricsBindAddress) {
			errs = append(errs, field.Invalid(field.NewPath("metricsBindAddress"), cc.MetricsBindAddress, msg))
		}
	}

	// 3. DebuggingConfiguration
	// Do nothing.

	// 4. Godel Scheduler
	{
		if len(cc.GodelSchedulerName) == 0 {
			errs = append(errs, field.Required(field.NewPath("GodelSchedulerName"), ""))
		}
		if cc.SchedulerName == nil {
			errs = append(errs, field.Required(field.NewPath("schedulerName"), ""))
		}
		if cc.ReservationTimeOutSeconds <= 0 {
			errs = append(errs, field.Invalid(field.NewPath("ReservationTimeOutSeconds"), cc.ReservationTimeOutSeconds, "ReservationTimeOutSeconds == 0"))
		}
		// TODO: Restore the following logic.
		// if cc.SubClusterKey == nil || len(*cc.SubClusterKey) == 0 {
		// 	errs = append(errs, field.Required(field.NewPath("subClusterKey"), ""))
		// }
	}

	// 5. Godel Profiles
	{
		if cc.DefaultProfile == nil {
			errs = append(errs, field.Required(field.NewPath("defaultProfile"), ""))
		} else {
			errs = append(errs, ValidateSubClusterArgs(cc.DefaultProfile, field.NewPath("defaultProfile"))...)
		}
		if cc.SubClusterProfiles != nil {
			for _, profile := range cc.SubClusterProfiles {
				if len(profile.SubClusterName) == 0 {
					errs = append(errs, field.Required(field.NewPath("subClusterName"), ""))
				}
				errs = append(errs, ValidateSubClusterArgs(cc.DefaultProfile, field.NewPath("subClusterProfile"))...)
			}
		}
	}

	return errs
}

func noDuplicatePlugins(plugins *config.PluginSet, fldPath *field.Path, childPath string) field.ErrorList {
	errs := field.ErrorList{}
	if plugins == nil {
		return errs
	}
	pluginSet := sets.NewString()
	for _, plugin := range plugins.Plugins {
		if pluginSet.Has(plugin.Name) {
			errs = append(errs, field.Invalid(fldPath.Child(childPath), plugins, "plugin "+plugin.Name+" is duplicated"))
		} else {
			pluginSet.Insert(plugin.Name)
		}
	}
	return errs
}

// ValidateBasePluginsConfiguration ensures validation of the base plugins struct
func ValidateBasePluginsConfiguration(plugins *config.Plugins, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}
	if plugins == nil {
		return errs
	}
	errs = append(errs, noDuplicatePlugins(plugins.Filter, fldPath, "filter")...)
	errs = append(errs, noDuplicatePlugins(plugins.Score, fldPath, "score")...)
	return errs
}

// ValidatePluginArgsConfiguration ensures validation of the ClientConnectionConfiguration struct
func ValidatePluginArgsConfiguration(pluginArgs []config.PluginConfig, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}
	if len(pluginArgs) == 0 {
		return errs
	}
	argsSet := sets.NewString()
	for _, pluginArg := range pluginArgs {
		if argsSet.Has(pluginArg.Name) {
			errs = append(errs, field.Invalid(fldPath, pluginArg, "plugin "+pluginArg.Name+" is duplicated"))
		} else {
			argsSet.Insert(pluginArg.Name)
			if pluginArg.Args.Size() == 0 {
				errs = append(errs, field.Invalid(fldPath, pluginArg, "plugin "+pluginArg.Name+" args is empty"))
			}
		}
	}
	return errs
}

func ValidateSubClusterArgs(cc *config.GodelSchedulerProfile, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, ValidateBasePluginsConfiguration(cc.BasePluginsForKubelet, field.NewPath("baseKubeletPlugins"))...)
	errs = append(errs, ValidateBasePluginsConfiguration(cc.BasePluginsForNM, field.NewPath("baseNMPlugins"))...)
	errs = append(errs, ValidatePluginArgsConfiguration(cc.PluginConfigs, field.NewPath("pluginConfig"))...)

	if cc.PercentageOfNodesToScore != nil && (*cc.PercentageOfNodesToScore < 0 || *cc.PercentageOfNodesToScore > 100) {
		errs = append(errs, field.Invalid(field.NewPath("percentageOfNodesToScore"),
			cc.PercentageOfNodesToScore, "not in valid range [0-100]"))
	}
	if cc.IncreasedPercentageOfNodesToScore != nil && (*cc.IncreasedPercentageOfNodesToScore < 0 || *cc.IncreasedPercentageOfNodesToScore > 100) {
		errs = append(errs, field.Invalid(field.NewPath("increasedPercentageOfNodesToScore"),
			cc.IncreasedPercentageOfNodesToScore, "not in valid range [0-100]"))
	}
	if cc.UnitInitialBackoffSeconds != nil && *cc.UnitInitialBackoffSeconds <= 0 {
		errs = append(errs, field.Invalid(field.NewPath("unitInitialBackoffSeconds"),
			cc.UnitInitialBackoffSeconds, "must be greater than 0"))
	}
	if cc.UnitInitialBackoffSeconds != nil && *cc.UnitMaxBackoffSeconds < *cc.UnitInitialBackoffSeconds {
		errs = append(errs, field.Invalid(field.NewPath("unitMaxBackoffSeconds"),
			cc.UnitMaxBackoffSeconds, "must be greater than or equal to UnitInitialBackoffSeconds"))
	}
	if cc.AttemptImpactFactorOnPriority != nil && *cc.AttemptImpactFactorOnPriority <= 0 {
		errs = append(errs, field.Invalid(field.NewPath("attemptImpactFactorOnPriority"),
			cc.AttemptImpactFactorOnPriority, "must be greater than 0"))
	}
	return errs
}
