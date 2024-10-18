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
	"net"
	"strconv"

	"k8s.io/apimachinery/pkg/runtime"

	defaultsconfig "github.com/kubewharf/godel-scheduler/pkg/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

const (
	DefaultSchedulerName               = "godel-scheduler"
	DefaultClientConnectionContentType = "application/vnd.kubernetes.protobuf"
	DefaultClientConnectionQPS         = 10000.0
	DefaultClientConnectionBurst       = 10000
	DefaultInsecureBinderPort          = 10451
	// VolumeBindingTimeoutSeconds defines the default bind timeout
	VolumeBindingTimeoutSeconds = 100

	BinderDefaultLockObjectName      = "binder"
	DefaultReservationTimeOutSeconds = 60

	// DefaultGodelBinderAddress is the default address for the scheduler status server.
	// May be overridden by a flag at startup.
	DefaultGodelBinderAddress = "0.0.0.0"
	// DefaultIDC is default idc name for godel scheduler
	DefaultIDC = "lq"
	// DefaultCluster is default cluster name for godel scheduler
	DefaultCluster = "default"
	// DefaultTracer is default tracer name for godel scheduler
	DefaultTracer = string(tracing.NoopConfig)
)

func addDefaultingFuncs(scheme *runtime.Scheme) error {
	return RegisterDefaults(scheme)
}

func SetDefaults_GodelBinderConfiguration(cfg *GodelBinderConfiguration) {
	if len(cfg.ClientConnection.ContentType) == 0 {
		cfg.ClientConnection.ContentType = DefaultClientConnectionContentType
	}

	if cfg.SchedulerName == nil {
		defaultValue := DefaultSchedulerName
		cfg.SchedulerName = &defaultValue
	}

	if cfg.Tracer == nil {
		cfg.Tracer = tracing.DefaultNoopOptions()
	}

	// Scheduler has an opinion about QPS/Burst, setting specific defaults for itself, instead of generic settings.
	if cfg.ClientConnection.QPS == 0.0 {
		cfg.ClientConnection.QPS = DefaultClientConnectionQPS
	}
	if cfg.ClientConnection.Burst == 0 {
		cfg.ClientConnection.Burst = DefaultClientConnectionBurst
	}

	defaultBindAddress := net.JoinHostPort("0.0.0.0", strconv.Itoa(DefaultInsecureBinderPort))
	if len(cfg.HealthzBindAddress) == 0 {
		cfg.HealthzBindAddress = defaultBindAddress
	} else {
		if host, port, err := net.SplitHostPort(cfg.HealthzBindAddress); err == nil {
			if len(host) == 0 {
				host = "0.0.0.0"
			}
			hostPort := net.JoinHostPort(host, port)
			cfg.HealthzBindAddress = hostPort
		} else {
			// Something went wrong splitting the host/port, could just be a missing port so check if the
			// existing value is a valid IP address. If so, use that with the default scheduler port
			if host := net.ParseIP(cfg.HealthzBindAddress); host != nil {
				hostPort := net.JoinHostPort(cfg.HealthzBindAddress, strconv.Itoa(DefaultInsecureBinderPort))
				cfg.HealthzBindAddress = hostPort
			} else {
				// TODO: in godelschedulerconfig we should let this error instead of stomping with a default value
				cfg.HealthzBindAddress = defaultBindAddress
			}
		}
	}

	// metrics
	if len(cfg.MetricsBindAddress) == 0 {
		cfg.MetricsBindAddress = defaultBindAddress
	} else {
		if host, port, err := net.SplitHostPort(cfg.MetricsBindAddress); err == nil {
			if len(host) == 0 {
				host = "0.0.0.0"
			}
			hostPort := net.JoinHostPort(host, port)
			cfg.MetricsBindAddress = hostPort
		} else {
			// Something went wrong splitting the host/port, could just be a missing port so check if the
			// existing value is a valid IP address. If so, use that with the default scheduler port
			if host := net.ParseIP(cfg.MetricsBindAddress); host != nil {
				hostPort := net.JoinHostPort(cfg.MetricsBindAddress, strconv.Itoa(DefaultInsecureBinderPort))
				cfg.MetricsBindAddress = hostPort
			} else {
				// TODO: in godelschedulerconfig we should let this error instead of stomping with a default value
				cfg.MetricsBindAddress = defaultBindAddress
			}
		}
	}

	// Use the default LeaderElectionConfiguration options
	defaultsconfig.SetDefaultLeaderElectionConfiguration(&cfg.LeaderElection)
	if len(cfg.LeaderElection.ResourceName) == 0 {
		cfg.LeaderElection.ResourceName = BinderDefaultLockObjectName
	}

	// Enable profiling by default in the scheduler
	if cfg.EnableProfiling == nil {
		enableProfiling := true
		cfg.EnableProfiling = &enableProfiling
	}

	// Enable contention profiling by default if profiling is enabled
	if *cfg.EnableProfiling && cfg.EnableContentionProfiling == nil {
		enableContentionProfiling := true
		cfg.EnableContentionProfiling = &enableContentionProfiling
	}

	cfg.VolumeBindingTimeoutSeconds = VolumeBindingTimeoutSeconds
	if cfg.ReservationTimeOutSeconds == 0 {
		cfg.ReservationTimeOutSeconds = DefaultReservationTimeOutSeconds
	}
}
