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

package controllers

import (
	k8smetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/kubewharf/godel-scheduler/pkg/controller/reservation/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/version"
)

var metricsList = []k8smetrics.Registerable{
	controllerInstanceCount,
	buildInfo,
}

func init() {
	metrics.Install(&metricsList)
}

// Register controller manager metrics.
func Register() {
	once.Do(func() {
		for _, m := range metricsList {
			legacyregistry.MustRegister(m)
		}
	})

	info := version.Get()
	buildInfo.WithLabelValues(info.Major, info.Minor, info.GitVersion, info.GitCommit, info.GitTreeState, info.BuildDate, info.GoVersion, info.Compiler, info.Platform).Set(1)
}
