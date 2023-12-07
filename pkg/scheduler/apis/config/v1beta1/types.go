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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	componentbaseconfig "k8s.io/component-base/config/v1alpha1"

	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GodelSchedulerConfiguration configures a scheduler
type GodelSchedulerConfiguration struct {
	metav1.TypeMeta `json:",inline"`

	// LeaderElection defines the configuration of leader election client.
	LeaderElection componentbaseconfig.LeaderElectionConfiguration `json:"leaderElection"`
	// SchedulerRenewIntervalSeconds is the duration for updating scheduler.
	// If this value is null, the default value (30s) will be used.
	SchedulerRenewIntervalSeconds int64 `json:"schedulerRenewIntervalSeconds"`

	// ClientConnection specifies the kubeconfig file and client connection
	// settings for the proxy server to use when communicating with the apiserver.
	ClientConnection componentbaseconfig.ClientConnectionConfiguration `json:"clientConnection"`
	// HealthzBindAddress is the IP address and port for the health check server to serve on,
	// defaulting to 0.0.0.0:10251
	HealthzBindAddress string `json:"healthzBindAddress,omitempty"`
	// MetricsBindAddress is the IP address and port for the metrics server to
	// serve on, defaulting to 0.0.0.0:10251.
	MetricsBindAddress string `json:"metricsBindAddress,omitempty"`

	// DebuggingConfiguration holds configuration for Debugging related features
	// TODO: We might wanna make this a substruct like Debugging componentbaseconfig.DebuggingConfiguration
	componentbaseconfig.DebuggingConfiguration `json:",inline"`

	// GodelSchedulerName is the name of the scheduler, scheduler will register scheduler crd with
	// this name, then dispatcher will choose one scheduler and use this scheduler's name to set the
	// selected-scheduler annotation on pod.
	GodelSchedulerName string `json:"godelSchedulerName,omitempty"`
	// SchedulerName specifies a scheduling system, scheduling components(dispatcher,
	// scheduler, binder) will not accept a pod, unless pod.Spec.SchedulerName == SchedulerName
	SchedulerName *string `json:"schedulerName,omitempty"`
	SubClusterKey *string `json:"subClusterKey,omitempty"`

	// Tracer defines the configuration of tracer
	Tracer *tracing.TracerConfiguration

	// TODO: update the comment
	// Profiles are scheduling profiles that kube-scheduler supports. Pods can
	// choose to be scheduled under a particular profile by setting its associated
	// scheduler name. Pods that don't specify any scheduler name are scheduled
	// with the "default-scheduler" profile, if present here.
	DefaultProfile     *GodelSchedulerProfile  `json:"defaultProfile,omitempty"`
	SubClusterProfiles []GodelSchedulerProfile `json:"subClusterProfiles,omitempty"`
}

// DecodeNestedObjects decodes plugin args for known types.
func (in *GodelSchedulerConfiguration) DecodeNestedObjects(d runtime.Decoder) error {
	decodeProfile := func(prof *GodelSchedulerProfile) error {
		if prof == nil {
			return nil
		}
		for j := range prof.PluginConfigs {
			err := prof.PluginConfigs[j].DecodeNestedObjects(d)
			if err != nil {
				return fmt.Errorf("decoding profile.pluginConfig[%d]: %w", j, err)
			}
		}
		for j := range prof.PreemptionPluginConfigs {
			err := prof.PreemptionPluginConfigs[j].DecodeNestedObjects(d)
			if err != nil {
				return fmt.Errorf("decoding profile.preemptionPluginConfig[%d]: %w", j, err)
			}
		}
		return nil
	}

	if err := decodeProfile(in.DefaultProfile); err != nil {
		return err
	}
	for _, prof := range in.SubClusterProfiles {
		if err := decodeProfile(&prof); err != nil {
			return err
		}
	}
	return nil
}

