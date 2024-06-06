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

package tracing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	separator = ":="
	delimiter = ";"

	ResultSuccess = "success"
	ResultFailure = "failure"
)

type SpanContext interface {
	Marshal() string
	Carrier() map[string]string
	IsEmpty() bool
}

type spanContext struct {
	context map[string]string
}

func (s *spanContext) Marshal() string {
	kvs := make([]string, 0, len(s.context))
	for k, v := range s.context {
		kvs = append(kvs, k+separator+v)
	}
	return strings.Join(kvs, delimiter)
}

func (s *spanContext) IsEmpty() bool {
	return len(s.context) == 0
}

func (s *spanContext) Carrier() map[string]string {
	return s.context
}

func NewEmptySpanContext() SpanContext {
	return &spanContext{
		context: make(map[string]string),
	}
}

func NewSpanContextFromString(str string) SpanContext {
	c := make(map[string]string)
	items := strings.Split(str, delimiter)
	for _, item := range items {
		index := strings.Index(item, separator)
		if index < 1 {
			continue
		}
		c[item[:index]] = item[index+2:]
	}
	return &spanContext{
		context: c,
	}
}

type TraceContext interface {
	Span() trace.Span
	SpanContext() SpanContext
	RootSpanContext() SpanContext
	WithTags(tags ...attribute.KeyValue)
	WithFields(fields ...attribute.KeyValue)
	Finish()
	FinishWithOptions(opts ...trace.SpanOption)
}

type traceContext struct {
	ctx               context.Context
	span              trace.Span
	spanName          string
	spanContext       SpanContext
	parentSpanContext SpanContext
}

func (t *traceContext) Span() trace.Span {
	return t.span
}

func (t *traceContext) SpanContext() SpanContext {
	if t.spanContext != nil && !t.spanContext.IsEmpty() {
		return t.spanContext
	}

	t.spanContext = NewEmptySpanContext()
	_ = injectContext(t.ctx, t.span, t.spanContext)
	return t.spanContext
}

func (t *traceContext) RootSpanContext() SpanContext {
	return t.parentSpanContext
}

func (t *traceContext) WithTags(tags ...attribute.KeyValue) {
	t.span.SetAttributes(tags...)
}

func (t *traceContext) Finish() {
	t.span.End(trace.WithTimestamp(time.Now()))
}

func (t *traceContext) FinishWithOptions(opts ...trace.SpanOption) {
	t.span.End(opts...)
}

func (t *traceContext) WithFields(fields ...attribute.KeyValue) {
	if len(fields) == 0 {
		return
	}
	t.span.SetAttributes(fields...)
}

func NewTraceContext(ctx context.Context, parentSpanContext SpanContext, span trace.Span) TraceContext {
	return &traceContext{
		parentSpanContext: parentSpanContext,
		ctx:               ctx,
		span:              span,
	}
}

type noopTraceContext struct{}

func (n noopTraceContext) Span() trace.Span                           { return nil }
func (n noopTraceContext) SpanContext() SpanContext                   { return &spanContext{} }
func (n noopTraceContext) RootSpanContext() SpanContext               { return &spanContext{} }
func (n noopTraceContext) WithTags(tags ...attribute.KeyValue)        {}
func (n noopTraceContext) WithFields(fields ...attribute.KeyValue)    {}
func (n noopTraceContext) Finish()                                    {}
func (n noopTraceContext) FinishWithOptions(opts ...trace.SpanOption) {}

func NewEmptyTraceContext() TraceContext {
	return &noopTraceContext{}
}

func WithDispatcherOption() trace.SpanOption {
	return WithComponent("dispatcher")
}

func WithSchedulerOption() trace.SpanOption {
	return WithComponent("scheduler")
}

func WithBinderOption() trace.SpanOption {
	return WithComponent("binder")
}

func WithComponent(component string) trace.SpanOption {
	return trace.WithAttributes(attribute.String("component", component))
}

func WithKeyValue(key string, value interface{}) attribute.KeyValue {
	return attribute.String(key, fmt.Sprint(value))
}

func WithResult(result string) trace.SpanOption {
	return trace.WithAttributes(attribute.String("result", fmt.Sprint(result)))
}

func WithStartTime(startTime time.Time) trace.SpanOption {
	return trace.WithTimestamp(startTime)
}

func WithScheduler(scheduler string) trace.SpanOption {
	return trace.WithAttributes(attribute.String("scheduler", scheduler))
}

func WithResultTag(value interface{}) attribute.KeyValue {
	return attribute.String("result", fmt.Sprint(value))
}

func WithHitCacheTag(result string) attribute.KeyValue {
	return attribute.String("hitCache", result)
}

func WithReasonField(reason string) attribute.KeyValue {
	return attribute.String("reason", reason)
}

func WithMessageField(message string) attribute.KeyValue {
	return attribute.String("message", message)
}

func WithErrorField(err error) attribute.KeyValue {
	if err != nil {
		return attribute.String("error", err.Error())
	}
	return attribute.KeyValue{}
}

func TruncateErrors(errs []error) []error {
	if len(errs) > 10 {
		return errs[:10]
	}
	return errs
}

func WithErrorFields(errs []error) []attribute.KeyValue {
	ret := make([]attribute.KeyValue, 0)
	for _, err := range errs {
		if err != nil {
			ret = append(ret, attribute.String("error", err.Error()))
		}
	}
	return ret
}

func WithE2ELatency(initialTime time.Time) attribute.KeyValue {
	return attribute.String("e2e", time.Since(initialTime).String())
}

func WithE2EExcludedTag(value bool) attribute.KeyValue {
	if value {
		return attribute.String("class", "backoff")
	} else {
		return attribute.String("class", "normal")
	}
}

func WithQueueField(queueName string) attribute.KeyValue {
	return attribute.String(QueueTag, queueName)
}

func WithEverScheduledTag(everScheduled bool) trace.SpanOption {
	return trace.WithAttributes(attribute.Bool("everScheduled", everScheduled))
}

func WithNodeGroupField(nodeGroup string) attribute.KeyValue {
	return attribute.String("nodeGroup", nodeGroup)
}

func WithPodKeyField(podKey string) attribute.KeyValue {
	return attribute.String("podKey", podKey)
}

func WithPodOwnerField(podOwner string) attribute.KeyValue {
	return attribute.String("podOwner", podOwner)
}

func WithNodeNameField(nodeName string) attribute.KeyValue {
	return attribute.String("nodeName", nodeName)
}

func WithNominatedNodeField(nominatedNode string) attribute.KeyValue {
	return attribute.String("nominatedNode", nominatedNode)
}

func WithVictimPodsField(victims string) attribute.KeyValue {
	return attribute.String("victimPods", victims)
}

func WithPlugins(plugins map[string]sets.String) []attribute.KeyValue {
	var pluginNames []attribute.KeyValue
	for pluginType, names := range plugins {
		var nameSlice []string
		for n := range names {
			nameSlice = append(nameSlice, n)
		}
		pluginNames = append(pluginNames, attribute.String(fmt.Sprintf("%v Plugins:", pluginType), strings.Join(nameSlice, ",")))
	}
	return pluginNames
}

func WithTemplateKeyField(templateKey string) attribute.KeyValue {
	return attribute.String("templateKey", templateKey)
}

func WithNodeCircleKey(key string) attribute.KeyValue {
	return attribute.String("nodeCircleKey", key)
}
