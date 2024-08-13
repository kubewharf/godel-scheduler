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

package v1beta1

import (
	"net"
	"strconv"

	"k8s.io/apimachinery/pkg/runtime"
	utilpointer "k8s.io/utils/pointer"

	defaultsconfig "github.com/kubewharf/godel-scheduler/pkg/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

func addDefaultingFuncs(scheme *runtime.Scheme) error {
	return RegisterDefaults(scheme)
}

// SetDefaults_GodelSchedulerConfiguration sets additional defaults
func SetDefaults_GodelSchedulerConfiguration(obj *GodelSchedulerConfiguration) {
	// 1. LeaderElection & SchedulerRenewIntervalSeconds
	{
		// Use the default LeaderElectionConfiguration options
		defaultsconfig.SetDefaultLeaderElectionConfiguration(&obj.LeaderElection)
		if len(obj.LeaderElection.ResourceName) == 0 {
			obj.LeaderElection.ResourceName = config.DefaultSchedulerName
		}
		if obj.SchedulerRenewIntervalSeconds == 0 {
			obj.SchedulerRenewIntervalSeconds = config.DefaultRenewIntervalInSeconds
		}
	}

	// 2. ClientConnection and BindSetting
	{
		if len(obj.ClientConnection.ContentType) == 0 {
			obj.ClientConnection.ContentType = "application/vnd.kubernetes.protobuf"
		}
		// Scheduler has an opinion about QPS/Burst, setting specific defaults for itself, instead of generic settings.
		if obj.ClientConnection.QPS == 0.0 {
			obj.ClientConnection.QPS = config.DefaultClientConnectionQPS
		}
		if obj.ClientConnection.Burst == 0 {
			obj.ClientConnection.Burst = config.DefaultClientConnectionBurst
		}
		// For Healthz and Metrics bind addresses, we want to check:
		// 1. If the value is nil, default to 0.0.0.0 and default scheduler port
		// 2. If there is a value set, attempt to split it. If it's just a port (ie, ":1234"), default to 0.0.0.0 with that port
		// 3. If splitting the value fails, check if the value is even a valid IP. If so, use that with the default port.
		// Otherwise use the default bind address
		if len(obj.HealthzBindAddress) == 0 {
			obj.HealthzBindAddress = config.DefaultBindAddress
		} else {
			if host, port, err := net.SplitHostPort(obj.HealthzBindAddress); err == nil {
				if len(host) == 0 {
					host = config.DefaultGodelSchedulerAddress
				}
				hostPort := net.JoinHostPort(host, port)
				obj.HealthzBindAddress = hostPort
			} else {
				// Something went wrong splitting the host/port, could just be a missing port so check if the
				// existing value is a valid IP address. If so, use that with the default scheduler port
				if host := net.ParseIP(obj.HealthzBindAddress); host != nil {
					hostPort := net.JoinHostPort(obj.HealthzBindAddress, strconv.Itoa(config.DefaultInsecureSchedulerPort))
					obj.HealthzBindAddress = hostPort
				} else {
					// TODO: in godelschedulerconfig we should let this error instead of stomping with a default value
					obj.HealthzBindAddress = config.DefaultBindAddress
				}
			}
		}

		if len(obj.MetricsBindAddress) == 0 {
			obj.MetricsBindAddress = config.DefaultBindAddress
		} else {
			if host, port, err := net.SplitHostPort(obj.MetricsBindAddress); err == nil {
				if len(host) == 0 {
					host = config.DefaultGodelSchedulerAddress
				}
				hostPort := net.JoinHostPort(host, port)
				obj.MetricsBindAddress = hostPort
			} else {
				// Something went wrong splitting the host/port, could just be a missing port so check if the
				// existing value is a valid IP address. If so, use that with the default scheduler port
				if host := net.ParseIP(obj.MetricsBindAddress); host != nil {
					hostPort := net.JoinHostPort(obj.MetricsBindAddress, strconv.Itoa(config.DefaultInsecureSchedulerPort))
					obj.MetricsBindAddress = hostPort
				} else {
					// TODO: in godelschedulerconfig we should let this error instead of stomping with a default value
					obj.MetricsBindAddress = config.DefaultBindAddress
				}
			}
		}
	}
	// 3. DebuggingConfiguration
	{
		// Enable profiling by default in the scheduler
		if obj.EnableProfiling == nil {
			enableProfiling := true
			obj.EnableProfiling = &enableProfiling
		}

		// Enable contention profiling by default if profiling is enabled
		if *obj.EnableProfiling && obj.EnableContentionProfiling == nil {
			enableContentionProfiling := true
			obj.EnableContentionProfiling = &enableContentionProfiling
		}
	}

	// 4. Godel Scheduler
	{
		// Only apply a default scheduler name when there is a single profile.
		// Validation will ensure that every profile has a non-empty unique name.
		if len(obj.GodelSchedulerName) == 0 {
			obj.GodelSchedulerName = config.DefaultGodelSchedulerName
		}
		if obj.SchedulerName == nil {
			defaultValue := config.DefaultSchedulerName
			obj.SchedulerName = &defaultValue
		}
		if obj.SubClusterKey == nil {
			defaultValue := config.DefaultSubClusterKey
			obj.SubClusterKey = &defaultValue
		}
		if obj.Tracer == nil {
			obj.Tracer = tracing.DefaultNoopOptions()
		}
	}
	// 5. Godel Profiles
	{
		if obj.DefaultProfile == nil {
			// We got SubClusterName "" as default.
			obj.DefaultProfile = &GodelSchedulerProfile{}
		}
		if obj.DefaultProfile.PercentageOfNodesToScore == nil {
			percentageOfNodesToScore := int32(config.DefaultPercentageOfNodesToScore)
			obj.DefaultProfile.PercentageOfNodesToScore = &percentageOfNodesToScore
		}
		if obj.DefaultProfile.IncreasedPercentageOfNodesToScore == nil {
			increasedPercentageOfNodesToScore := int32(config.DefaultIncreasedPercentageOfNodesToScore)
			obj.DefaultProfile.IncreasedPercentageOfNodesToScore = &increasedPercentageOfNodesToScore
		}
		if obj.DefaultProfile.UnitInitialBackoffSeconds == nil {
			defaultUnitInitialBackoffInSeconds := int64(config.DefaultUnitInitialBackoffInSeconds)
			obj.DefaultProfile.UnitInitialBackoffSeconds = &defaultUnitInitialBackoffInSeconds
		}
		if obj.DefaultProfile.UnitMaxBackoffSeconds == nil {
			defaultUnitMaxBackoffInSeconds := int64(config.DefaultUnitMaxBackoffInSeconds)
			obj.DefaultProfile.UnitMaxBackoffSeconds = &defaultUnitMaxBackoffInSeconds
		}
		if obj.DefaultProfile.AttemptImpactFactorOnPriority == nil {
			attemptImpactFactorOnPriority := config.DefaultAttemptImpactFactorOnPriority
			obj.DefaultProfile.AttemptImpactFactorOnPriority = &attemptImpactFactorOnPriority
		}
		// Set disable preemption default to false if not set
		if obj.DefaultProfile.DisablePreemption == nil {
			obj.DefaultProfile.DisablePreemption = utilpointer.BoolPtr(config.DefaultDisablePreemption)
		}
		// Set disable preemption default to false if not set
		if obj.DefaultProfile.BlockQueue == nil {
			obj.DefaultProfile.BlockQueue = utilpointer.BoolPtr(config.DefaultBlockQueue)
		}
		if obj.DefaultProfile.MaxWaitingDeletionDuration == 0 {
			obj.DefaultProfile.MaxWaitingDeletionDuration = config.DefaultMaxWaitingDeletionDuration
		}
	}
}