// EncodeNestedObjects encodes plugin args.
func (in *GodelSchedulerConfiguration) EncodeNestedObjects(e runtime.Encoder) error {
	encodeProfile := func(prof *GodelSchedulerProfile) error {
		if prof == nil {
			return nil
		}
		for j := range prof.PluginConfigs {
			err := prof.PluginConfigs[j].EncodeNestedObjects(e)
			if err != nil {
				return fmt.Errorf("encoding profile.pluginConfig[%d]: %w", j, err)
			}
		}
		for j := range prof.PreemptionPluginConfigs {
			err := prof.PreemptionPluginConfigs[j].EncodeNestedObjects(e)
			if err != nil {
				return fmt.Errorf("encoding profile.preemptionPluginConfig[%d]: %w", j, err)
			}
		}
		return nil
	}

	if err := encodeProfile(in.DefaultProfile); err != nil {
		return err
	}
	for _, prof := range in.SubClusterProfiles {
		if err := encodeProfile(&prof); err != nil {
			return err
		}
	}
	return nil
}

// GodelSchedulerProfile is a scheduling profile.
type GodelSchedulerProfile struct {
	// ProfileKey associates the profile to a subcluster if it is not nil.
	SubClusterName string `json:"subClusterName,omitempty"`

	// BasePluginsForKubelet specify the set of default plugins.
	BasePluginsForKubelet *config.Plugins `json:"baseKubeletPlugins,omitempty"`

	// BasePluginsForNM specify the set of default plugins.
	BasePluginsForNM *config.Plugins `json:"baseNMPlugins,omitempty"`

	// PluginConfigs is an optional set of custom plugin arguments for each plugin.
	// Omitting config args for a plugin is equivalent to using the default config
	// for that plugin.
	PluginConfigs []config.PluginConfig `json:"pluginConfig,omitempty"`

	// PreemptionPluginConfigs is an optional set of custom plugin arguments for each preemption plugin.
	// Omitting config args for a preemption plugin is equivalent to using the default config
	// for that preemption plugin.
	PreemptionPluginConfigs []config.PluginConfig `json:"preemptionPluginConfigs,omitempty"`

	// TODO: reserve temporarily(godel).
	// PercentageOfNodesToScore is the percentage of all nodes that once found feasible
	// for running a pod, the scheduler stops its search for more feasible nodes in
	// the cluster. This helps improve scheduler's performance. Scheduler always tries to find
	// at least "minFeasibleNodesToFind" feasible nodes no matter what the value of this flag is.
	// Example: if the cluster size is 500 nodes and the value of this flag is 30,
	// then scheduler stops finding further feasible nodes once it finds 150 feasible ones.
	// When the value is 0, default percentage (5%--50% based on the size of the cluster) of the
	// nodes will be scored.
	PercentageOfNodesToScore *int32 `json:"percentageOfNodesToScore,omitempty"`

	// IncreasedPercentageOfNodesToScore is used to improve the scheduling quality for particular
	// pods for which the scheduler will find more feasible nodes. It is usually greater than PercenrageOfNodesToScore.
	IncreasedPercentageOfNodesToScore *int32 `json:"increasedPercentageOfNodesToScore,omitempty"`

	// DisablePreemption disables the pod preemption feature.
	DisablePreemption *bool `json:"disablePreemption,omitempty"`

	// BlockQueue indicates whether a BlockQueue is required.
	BlockQueue *bool `json:"blockQueue,omitempty"`

	// UnitQueueSortPlugin specifies the sort plugin used in scheduling queue, defining the priority of pods in scheduling queue.
	UnitQueueSortPlugin           *config.Plugin `json:"unitQueueSortPlugin,omitempty"`
	AttemptImpactFactorOnPriority *float64       `json:"attemptImpactFactorOnPriority,omitempty"`
	// UnitInitialBackoffSeconds is the initial backoff for unschedulable pods.
	// If specified, it must be greater than 0. If this value is null, the default value (10s)
	// will be used.
	UnitInitialBackoffSeconds *int64 `json:"unitInitialBackoffSeconds,omitempty"`
	// UnitMaxBackoffSeconds is the max backoff for unschedulable pods.
	// If specified, it must be greater than or equal to unitInitialBackoffSeconds. If this value is null,
	// the default value (10s) will be used.
	UnitMaxBackoffSeconds *int64 `json:"unitMaxBackoffSeconds,omitempty"`

	// CandidatesSelectPolicies
	CandidatesSelectPolicy *string `json:"candidatesSelectPolicy,omitempty"`

	// BetterSelectPolicies
	BetterSelectPolicies *config.StringSlice `json:"betterSelectPolicies,omitempty"`
}
