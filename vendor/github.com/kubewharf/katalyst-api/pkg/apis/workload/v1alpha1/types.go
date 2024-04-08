/*
Copyright 2022 The Katalyst Authors.

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
	"bytes"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	metrics "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	"sigs.k8s.io/yaml"

	"github.com/kubewharf/katalyst-api/pkg/apis/autoscaling/v1alpha1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=spd

// ServiceProfileDescriptor captures information about a service workload
type ServiceProfileDescriptor struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the behavior of a ServiceProfileDescriptor.
	// +optional
	Spec ServiceProfileDescriptorSpec `json:"spec,omitempty"`

	// Status represents the concrete samples of ServiceProfileData with multiple resources.
	// +optional
	Status ServiceProfileDescriptorStatus `json:"status,omitempty"`
}

// DecodeNestedObjects decodes extended indicator for known types.
func (c *ServiceProfileDescriptor) DecodeNestedObjects(d runtime.Decoder) error {
	var strictDecodingErrs []error
	for i := range c.Spec.ExtendedIndicator {
		indicator := &c.Spec.ExtendedIndicator[i]
		err := indicator.decodeNestedObjects(d)
		if err != nil {
			decodingErr := fmt.Errorf("decoding .spec.extendedIndicator[%d]: %w", i, err)
			if runtime.IsStrictDecodingError(err) {
				strictDecodingErrs = append(strictDecodingErrs, decodingErr)
			} else {
				return decodingErr
			}
		}
	}
	if len(strictDecodingErrs) > 0 {
		return runtime.NewStrictDecodingError(strictDecodingErrs)
	}
	return nil
}

// EncodeNestedObjects encodes extended indicator.
func (c *ServiceProfileDescriptor) EncodeNestedObjects(e runtime.Encoder) error {
	for i := range c.Spec.ExtendedIndicator {
		indicator := &c.Spec.ExtendedIndicator[i]
		err := indicator.encodeNestedObjects(e)
		if err != nil {
			return fmt.Errorf("encoding .spec.extendedIndicator[%d]: %w", i, err)
		}
	}
	return nil
}

// ServiceProfileDescriptorSpec is the specification of the behavior of the SPD.
type ServiceProfileDescriptorSpec struct {
	// TargetRef points to the controller managing the set of pods for the
	// spd-controller to control - e.g. Deployment, StatefulSet.
	// SPD should have one-to-one mapping relationships with workload.
	TargetRef v1alpha1.CrossVersionObjectReference `json:"targetRef"`

	// BaselinePercent marks off a bunch of instances, and skip adjusting Knobs
	// for them; those instances are defined as baselines, and can be compared
	// with other (experimental/production) instances to demonstrate the benefits.
	// If BaselinePercent is not set, we should take all instances as production instances.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	BaselinePercent *int32 `json:"baselinePercent,omitempty"`

	// if multiple ExtendedIndicator are defined, it means that the service should
	// satisfy all the strategies in those extended indicators
	// +optional
	// +listMapKey=name
	// +listType=map
	ExtendedIndicator []ServiceExtendedIndicatorSpec `json:"extendedIndicator,omitempty"`

	// if multiple BusinessIndicator are defined, it means that we should
	// try to satisfy all of those indicator targets
	// +optional
	// +listMapKey=name
	// +listType=map
	BusinessIndicator []ServiceBusinessIndicatorSpec `json:"businessIndicator,omitempty"`

	// if multiple SystemIndicator are defined, it means that we should
	// try to satisfy all of those indicator targets
	// +optional
	// +listMapKey=name
	// +listType=map
	SystemIndicator []ServiceSystemIndicatorSpec `json:"systemIndicator,omitempty"`
}

// ServiceExtendedIndicatorSpec specifies extended workload characteristics and can serve as
// an extension to indicators beyond business and system scopes. Examples include policies for
// custom memory reclamation and I/O limitations specific to a service.
type ServiceExtendedIndicatorSpec struct {
	// Name defines the name of extended module
	Name string `json:"name"`

	// Indicators defines extend workload characteristics, Indicators can have arbitrary structure.
	// Each kind of Indicator must be named using Name, followed by the suffix 'Indicators'.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Indicators runtime.RawExtension `json:"indicators,omitempty"`
}

func (c *ServiceExtendedIndicatorSpec) decodeNestedObjects(d runtime.Decoder) error {
	gvk := SchemeGroupVersion.WithKind(c.Name + "Indicators")
	// dry-run to detect and skip out-of-tree extended indicators.
	if _, _, err := d.Decode(nil, &gvk, nil); runtime.IsNotRegisteredError(err) {
		return nil
	}

	var strictDecodingErr error
	obj, parsedGvk, err := d.Decode(c.Indicators.Raw, &gvk, nil)
	if err != nil {
		decodingArgsErr := fmt.Errorf("decoding extended indicators %s: %w", c.Name, err)
		if obj != nil && runtime.IsStrictDecodingError(err) {
			strictDecodingErr = runtime.NewStrictDecodingError([]error{decodingArgsErr})
		} else {
			return decodingArgsErr
		}
	}
	if parsedGvk.GroupKind() != gvk.GroupKind() {
		return fmt.Errorf("indicators for %s were not of type %s, got %s", c.Name, gvk.GroupKind(), parsedGvk.GroupKind())
	}
	c.Indicators.Object = obj
	return strictDecodingErr
}

func (c *ServiceExtendedIndicatorSpec) encodeNestedObjects(e runtime.Encoder) error {
	if c.Indicators.Object == nil {
		return nil
	}
	var buf bytes.Buffer
	err := e.Encode(c.Indicators.Object, &buf)
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
	c.Indicators.Raw = json
	return nil
}

// IndicatorLevelName defines several levels for each indicator, and we will
// always try to keep the actual indicator in acceptable intervals instead of
// as an accurate value. Those intervals are marked by IndicatorLevelName.
type IndicatorLevelName string

const (
	// IndicatorLevelLowerBound is usually used to define the lower bound to define
	// service working states. For instance, if rpc-latency is defined as a
	// business indicator, if actual observed value is below IndicatorLevelLowerBound,
	//	it means the workload works perfectly; if observed value is above
	// 	IndicatorLevelLowerBound but below IndicatorLevelUpperBound, it means the workload
	//	works can still work, but may suffer with performance downgrade.
	IndicatorLevelLowerBound = "LowerBound"

	// IndicatorLevelUpperBound is usually used to define the upper bound that
	// the workload be bear with. For instance, if rpc-latency is defined as a
	// business indicator, if actual observed value is above IndicatorLevelUpperBound,
	// it means the workload is broken and can't serve the online traffic anymore.
	IndicatorLevelUpperBound = "UpperBound"
)

type Indicator struct {
	IndicatorLevel IndicatorLevelName `json:"indicatorLevel"`
	Value          float32            `json:"value"`
}

type ServiceBusinessIndicatorName string

const (
	ServiceBusinessIndicatorNameRPCLatency ServiceBusinessIndicatorName = "RPCLatency"
)

// ServiceBusinessIndicatorSpec defines workload profiling in business level, such as rpc-latency,
// success-rate, service-health-score and so on, and general control-flow works as below
//
// - according to workload states, user defines several key indicates
// - user-system calculate and update observed values in status
// - sysadvisor (in-tree katalyst) decides system-indicator offset according to business-indicator
// - sysadvisor (along with reporter and qrm) to perform resources and controlKnob actions
type ServiceBusinessIndicatorSpec struct {
	// Name is used to define the business-related profiling indicator for the workload,
	// e.g. rpc-latency, success-rate, service-health-score and so on.
	// Users can use it as an expended way, and customize sysadvisor to adapter with it.
	Name ServiceBusinessIndicatorName `json:"name"`

	// +optional
	Indicators []Indicator `json:"indicators,omitempty"`
}

type ServiceSystemIndicatorName string

const (
	ServiceSystemIndicatorNameCPUSchedWait  ServiceSystemIndicatorName = "cpu_sched_wait"
	ServiceSystemIndicatorNameCPUUsageRatio ServiceSystemIndicatorName = "cpu_usage_ratio"
	ServiceSystemIndicatorNameCPI           ServiceSystemIndicatorName = "cpi"
)

// ServiceSystemIndicatorSpec defines workload profiling in system level, such as
// cpu_sched_wait、cpi、mbw ... and so on, and sysadvisor (along with reporter and qrm)
// will try to perform resources and controlKnob actions
//
// System-target indicator (along with its values in each level) could be difficult
// to pre-define, and it may have strong correlations with both workload characters
// and node environments, so we suggest users to run offline analysis pipelines to
// get those stats.
type ServiceSystemIndicatorSpec struct {
	// Name is used to define the system-related profiling indicator for the workload,
	// e.g. cpu_sched_wait、cpi、mbw ... and so on.
	// Users can use it as an expended way, and customize sysadvisor to adapter with it.
	Name ServiceSystemIndicatorName `json:"name"`

	// +optional
	Indicators []Indicator `json:"indicators,omitempty"`
}

// +kubebuilder:validation:Enum=avg;max

type Aggregator string

const (
	Avg Aggregator = "avg"
	Max Aggregator = "max"
)

// ServiceBusinessIndicatorStatus is connected with ServiceBusinessIndicatorSpec with Name
// to indicate the observed info for this workload (as for this indicator).
type ServiceBusinessIndicatorStatus struct {
	Name ServiceBusinessIndicatorName `json:"name"`

	// Current indicates the current observed value for this business indicator
	// +optional
	Current *float32 `json:"current,omitempty"`
}

// AggPodMetrics records the aggregated metrics based.
type AggPodMetrics struct {
	// Aggregator indicates how the metrics data in Items are calculated, i.e.
	// defines the aggregation functions.
	Aggregator Aggregator `json:"aggregator"`

	// +optional
	Items []metrics.PodMetrics `json:"items,omitempty"`
}

// ServiceProfileDescriptorStatus describes the observed info of the spd.
type ServiceProfileDescriptorStatus struct {
	// +optional
	AggMetrics []AggPodMetrics `json:"aggMetrics,omitempty"`

	// +optional
	BusinessStatus []ServiceBusinessIndicatorStatus `json:"businessStatus,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ServiceProfileDescriptorList is a collection of SPD objects.
type ServiceProfileDescriptorList struct {
	metav1.TypeMeta `json:",inline"`

	// Standard list metadata
	// More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#metadata
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// items is the list of SPDs
	Items []ServiceProfileDescriptor `json:"items"`
}
