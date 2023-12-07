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

package api

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	v1 "k8s.io/api/core/v1"
	"k8s.io/component-base/metrics"

	pkgmetrics "github.com/kubewharf/godel-scheduler/pkg/common/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

type ObservableUnit interface {
	GetUnitProperty() UnitProperty
}

type UnitProperty interface {
	GetPodProperty() *PodProperty
	ObserveProperty
}

// ObserveProperty contains object property used by metrics、tracing.
type ObserveProperty interface {
	ConvertToMetricsLabels() metrics.Labels
	ConvertToTracingTags() trace.SpanOption
}

var _ ObserveProperty = &PodProperty{}

type PodProperty struct {
	Namespace  string
	UnitKey    string
	Priority   int32
	Qos        podutil.PodResourceType
	SubCluster string
}

// complete will complete the Metric Label Value, if some label is empty, we will set it to default value.
func completePodLabels(labels metrics.Labels) {
	setDefaultValueIfEmpty(labels, pkgmetrics.QosLabel)
	setDefaultValueIfEmpty(labels, pkgmetrics.SubClusterLabel)
}

func setDefaultValueIfEmpty(labels metrics.Labels, label string) {
	if labels[label] == "" {
		labels[label] = pkgmetrics.UndefinedLabelValue
	}
}

func (p *PodProperty) ConvertToMetricsLabels() metrics.Labels {
	ret := metrics.Labels{}
	if p != nil {
		ret[pkgmetrics.SubClusterLabel] = p.SubCluster
		ret[pkgmetrics.QosLabel] = string(p.Qos)
	}
	completePodLabels(ret)
	return ret
}

const (
	NamespaceTagKey  = "namespace"
	UnitKeyTagKey    = "unitKey"
	PriorityTagKey   = "priority"
	QosTagKey        = "qos"
	SubClusterTagKey = "subCluster"

	UnitTypeTagKey  = "unitType"
	MinMemberTagKey = "minMember"
)

func (p *PodProperty) ConvertToTracingTags() trace.SpanOption {
	return trace.WithAttributes(
		attribute.String(NamespaceTagKey, p.Namespace),
		attribute.String(UnitKeyTagKey, p.UnitKey),
		attribute.Int(PriorityTagKey, int(p.Priority)),
		attribute.String(QosTagKey, string(p.Qos)),
		attribute.String(SubClusterTagKey, p.SubCluster),
	)
}

// ExtractPodProperty extract property from Pod, if Pod is nil return nil
func ExtractPodProperty(pod *v1.Pod) *PodProperty {
	if pod == nil {
		return nil
	}

	p := &PodProperty{
		Namespace:  pod.Namespace,
		UnitKey:    GetUnitKey(pod),
		Priority:   GetPodPriority(pod),
		Qos:        GetPodQos(pod),
		SubCluster: GetPodSubCluster(pod),
	}
	return p
}

func GetUnitKey(pod *v1.Pod) string {
	if pod == nil {
		return ""
	}

	if len(pod.Annotations) == 0 || len(pod.Annotations[podutil.PodGroupNameAnnotationKey]) == 0 {
		return string(SinglePodUnitType) + "/" + pod.Namespace + "/" + pod.Name
	}

	pgName := pod.Annotations[podutil.PodGroupNameAnnotationKey]
	return string(PodGroupUnitType) + "/" + pod.Namespace + "/" + pgName
}

func compatibleSubCluster(subCluster string) string {
	if subCluster == DefaultSubCluster {
		subCluster = "-"
	}
	return subCluster
}

func GetPodSubCluster(pod *v1.Pod) string {
	if pod.Spec.NodeSelector != nil {
		return compatibleSubCluster(pod.Spec.NodeSelector[config.DefaultSubClusterKey])
	}
	return "-"
}

func GetPodPriority(pod *v1.Pod) int32 {
	return podutil.GetDefaultPriorityForGodelPod(pod)
}

func GetPodQos(pod *v1.Pod) podutil.PodResourceType {
	qos, err := podutil.GetPodResourceType(pod)
	if err != nil {
		return podutil.UndefinedPod
	}
	return qos
}

var _ ObserveProperty = &ScheduleUnitProperty{}

type ScheduleUnitProperty struct {
	// TODO: figure out whether we should embed PodProperty into ScheduleUnitProperty
	*PodProperty
	MinMember int
	UnitType  string
}

func NewScheduleUnitProperty(unit ScheduleUnit) (*ScheduleUnitProperty, error) {
	if unit == nil {
		return nil, fmt.Errorf("empty unit")
	}

	var podProperty *PodProperty
	for _, podInfo := range unit.GetPods() {
		if podInfo.Pod != nil {
			podProperty = ExtractPodProperty(podInfo.Pod)
			break
		}
	}

	if podProperty == nil {
		return nil, fmt.Errorf("empty podProperty")
	}

	mm, err := unit.GetMinMember()
	if err != nil {
		return nil, err
	}

	return &ScheduleUnitProperty{
		PodProperty: podProperty,
		MinMember:   mm,
		UnitType:    string(unit.Type()),
	}, nil
}

func (up *ScheduleUnitProperty) GetPodProperty() *PodProperty {
	return up.PodProperty
}

func completeUnitLabels(labels metrics.Labels) {
	setDefaultValueIfEmpty(labels, pkgmetrics.QosLabel)
	setDefaultValueIfEmpty(labels, pkgmetrics.SubClusterLabel)
	setDefaultValueIfEmpty(labels, pkgmetrics.UnitTypeLabel)
}

func (up *ScheduleUnitProperty) ConvertToMetricsLabels() metrics.Labels {
	ret := metrics.Labels{}
	if up != nil {
		ret[pkgmetrics.SubClusterLabel] = up.SubCluster
		ret[pkgmetrics.QosLabel] = string(up.Qos)
		ret[pkgmetrics.UnitTypeLabel] = up.UnitType
	}
	completeUnitLabels(ret)
	return ret
}

func (up *ScheduleUnitProperty) ConvertToTracingTags() trace.SpanOption {
	return trace.WithAttributes(
		attribute.String(UnitTypeTagKey, up.UnitType),
		attribute.Int(MinMemberTagKey, up.MinMember),
	)
}

func MustConvertToMetricsLabels(property UnitProperty) metrics.Labels {
	if property != nil {
		return property.ConvertToMetricsLabels()
	}

	ret := metrics.Labels{}
	completeUnitLabels(ret)
	return ret
}
