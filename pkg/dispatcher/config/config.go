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
	componentbaseconfig "k8s.io/component-base/config/v1alpha1"

	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

// GodelDispatcherConfiguration configures a godel dispatcher.
type GodelDispatcherConfiguration struct {
	// DebuggingConfiguration holds configuration for Debugging related features
	// TODO: We might wanna make this a substruct like Debugging componentbaseconfig.DebuggingConfiguration
	componentbaseconfig.DebuggingConfiguration

	// ClientConnection specifies the kubeconfig file and client connection
	// settings for the proxy server to use when communicating with the apiserver.
	ClientConnection componentbaseconfig.ClientConnectionConfiguration

	// SchedulerName specifies a scheduling system, scheduling components(dispatcher,
	// scheduler, binder) will not accept a pod, unless pod.Spec.SchedulerName == SchedulerName
	SchedulerName *string
	// usually, we only accept pods that pod.Spec.SchedulerName == SchedulerName,
	// if TakeOverDefaultScheduler is set, scheduling components will also accept pods
	// that pod.Spec.SchedulerName == "default-scheduler".
	TakeOverDefaultScheduler bool

	// LeaderElection defines the configuration of leader election client.
	LeaderElection componentbaseconfig.LeaderElectionConfiguration

	// HealthzBindAddress is the IP address and port for the health check server to serve on,
	// defaulting to 0.0.0.0:10351
	HealthzBindAddress string

	// MetricsBindAddress is the IP address and port for the metrics server to
	// serve on, defaulting to 0.0.0.0:10351.
	MetricsBindAddress string `json:"metricsBindAddress,omitempty" yaml:"metricsBindAddress,omitempty"`

	// Tracer defines the configuration of tracer
	Tracer *tracing.TracerConfiguration `json:"tracer,omitempty" yaml:"tracer,omitempty"`
}
