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
	"bytes"
	"fmt"
	"math"
	"net"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	componentbaseconfig "k8s.io/component-base/config/v1alpha1"

	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"

	"sigs.k8s.io/yaml"
)

const (
	// SchedulerDefaultLockObjectNamespace defines default scheduler lock object namespace ("kube-system")
	SchedulerDefaultLockObjectNamespace = metav1.NamespaceSystem

	// SchedulerDefaultLockObjectName defines default scheduler lock object name ("kube-scheduler")
	SchedulerDefaultLockObjectName = "scheduler"

	// SchedulerPolicyConfigMapKey defines the key of the element in the
	// scheduler's policy ConfigMap that contains scheduler's policy config.
	SchedulerPolicyConfigMapKey = "policy.cfg"

	// FIXME
	// SchedulerDefaultProviderName defines the default provider names
	SchedulerDefaultProviderName = "DefaultProvider"

	// DefaultInsecureSchedulerPort is the default port for the scheduler status server.
	// May be overridden by a flag at startup.
	// Deprecated: use the secure GodelSchedulerPort instead.
	DefaultInsecureSchedulerPort = 10251

	// DefaultGodelSchedulerPort is the default port for the scheduler status server.
	// May be overridden by a flag at startup.
	DefaultGodelSchedulerPort = 10259

	// DefaultGodelSchedulerAddress is the default address for the scheduler status server.
	// May be overridden by a flag at startup.
	DefaultGodelSchedulerAddress = "0.0.0.0"
)

const (
	// DefaultPercentageOfNodesToScore defines the percentage of nodes of all nodes
	// that once found feasible, the scheduler stops looking for more nodes.
	// A value of 0 means adaptive, meaning the scheduler figures out a proper default.
	DefaultPercentageOfNodesToScore = 0

	DefaultIncreasedPercentageOfNodesToScore = 0

	// MaxCustomPriorityScore is the max score UtilizationShapePoint expects.
	MaxCustomPriorityScore int64 = 10

	// MaxTotalScore is the maximum total score.
	MaxTotalScore int64 = math.MaxInt64

	// MaxWeight defines the max weight value allowed for custom PriorityPolicy
	MaxWeight = MaxTotalScore / MaxCustomPriorityScore
)

const (
	// DefaultUnitInitialBackoffInSeconds is the default value for the initial backoff duration
	// for unschedulable units. To change the default podInitialBackoffDurationSeconds used by the
	// scheduler, update the ComponentConfig value in defaults.go
	DefaultUnitInitialBackoffInSeconds = 10
	// DefaultUnitMaxBackoffInSeconds is the default value for the max backoff duration
	// for unschedulable units. To change the default unitMaxBackoffDurationSeconds used by the
	// scheduler, update the ComponentConfig value in defaults.go
	DefaultUnitMaxBackoffInSeconds = 300
	// DefaultDisablePreemption is the default value for the option to disable preemption ability
	// for unschedulable pods.
	DefaultDisablePreemption        = true
	CandidateSelectPolicyBest       = "Best"
	CandidateSelectPolicyBetter     = "Better"
	CandidateSelectPolicyRandom     = "Random"
	BetterPreemptionPolicyAscending = "Ascending"
	BetterPreemptionPolicyDichotomy = "Dichotomy"
	// DefaultBlockQueue is the default value for the option to use block queue for SchedulingQueue.
	DefaultBlockQueue = false
	// DefaultPodUpgradePriorityInMinutes is the default upgrade priority duration for godel sort.
	DefaultPodUpgradePriorityInMinutes = 5
	// DefaultGodelSchedulerName defines the name of default scheduler.
	DefaultGodelSchedulerName = "godel-scheduler"
	// DefaultRenewIntervalInSeconds is the default value for the renew interval duration for scheduler.
	DefaultRenewIntervalInSeconds = 30

	// DefaultSchedulerName is default high level scheduler name
	DefaultSchedulerName = "godel-scheduler"

	// DefaultClientConnectionQPS is default scheduler qps
	DefaultClientConnectionQPS = 10000.0
	// DefaultClientConnectionBurst is default scheduler burst
	DefaultClientConnectionBurst = 10000

	// DefaultIDC is default idc name for godel scheduler
	DefaultIDC = "lq"
	// DefaultCluster is default cluster name for godel scheduler
	DefaultCluster = "default"
	// DefaultTracer is default tracer name for godel scheduler
	DefaultTracer = string(tracing.NoopConfig)

	DefaultSubClusterKey = ""

	// DefaultAttemptImpactFactorOnPriority is the default attempt factors used by godel sort
	DefaultAttemptImpactFactorOnPriority = 10.0
)

