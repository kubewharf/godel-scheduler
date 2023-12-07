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
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	v1 "k8s.io/api/core/v1"

	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

type TracerConfig string

const (
	NoopConfig TracerConfig = "noop"
)

// StartSpanForPodWithParentSpan creates a span and tracing context of related pod. The new span will be based on spanCtx if it is not empty.
// If both spanCtx is empty, a root span is created for the pod and return the new tracing context as spanCtx value.
// The returned value will include the new created span and spanCtx.
// If root span is created, spanCtx from root span is returned, otherwise returned empty.
func StartSpanForPodWithParentSpan(podKey, spanName string, parentSpanContext SpanContext, options ...trace.SpanOption) (TraceContext, error) {
	if parentSpanContext == nil || parentSpanContext.IsEmpty() {
		return NewEmptyTraceContext(), fmt.Errorf("parent span context is empty")
	}

	opts := []trace.SpanOption{
		trace.WithAttributes(attribute.String("pod", podKey)),
	}
	opts = append(opts, options...)
	return createTraceContext(context.Background(), spanName, parentSpanContext, opts...), nil
}

func StartSpanForPod(podKey, spanName string, options ...trace.SpanOption) (TraceContext, error) {
	opts := []trace.SpanOption{
		trace.WithAttributes(attribute.String("pod", podKey)),
	}
	opts = append(opts, options...)

	// create root span
	rootSpan := createTraceContext(context.Background(), "rootSpan", NewEmptySpanContext(), opts...)
	defer func() {
		rootSpan.Span().End(trace.WithTimestamp(time.Now()))
	}()
	parentSpanContext := rootSpan.SpanContext()

	return StartSpanForPodWithParentSpan(podKey, spanName, parentSpanContext, opts...)
}

// createTraceContext does the real work of creating a span and tracing context, and returns the new tracing context.
func createTraceContext(ctx context.Context, spanName string, parentSpanContext SpanContext, options ...trace.SpanOption) TraceContext {
	span, spanCtx, _ := startSpan(ctx, DefaultSpanType, spanName, parentSpanContext, options...)
	return NewTraceContext(spanCtx, parentSpanContext, span)
}

func GetSpanContextFromPod(pod *v1.Pod) SpanContext {
	contextString := pod.Annotations[podutil.TraceContext]
	return NewSpanContextFromString(contextString)
}

func SetSpanContextForPod(pod *v1.Pod, spanContext SpanContext) {
	pod.Annotations[podutil.TraceContext] = spanContext.Marshal()
}
