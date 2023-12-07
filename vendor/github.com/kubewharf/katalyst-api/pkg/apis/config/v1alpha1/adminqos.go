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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=adminqosconfigurations,shortName=aqc
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=.metadata.creationTimestamp
// +kubebuilder:printcolumn:name="SELECTOR",type=string,JSONPath=".spec.nodeLabelSelector"
// +kubebuilder:printcolumn:name="PRIORITY",type=string,JSONPath=".spec.priority"
// +kubebuilder:printcolumn:name="NODES",type=string,JSONPath=".spec.ephemeralSelector.nodeNames"
// +kubebuilder:printcolumn:name="DURATION",type=string,JSONPath=".spec.ephemeralSelector.lastDuration"
// +kubebuilder:printcolumn:name="VALID",type=string,JSONPath=".status.conditions[?(@.type==\"Valid\")].status"
// +kubebuilder:printcolumn:name="REASON",type=string,JSONPath=".status.conditions[?(@.type==\"Valid\")].reason"
// +kubebuilder:printcolumn:name="MESSAGE",type=string,JSONPath=".status.conditions[?(@.type==\"Valid\")].message"

// AdminQoSConfiguration is the Schema for the configuration API used by admin QoS policy
type AdminQoSConfiguration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AdminQoSConfigurationSpec `json:"spec,omitempty"`
	Status GenericConfigStatus       `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// AdminQoSConfigurationList contains a list of AdminQoSConfiguration
type AdminQoSConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AdminQoSConfiguration `json:"items"`
}

// AdminQoSConfigurationSpec defines the desired state of AdminQoSConfiguration
type AdminQoSConfigurationSpec struct {
	GenericConfigSpec `json:",inline"`

	// Config is custom field for admin qos configuration
	Config AdminQoSConfig `json:"config"`
}

type AdminQoSConfig struct {
	// ReclaimedResourceConfig is a configuration for reclaim resource
	// +optional
	ReclaimedResourceConfig *ReclaimedResourceConfig `json:"reclaimedResourceConfig,omitempty"`

	// EvictionConfig is a configuration for eviction
	// +optional
	EvictionConfig *EvictionConfig `json:"evictionConfig,omitempty"`
}

type ReclaimedResourceConfig struct {
	// EnableReclaim is a flag to enable reclaim resource, if true, reclaim resource will be enabled,
	// which means reclaim resource will be reported to custom node resource and support colocation between
	// reclaimed_cores pod and other pods, otherwise, reclaim resource will be disabled.
	// +optional
	EnableReclaim *bool `json:"enableReclaim,omitempty"`

	// ReservedResourceForReport is a reserved resource for report to custom node resource, which is used to
	// prevent reclaim resource from being requested by reclaimed_cores pods.
	// +optional
	ReservedResourceForReport *v1.ResourceList `json:"reservedResourceForReport,omitempty"`

	// MinReclaimedResourceForReport is a minimum reclaimed resource for report to custom node resource, which means
	// if reclaimed resource is less than MinReclaimedResourceForReport, then reclaimed resource will be reported as
	// MinReclaimedResourceForReport.
	// +optional
	MinReclaimedResourceForReport *v1.ResourceList `json:"minReclaimedResourceForReport,omitempty"`

	// ReservedResourceForAllocate is a resource reserved for non-reclaimed_cores pods that are not allocated to
	// reclaimed_cores pods. It is used to set aside some buffer resources to avoid sudden increase in resource
	// requirements.
	// +optional
	ReservedResourceForAllocate *v1.ResourceList `json:"reservedResourceForAllocate,omitempty"`

	// MinReclaimedResourceForAllocate is a resource reserved for reclaimed_cores pods，these resources will not be used
	// by shared_cores pods.
	// +optional
	MinReclaimedResourceForAllocate *v1.ResourceList `json:"minReclaimedResourceForAllocate,omitempty"`

	// CPUHeadroomConfig is a configuration for cpu headroom
	// +optional
	CPUHeadroomConfig *CPUHeadroomConfig `json:"cpuHeadroomConfig,omitempty"`

	// MemoryHeadroomConfig is a configuration for memory headroom
	// +optional
	MemoryHeadroomConfig *MemoryHeadroomConfig `json:"memoryHeadroomConfig,omitempty"`
}

type MemoryHeadroomConfig struct {
	// MemoryHeadroomUtilBasedConfig is a config for utilization based memory headroom policy
	// +optional
	UtilBasedConfig *MemoryHeadroomUtilBasedConfig `json:"utilBasedConfig,omitempty"`
}

type MemoryHeadroomUtilBasedConfig struct {
	// Enable is a flag to enable utilization based memory headroom policy
	// +optional
	Enable *bool `json:"enable,omitempty"`

	// FreeBasedRatio is the estimation of free memory utilization, which can
	// be used as system buffer to oversold memory.
	// +kubectl:validation:Minimum=0
	// +kubectl:validation:Maximum=1
	// +optional
	FreeBasedRatio *float64 `json:"freeBasedRatio,omitempty"`

	// StaticBasedCapacity is the static oversold memory size by bytes
	// +kubectl:validation:Minimum=0
	// +optional
	StaticBasedCapacity *float64 `json:"staticBasedCapacity,omitempty"`

	// CacheBasedRatio is the rate of cache oversold, 0 means disable cache oversold
	// +kubectl:validation:Minimum=0
	// +kubectl:validation:Maximum=1
	// +optional
	CacheBasedRatio *float64 `json:"cacheBasedRatio,omitempty"`
}

type CPUHeadroomConfig struct {
	// UtilBasedConfig is a config for utilization based cpu headroom policy
	// +optional
	UtilBasedConfig *CPUHeadroomUtilBasedConfig `json:"utilBasedConfig,omitempty"`
}

type CPUHeadroomUtilBasedConfig struct {
	// Enable is a flag to enable utilization based cpu headroom policy
	// +optional
	Enable *bool `json:"enable,omitempty"`

	// TargetReclaimedCoreUtilization is the target reclaimed core utilization to be used for
	// calculating the oversold cpu headroom
	// +kubectl:validation:Minimum=0
	// +kubectl:validation:Maximum=1
	// +optional
	TargetReclaimedCoreUtilization *float64 `json:"targetReclaimedCoreUtilization,omitempty"`

	// MaxReclaimedCoreUtilization is the max reclaimed core utilization of reclaimed_cores pool,
	// which is used to calculate the oversold cpu headroom, if zero means no limit
	// +kubectl:validation:Minimum=0
	// +kubectl:validation:Maximum=1
	// +optional
	MaxReclaimedCoreUtilization *float64 `json:"maxReclaimedCoreUtilization,omitempty"`

	// MaxOversoldRate is the max oversold rate of cpu headroom to the actual size of
	// reclaimed_cores pool
	// +kubectl:validation:Minimum=0
	// +optional
	MaxOversoldRate *float64 `json:"maxOversoldRate,omitempty"`

	// MaxHeadroomCapacityRate is the max headroom capacity rate of cpu headroom to the total
	// cpu capacity of node
	// +kubectl:validation:Minimum=0
	// +optional
	MaxHeadroomCapacityRate *float64 `json:"maxHeadroomCapacityRate,omitempty"`
}

type EvictionConfig struct {
	// DryRun is the list of eviction plugins to dryRun
	// '*' means "all dry-run by default"
	// 'foo' means "dry-run 'foo'"
	// first item for a particular name wins
	// +optional
	DryRun []string `json:"dryRun"`

	// CPUPressureEvictionConfig is the config for cpu pressure eviction
	// +optional
	CPUPressureEvictionConfig *CPUPressureEvictionConfig `json:"cpuPressureEvictionConfig,omitempty"`

	// SystemLoadPressureEvictionConfig is the config for system load eviction
	// +optional
	SystemLoadPressureEvictionConfig *SystemLoadPressureEvictionConfig `json:"systemLoadPressureEvictionConfig,omitempty"`

	// MemoryPressureEvictionConfig is the config for memory pressure eviction
	// +optional
	MemoryPressureEvictionConfig *MemoryPressureEvictionConfig `json:"memoryPressureEvictionConfig,omitempty"`

	// ReclaimedResourcesEvictionConfig is the config for reclaimed resources' eviction
	// +optional
	ReclaimedResourcesEvictionConfig *ReclaimedResourcesEvictionConfig `json:"reclaimedResourcesEvictionConfig,omitempty"`
}

type ReclaimedResourcesEvictionConfig struct {
	// EvictionThreshold eviction threshold rate for reclaimed resources
	// +optional
	EvictionThreshold map[v1.ResourceName]float64 `json:"evictionThreshold"`

	// GracePeriod is the grace period of reclaimed resources' eviction
	// +kubectl:validation:Minimum=0
	// +optional
	GracePeriod *int64 `json:"gracePeriod,omitempty"`
}

type CPUPressureEvictionConfig struct {
	// EnableLoadEviction is whether to enable cpu load eviction
	// +optional
	EnableLoadEviction *bool `json:"enableLoadEviction,omitempty"`

	// LoadUpperBoundRatio is the upper bound ratio of cpuset pool load, if the load
	// of the target cpuset pool is greater than the load upper bound repeatedly, the
	// eviction will be triggered
	// +kubectl:validation:Minimum=1
	// +optional
	LoadUpperBoundRatio *float64 `json:"loadUpperBoundRatio,omitempty"`

	// LoadThresholdMetPercentage is the percentage of the number of times the load
	// over the upper bound to the total number of times the load is measured, if the
	// percentage is greater than the load threshold met percentage, the eviction or
	// node tainted will be triggered
	// +kubectl:validation:Minimum=0
	// +kubectl:validation:Maximum=1
	// +optional
	LoadThresholdMetPercentage *float64 `json:"loadThresholdMetPercentage,omitempty"`

	// LoadMetricRingSize is the size of the load metric ring, which is used to calculate the
	// load of the target cpuset pool
	// +kubebuilder:validation:Minimum=1
	// +optional
	LoadMetricRingSize *int `json:"loadMetricRingSize,omitempty"`

	// LoadEvictionCoolDownTime is the cool-down time of cpu load eviction,
	// if the cpu load eviction is triggered, the cpu load eviction will be
	// disabled for the cool-down time
	// +optional
	LoadEvictionCoolDownTime *metav1.Duration `json:"loadEvictionCoolDownTime,omitempty"`

	// EnableSuppressionEviction is whether to enable pod-level cpu suppression eviction
	// +optional
	EnableSuppressionEviction *bool `json:"enableSuppressionEviction,omitempty"`

	// MaxSuppressionToleranceRate is the maximum cpu suppression tolerance rate that
	// can be set by the pod, if the cpu suppression tolerance rate of the pod is greater
	// than the maximum cpu suppression tolerance rate, the cpu suppression tolerance rate
	// of the pod will be set to the maximum cpu suppression tolerance rate
	// +kubectl:validation:Minimum=0
	// +optional
	MaxSuppressionToleranceRate *float64 `json:"maxSuppressionToleranceRate,omitempty"`

	// MinSuppressionToleranceDuration is the minimum duration a pod can tolerate cpu
	// suppression, only if the cpu suppression duration of the pod is greater than the
	// minimum cpu suppression duration, the eviction will be triggered
	// +optional
	MinSuppressionToleranceDuration *metav1.Duration `json:"minSuppressionToleranceDuration,omitempty"`

	// GracePeriod is the grace period of cpu pressure eviction
	// +kubectl:validation:Minimum=0
	// +optional
	GracePeriod *int64 `json:"gracePeriod,omitempty"`
}

type MemoryPressureEvictionConfig struct {
	// EnableNumaLevelEviction is whether to enable numa-level eviction
	// +optional
	EnableNumaLevelEviction *bool `json:"enableNumaLevelEviction,omitempty"`

	// EnableSystemLevelEviction is whether to enable system-level eviction
	// +optional
	EnableSystemLevelEviction *bool `json:"enableSystemLevelEviction,omitempty"`

	// NumaFreeBelowWatermarkTimesThreshold is the threshold for the number of
	// times NUMA's free memory falls below the watermark
	// +kubectl:validation:Minimum=0
	// +optional
	NumaFreeBelowWatermarkTimesThreshold *int `json:"numaFreeBelowWatermarkTimesThreshold,omitempty"`

	// NumaFreeBelowWatermarkTimesThreshold is the threshold for the rate of
	// kswapd reclaiming rate
	// +kubectl:validation:Minimum=0
	// +optional
	SystemKswapdRateThreshold *int `json:"systemKswapdRateThreshold,omitempty"`

	// SystemKswapdRateExceedDurationThreshold is the threshold for the duration the kswapd reclaiming rate
	// exceeds the threshold
	// +kubectl:validation:Minimum=0
	// +optional
	SystemKswapdRateExceedDurationThreshold *int `json:"systemKswapdRateExceedDurationThreshold,omitempty"`

	// NumaEvictionRankingMetrics is the metrics used to rank pods for eviction
	// at the NUMA level
	// +kubebuilder:validation:MinItems=1
	// +optional
	NumaEvictionRankingMetrics []NumaEvictionRankingMetric `json:"numaEvictionRankingMetrics,omitempty"`

	// SystemEvictionRankingMetrics is the metrics used to rank pods for eviction
	// at the system level
	// +kubebuilder:validation:MinItems=1
	// +optional
	SystemEvictionRankingMetrics []SystemEvictionRankingMetric `json:"systemEvictionRankingMetrics,omitempty"`

	// EnableRSSOveruseEviction is whether to enable pod-level rss overuse eviction
	// +optional
	EnableRSSOveruseEviction *bool `json:"enableRSSOveruseEviction,omitempty"`

	// RSSOveruseRateThreshold is the threshold for the rate of rss
	// +kubectl:validation:Minimum=0
	// +optional
	RSSOveruseRateThreshold *float64 `json:"rssOveruseRateThreshold,omitempty"`

	// GracePeriod is the grace period of memory pressure eviction
	// +kubectl:validation:Minimum=0
	// +optional
	GracePeriod *int64 `json:"gracePeriod,omitempty"`
}

type SystemLoadPressureEvictionConfig struct {
	// SoftThreshold is the soft threshold of system load pressure, it should be an integral multiple of 100, which means
	// the real threshold is (SoftThreshold / 100) * CoreNumber
	// +optional
	SoftThreshold *int64 `json:"softThreshold,omitempty"`

	// HardThreshold is the hard threshold of system load pressure, it should be an integral multiple of 100, which means
	// the real threshold is (SoftThreshold / 100) * CoreNumber
	// +optional
	HardThreshold *int64 `json:"hardThreshold,omitempty"`

	// HistorySize is the size of the load metric ring, which is used to calculate the system load
	// +kubebuilder:validation:Minimum=1
	// +optional
	HistorySize *int64 `json:"historySize,omitempty"`

	// SyncPeriod is the interval in seconds of the plugin fetch the load information
	// +kubebuilder:validation:Minimum=1
	// +optional
	SyncPeriod *int64 `json:"syncPeriod,omitempty"`

	// CoolDownTime is the cool-down time of the plugin evict pods
	// +kubebuilder:validation:Minimum=1
	// +optional
	CoolDownTime *int64 `json:"coolDownTime,omitempty"`

	// GracePeriod is the grace period of pod deletion
	// +optional
	GracePeriod *int64 `json:"gracePeriod,omitempty"`

	// the plugin considers the node is facing load pressure only when the ratio of load history which is greater than
	// threshold is greater than this percentage
	// +optional
	ThresholdMetPercentage *float64 `json:"thresholdMetPercentage,omitempty"`
}

// NumaEvictionRankingMetric is the metrics used to rank pods for eviction at the
// NUMA level
// +kubebuilder:validation:Enum=qos.pod;priority.pod;mem.total.numa.container
type NumaEvictionRankingMetric string

// SystemEvictionRankingMetric is the metrics used to rank pods for eviction at the
// system level
// +kubebuilder:validation:Enum=qos.pod;priority.pod;mem.usage.container
type SystemEvictionRankingMetric string