var DefaultBindAddress = net.JoinHostPort(DefaultGodelSchedulerAddress, strconv.Itoa(DefaultInsecureSchedulerPort))

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GodelSchedulerConfiguration configures a scheduler
type GodelSchedulerConfiguration struct {
	metav1.TypeMeta

	// LeaderElection defines the configuration of leader election client.
	LeaderElection componentbaseconfig.LeaderElectionConfiguration
	// SchedulerRenewIntervalSeconds is the duration for updating scheduler.
	// If this value is null, the default value (30s) will be used.
	SchedulerRenewIntervalSeconds int64

	// ClientConnection specifies the kubeconfig file and client connection
	// settings for the proxy server to use when communicating with the apiserver.
	ClientConnection componentbaseconfig.ClientConnectionConfiguration
	// HealthzBindAddress is the IP address and port for the health check server to serve on,
	// defaulting to 0.0.0.0:10251
	HealthzBindAddress string
	// MetricsBindAddress is the IP address and port for the metrics server to
	// serve on, defaulting to 0.0.0.0:10251.
	MetricsBindAddress string

	// DebuggingConfiguration holds configuration for Debugging related features
	// TODO: We might wanna make this a substruct like Debugging componentbaseconfig.DebuggingConfiguration
	componentbaseconfig.DebuggingConfiguration

	// GodelSchedulerName is the name of the scheduler, scheduler will register scheduler crd with
	// this name, then dispatcher will choose one scheduler and use this scheduler's name to set the
	// selected-scheduler annotation on pod.
	GodelSchedulerName string
	// SchedulerName specifies a scheduling system, scheduling components(dispatcher,
	// scheduler, binder) will not accept a pod, unless pod.Spec.SchedulerName == SchedulerName
	SchedulerName *string

	// Tracer defines the configuration of tracer
	Tracer *tracing.TracerConfiguration

	SubClusterKey *string

	// TODO: update the comment
	// Profiles are scheduling profiles that kube-scheduler supports. Pods can
	// choose to be scheduled under a particular profile by setting its associated
	// scheduler name. Pods that don't specify any scheduler name are scheduled
	// with the "default-scheduler" profile, if present here.
	DefaultProfile     *GodelSchedulerProfile
	SubClusterProfiles []GodelSchedulerProfile
}

// GodelSchedulerProfile is a scheduling profile.
type GodelSchedulerProfile struct {
	// ProfileKey associates the profile to a subcluster if it is not nil.
	SubClusterName string

	// BasePluginsForKubelet specify the set of default plugins.
	BasePluginsForKubelet *Plugins

	// BasePluginsForNM specify the set of default plugins.
	BasePluginsForNM *Plugins

	// PluginConfigs is an optional set of custom plugin arguments for each plugin.
	// Omitting config args for a plugin is equivalent to using the default config
	// for that plugin.
	PluginConfigs []PluginConfig

	// PreemptionPluginConfigs is an optional set of custom plugin arguments for each preemption plugin.
	// Omitting config args for a preemption plugin is equivalent to using the default config
	// for that preemption plugin.
	PreemptionPluginConfigs []PluginConfig

	// TODO: reserve temporarily(godel).
	// PercentageOfNodesToScore is the percentage of all nodes that once found feasible
	// for running a pod, the scheduler stops its search for more feasible nodes in
	// the cluster. This helps improve scheduler's performance. Scheduler always tries to find
	// at least "minFeasibleNodesToFind" feasible nodes no matter what the value of this flag is.
	// Example: if the cluster size is 500 nodes and the value of this flag is 30,
	// then scheduler stops finding further feasible nodes once it finds 150 feasible ones.
	// When the value is 0, default percentage (5%--50% based on the size of the cluster) of the
	// nodes will be scored.
	PercentageOfNodesToScore *int32

	// IncreasedPercentageOfNodesToScore is used to improve the scheduling quality for particular
	// pods for which the scheduler will find more feasible nodes. It is usually greater than PercenrageOfNodesToScore.
	IncreasedPercentageOfNodesToScore *int32

	// DisablePreemption disables the pod preemption feature.
	DisablePreemption *bool

	// CandidatesSelectPolicies
	CandidatesSelectPolicy *string

	// BetterSelectPolicies
	BetterSelectPolicies *StringSlice

	// BlockQueue indicates whether a BlockQueue is required.
	BlockQueue *bool

	// UnitQueueSortPlugin specifies the sort plugin used in scheduling queue, defining the priority of pods in scheduling queue.
	UnitQueueSortPlugin           *Plugin
	AttemptImpactFactorOnPriority *float64
	// UnitInitialBackoffSeconds is the initial backoff for unschedulable pods.
	// If specified, it must be greater than 0. If this value is null, the default value (10s)
	// will be used.
	UnitInitialBackoffSeconds *int64
	// UnitMaxBackoffSeconds is the max backoff for unschedulable pods.
	// If specified, it must be greater than or equal to unitInitialBackoffSeconds. If this value is null,
	// the default value (10s) will be used.
	UnitMaxBackoffSeconds *int64
}

