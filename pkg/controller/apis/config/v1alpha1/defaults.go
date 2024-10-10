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

package v1alpha1

import (
	"net"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	componentbaseconfig "k8s.io/component-base/config/v1alpha1"

	reservationconfig "github.com/kubewharf/godel-scheduler/pkg/controller/reservation/config"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

const (
	GodelControllerManagerSecurePort   = 10659
	GodelControllerManagerInSecurePort = 10651
	// DefaultClientConnectionQPS is default scheduler qps
	DefaultClientConnectionQPS = 10000.0
	// DefaultClientConnectionBurst is default scheduler burst
	DefaultClientConnectionBurst = 10000
	// DefaultGodelControllerManagerAddress is the default address for the scheduler status server.
	// May be overridden by a flag at startup.
	DefaultGodelControllerManagerAddress = "0.0.0.0"
)

var DefaultBindAddress = net.JoinHostPort(DefaultGodelControllerManagerAddress, strconv.Itoa(GodelControllerManagerInSecurePort))

func addDefaultingFuncs(scheme *runtime.Scheme) error {
	return RegisterDefaults(scheme)
}

func SetDefaults_GodelControllerManagerConfiguration(obj *GodelControllerManagerConfiguration) {
	if obj.Generic == nil {
		obj.Generic = &GenericControllerManagerConfiguration{}
	}

	{
		if len(obj.Generic.ClientConnection.ContentType) == 0 {
			obj.Generic.ClientConnection.ContentType = "application/vnd.kubernetes.protobuf"
		}
		if obj.Generic.ClientConnection.QPS == 0.0 {
			obj.Generic.ClientConnection.QPS = DefaultClientConnectionQPS
		}
		if obj.Generic.ClientConnection.Burst == 0 {
			obj.Generic.ClientConnection.Burst = DefaultClientConnectionBurst
		}
		// For Healthz and Metrics bind addresses, we want to check:
		// 1. If the value is nil, default to 0.0.0.0 and default scheduler port
		// 2. If there is a value set, attempt to split it. If it's just a port (ie, ":1234"), default to 0.0.0.0 with that port
		// 3. If splitting the value fails, check if the value is even a valid IP. If so, use that with the default port.
		// Otherwise use the default bind address
		if len(obj.HealthzBindAddress) == 0 {
			obj.HealthzBindAddress = DefaultBindAddress
		} else {
			if host, port, err := net.SplitHostPort(obj.HealthzBindAddress); err == nil {
				if len(host) == 0 {
					host = DefaultGodelControllerManagerAddress
				}
				hostPort := net.JoinHostPort(host, port)
				obj.HealthzBindAddress = hostPort
			} else {
				// Something went wrong splitting the host/port, could just be a missing port so check if the
				// existing value is a valid IP address. If so, use that with the default scheduler port
				if host := net.ParseIP(obj.HealthzBindAddress); host != nil {
					hostPort := net.JoinHostPort(obj.HealthzBindAddress, strconv.Itoa(GodelControllerManagerInSecurePort))
					obj.HealthzBindAddress = hostPort
				} else {
					// TODO: in godelschedulerconfig we should let this error instead of stomping with a default value
					obj.HealthzBindAddress = DefaultBindAddress
				}
			}
		}

		if len(obj.MetricsBindAddress) == 0 {
			obj.MetricsBindAddress = DefaultBindAddress
		} else {
			if host, port, err := net.SplitHostPort(obj.MetricsBindAddress); err == nil {
				if len(host) == 0 {
					host = DefaultGodelControllerManagerAddress
				}
				hostPort := net.JoinHostPort(host, port)
				obj.MetricsBindAddress = hostPort
			} else {
				// Something went wrong splitting the host/port, could just be a missing port so check if the
				// existing value is a valid IP address. If so, use that with the default scheduler port
				if host := net.ParseIP(obj.MetricsBindAddress); host != nil {
					hostPort := net.JoinHostPort(obj.MetricsBindAddress, strconv.Itoa(GodelControllerManagerInSecurePort))
					obj.MetricsBindAddress = hostPort
				} else {
					// TODO: in godelschedulerconfig we should let this error instead of stomping with a default value
					obj.MetricsBindAddress = DefaultBindAddress
				}
			}
		}
	}

	SetDefaultGenericControllerManagerConfiguration(obj.Generic)

	if obj.ReservationController == nil {
		obj.ReservationController = reservationconfig.NewReservationControllerConfiguration()
	}
	reservationconfig.SetDefaultReservationController(obj.ReservationController)

	if obj.Tracer == nil {
		obj.Tracer = tracing.DefaultNoopOptions()
	}
}

func SetDefaultGenericControllerManagerConfiguration(obj *GenericControllerManagerConfiguration) {
	zero := metav1.Duration{}
	if obj.Address == "" {
		obj.Address = "0.0.0.0"
	}
	if obj.MinResyncPeriod == zero {
		obj.MinResyncPeriod = metav1.Duration{Duration: 12 * time.Hour}
	}
	if obj.ControllerStartInterval == zero {
		obj.ControllerStartInterval = metav1.Duration{Duration: 0 * time.Second}
	}
	if len(obj.Controllers) == 0 {
		obj.Controllers = []string{"*"}
	}

	if len(obj.LeaderElection.ResourceLock) == 0 {
		// Use lease-based leader election to reduce cost.
		// We migrated for EndpointsLease lock in 1.17 and starting in 1.20 we
		// migrated to Lease lock.
		obj.LeaderElection.ResourceLock = "leases"
	}

	// Use the default ClientConnectionConfiguration and LeaderElectionConfiguration options
	componentbaseconfig.RecommendedDefaultClientConnectionConfiguration(&obj.ClientConnection)
	componentbaseconfig.RecommendedDefaultLeaderElectionConfiguration(&obj.LeaderElection)
}
