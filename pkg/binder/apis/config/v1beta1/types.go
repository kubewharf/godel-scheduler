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
	"bytes"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	componentbaseconfig "k8s.io/component-base/config/v1alpha1"
	"sigs.k8s.io/yaml"

	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GodelBinderConfiguration configures a godel binder.
type GodelBinderConfiguration struct {
	metav1.TypeMeta `json:",inline"`

	// DebuggingConfiguration holds configuration for Debugging related features
	// TODO: We might wanna make this a substruct like Debugging componentbaseconfig.DebuggingConfiguration
	componentbaseconfig.DebuggingConfiguration `json:"debugging"`

	// ClientConnection specifies the kubeconfig file and client connection
	// settings for the proxy server to use when communicating with the apiserver.
	ClientConnection componentbaseconfig.ClientConnectionConfiguration `json:"clientconnection"`

	// SchedulerName specifies a scheduling system, scheduling components(godel-dispatcher,
	// godel-scheduler, godel-binder) will not accept a pod, unless pod.Spec.SchedulerName == SchedulerName
	SchedulerName *string `json:"schedulerName,omitempty"`

	// LeaderElection defines the configuration of leader election client.
	LeaderElection componentbaseconfig.LeaderElectionConfiguration `json:"leaderElection"`

	// HealthzBindAddress is the IP address and port for the health check server to serve on,
	// defaulting to 0.0.0.0:10451
	HealthzBindAddress string `json:"healthzBindAddress,omitempty"`

	// MetricsBindAddress is the IP address and port for the metrics server to
	// serve on, defaulting to 0.0.0.0:10451.
	MetricsBindAddress string `json:"metricsBindAddress,omitempty"`

	VolumeBindingTimeoutSeconds int64

	// Tracer defines the configuration of tracer
	Tracer *tracing.TracerConfiguration

	// reserved resources will be released after a period of time.
	ReservationTimeOutSeconds int64 `json:"reservationTimeOutSeconds,omitempty"`

	Profile *GodelBinderProfile `json:"profile"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GodelBinderProfile is a scheduling profile.
type GodelBinderProfile struct {
	metav1.TypeMeta `json:",inline"`

	Plugins *Plugins `json:"plugins"`

	// PluginConfigs is an optional set of custom plugin arguments for each plugin.
	// Omitting config args for a plugin is equivalent to using the default config
	// for that plugin.
	PreemptionPluginConfigs []PluginConfig `json:"preemptionPluginConfigs,omitempty"`
	PluginConfigs           []PluginConfig `json:"pluginConfigs,omitempty"`
}

type Plugins struct {
	// Searching is a list of plugins that should be invoked in preemption phase
	VictimChecking *VictimCheckingPluginSet `json:"victimChecking,omitempty"`
}

// SearchingPluginSet specifies enabled and disabled plugins for an extension point.
// If an array is empty, missing, or nil, default plugins at that extension point will be used.
type VictimCheckingPluginSet struct {
	// SearchingPlugins specifies preemption plugin collections that should be used.
	PluginCollections []VictimCheckingPluginCollection `json:"pluginCollections,omitempty"`
}

type VictimCheckingPluginCollection struct {
	// if ForceQuickPass is true and result is PreemptionSucceed, return canBePreempted=true directly, no need to execute the rest of the preemption plugins
	ForceQuickPass bool `json:"forceQuickPass,omitempty"`
	// if EnableQuickPass is true and result is PreemptionSucceed, return canBePreempted=true, but need to execute the rest of the preemption plugins
	EnableQuickPass bool `json:"enableQuickPass,omitempty"`
	// PreemptionPlugins specifies preemption plugins in this collection
	Plugins []Plugin `json:"plugins,omitempty"`
}

// Plugin specifies a plugin name and its weight when applicable. Weight is used only for Score plugins.
type Plugin struct {
	// Name defines the name of plugin
	Name string `json:"name"`
	// Weight defines the weight of plugin, only used for Score plugins.
	Weight int64 `json:"weight,omitempty"`
}

// DecodeNestedObjects decodes plugin args for known types.
func (in *GodelBinderConfiguration) DecodeNestedObjects(d runtime.Decoder) error {
	decodeProfile := func(prof *GodelBinderProfile) error {
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
				return fmt.Errorf("decoding profile.                  preemptionPluginConfig[%d]: %w", j, err)
			}
		}
		return nil
	}

	if err := decodeProfile(in.Profile); err != nil {
		return err
	}
	return nil
}

// EncodeNestedObjects encodes plugin args.
func (in *GodelBinderConfiguration) EncodeNestedObjects(e runtime.Encoder) error {
	encodeProfile := func(prof *GodelBinderProfile) error {
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
				return fmt.Errorf("encoding profile.                  preemptionPluginConfig[%d]: %w", j, err)
			}
		}
		return nil
	}

	if err := encodeProfile(in.Profile); err != nil {
		return err
	}
	return nil
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
