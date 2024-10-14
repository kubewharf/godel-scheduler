/*
Copyright 2024 The Godel Scheduler Authors.

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

package controllers

import (
	"sync"

	k8smetrics "k8s.io/component-base/metrics"
)

var (
	// TODO: replace this with k8s.io/component-base/metrics/prometheus/controllers
	// after upgrade the import version
	once                    sync.Once
	controllerInstanceCount = k8smetrics.NewGaugeVec(
		&k8smetrics.GaugeOpts{
			Name:           "running_managed_controllers",
			Help:           "Indicates where instances of a controller are currently running",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"name", "manager"},
	)

	buildInfo = k8smetrics.NewGaugeVec(
		&k8smetrics.GaugeOpts{
			Name:           "controller_manager_build_info",
			Help:           "A metric with a constant value which indicates the build info of the controller manager.",
			StabilityLevel: k8smetrics.ALPHA,
		}, []string{"major", "minor", "gitVersion", "gitCommit", "gitTreeState", "buildDate", "goVersion", "compiler", "platform"})
)

// ControllerManagerMetrics is a proxy to set controller manager specific metrics.
type ControllerManagerMetrics struct {
	manager string
}

// NewControllerManagerMetrics create a new ControllerManagerMetrics, with specific manager name.
func NewControllerManagerMetrics(manager string) *ControllerManagerMetrics {
	controllerMetrics := &ControllerManagerMetrics{
		manager: manager,
	}
	return controllerMetrics
}

// ControllerStarted sets the controllerInstanceCount to 1.
// These values use set instead of inc/dec to avoid accidentally double counting
// a controller that starts but fails to properly signal when it crashes.
func (a *ControllerManagerMetrics) ControllerStarted(name string) {
	controllerInstanceCount.With(k8smetrics.Labels{"name": name, "manager": a.manager}).Set(float64(1))
}

// ControllerStopped sets the controllerInstanceCount to 0.
func (a *ControllerManagerMetrics) ControllerStopped(name string) {
	controllerInstanceCount.With(k8smetrics.Labels{"name": name, "manager": a.manager}).Set(float64(0))
}