// Plugins include multiple extension points. When specified, the list of plugins for
// a particular extension point are the only ones enabled. If an extension point is
// omitted from the config, then the default set of plugins is used for that extension point.
// Plugins plugins are called in the order specified here, after default plugins. If they need to
// be invoked before default plugins, default plugins must be disabled and re-enabled here in desired order.
type Plugins struct {
	// Filter is a list of plugins that should be invoked when filtering out nodes that cannot run the Pod.
	Filter *PluginSet `json:"filter,omitempty"`

	// Score is a list of plugins that should be invoked when ranking nodes that have passed the filtering phase.
	Score *PluginSet `json:"score,omitempty"`

	// Preemption is a list of plugins that should be invoked in preemption phase
	VictimSearching *VictimSearchingPluginSet `json:"victimSearching,omitempty"`

	Sorting *PluginSet `json:"sorting,omitempty"`
}

// PreemptionPluginSet specifies enabled and disabled plugins for an extension point.
// If an array is empty, missing, or nil, default plugins at that extension point will be used.
type VictimSearchingPluginSet struct {
	// PreemptionPlugins specifies preemption plugin collections that should be used.
	PluginCollections []VictimSearchingPluginCollection `json:"pluginCollections,omitempty"`
}

type VictimSearchingPluginCollection struct {
	// if ForceQuickPass is true and result is PreemptionSucceed, return canBePreempted=true directly, no need to execute the rest of the preemption plugins
	ForceQuickPass bool `json:"forceQuickPass,omitempty"`
	// if EnableQuickPass is true and result is PreemptionSucceed, return canBePreempted=true directly, no need to execute the rest of the preemption plugins
	EnableQuickPass bool `json:"enableQuickPass,omitempty"`
	// if RejectNotSure is true and result is PreemptionNotSure, return PreemptFail
	RejectNotSure bool `json:"rejectNotSure,omitempty"`
	// PreemptionPlugins specifies preemption plugins in this collection
	Plugins []Plugin `json:"plugins,omitempty"`
}

// PluginSet specifies enabled and disabled plugins for an extension point.
// If an array is empty, missing, or nil, default plugins at that extension point will be used.
type PluginSet struct {
	// Plugins specifies plugins that should be used.
	// These are called after default plugins and in the same order specified here.
	Plugins []Plugin `json:"plugins,omitempty"`
}

// Plugin specifies a plugin name and its weight when applicable. Weight is used only for Score plugins.
type Plugin struct {
	// Name defines the name of plugin
	Name string `json:"name"`
	// Weight defines the weight of plugin, only used for Score plugins.
	Weight int64 `json:"weight,omitempty"`
}

// PluginConfig specifies arguments that should be passed to a plugin at the time of initialization.
// A plugin that is invoked at multiple extension points is initialized once. Args can have arbitrary structure.
// It is up to the plugin to process these Args.
type PluginConfig struct {
	// Name defines the name of plugin being configured
	Name string `json:"name"`
	// Args defines the arguments passed to the plugins at the time of initialization. Args can have arbitrary structure.
	Args runtime.RawExtension `json:"args,omitempty"`
}

func (c *PluginConfig) DecodeNestedObjects(d runtime.Decoder) error {
	gvk := SchemeGroupVersion.WithKind(c.Name + "Args")
	// dry-run to detect and skip out-of-tree plugin args.
	if _, _, err := d.Decode(nil, &gvk, nil); runtime.IsNotRegisteredError(err) {
		return nil
	}

	obj, parsedGvk, err := d.Decode(c.Args.Raw, &gvk, nil)
	if err != nil {
		return fmt.Errorf("decoding args for plugin %s: %w", c.Name, err)
	}
	if parsedGvk.GroupKind() != gvk.GroupKind() {
		return fmt.Errorf("args for plugin %s were not of type %s, got %s", c.Name, gvk.GroupKind(), parsedGvk.GroupKind())
	}
	c.Args.Object = obj
	return nil
}

func (c *PluginConfig) EncodeNestedObjects(e runtime.Encoder) error {
	if c.Args.Object == nil {
		return nil
	}
	var buf bytes.Buffer
	err := e.Encode(c.Args.Object, &buf)
	if err != nil {
		return err
	}
	// The <e> encoder might be a YAML encoder, but the parent encoder expects
	// JSON output, so we convert YAML back to JSON.
	// This is a no-op if <e> produces JSON.
	json, err := yaml.YAMLToJSON(buf.Bytes())
	if err != nil {
		return err
	}
	c.Args.Raw = json
	return nil
}
