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

	"go.opentelemetry.io/otel/trace"
)

var GlobalNoopTracer tracer = newNoopTracer()

func newNoopTracer() *NoopTracer {
	tracer := trace.NewNoopTracerProvider().Tracer("noop")
	return &NoopTracer{
		tracer: tracer,
	}
}

type NoopTracer struct {
	tracer trace.Tracer
}

func (tracer *NoopTracer) Init(componentName, idc, cluster string) error {
	if tracer.tracer == nil {
		tracer.tracer = trace.NewNoopTracerProvider().Tracer("noop")
	}

	return nil
}

func (tracer *NoopTracer) StartSpan(ctx context.Context, spanType, spanName string, spanContext SpanContext, opts ...trace.SpanOption) (trace.Span, context.Context, error) {
	ctx, span := tracer.tracer.Start(ctx, spanName, opts...)
	return span, ctx, nil
}

func (tracer *NoopTracer) InjectContext(ctx context.Context, span trace.Span, spanContext SpanContext) error {
	return nil
}

func (tracer *NoopTracer) Close() error {
	return nil
}
