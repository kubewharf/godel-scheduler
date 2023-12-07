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
	"io"

	"go.opentelemetry.io/otel/trace"
)

const (
	Cluster         = "default"
	IDC             = "lq"
	DefaultSpanType = "GodelScheduler"

	PodTag     = "pod"
	ClusterTag = "cluster"
	IDCTag     = "idc"
	ResultTag  = "result"
	QueueTag   = "queue"
)

type (
	TraceContextVal string
	TraceContextMap map[string]string
)

var ContextError = fmt.Errorf("unsupported span context")

type tracer interface {
	Init(componentName, idc, cluster string) error
	StartSpan(context.Context, string, string, SpanContext, ...trace.SpanOption) (trace.Span, context.Context, error)
	InjectContext(context.Context, trace.Span, SpanContext) error
	Close() error
}

func NewTracer(componentName string, cfg *TracerConfiguration) io.Closer {
	globalTracer = provider[TracerConfig(*cfg.Tracer)]
	if globalTracer == nil {
		globalTracer = provider[NoopConfig]
	}
	if err := globalTracer.Init(componentName, *cfg.IDCName, *cfg.ClusterName); err != nil {
		globalTracer = provider[NoopConfig]
	}
	return globalTracer
}

var (
	provider     = map[TracerConfig]tracer{}
	globalTracer tracer
)

func init() {
	provider[NoopConfig] = GlobalNoopTracer
}

func startSpan(ctx context.Context, spanType, spanName string, spanContext SpanContext, opts ...trace.SpanOption) (trace.Span, context.Context, error) {
	if globalTracer == nil {
		globalTracer = provider[NoopConfig]
	}
	return globalTracer.StartSpan(ctx, spanType, spanName, spanContext, opts...)
}

func injectContext(ctx context.Context, span trace.Span, spanContext SpanContext) error {
	if globalTracer == nil {
		globalTracer = provider[NoopConfig]
	}
	return globalTracer.InjectContext(ctx, span, spanContext)
}
