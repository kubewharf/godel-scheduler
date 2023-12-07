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
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

type SchedulingTrace interface {
	GetRootSpanContext() SpanContext
	NewTraceContext(parentSpanName, name string, options ...trace.SpanOption) TraceContext
	GetTraceContext(name string) TraceContext
}

type NoopSchedulingTrace struct{}

func (n *NoopSchedulingTrace) GetRootSpanContext() SpanContext {
	return NewEmptySpanContext()
}

func (n *NoopSchedulingTrace) NewTraceContext(parentSpanName, name string, options ...trace.SpanOption) TraceContext {
	return noopTraceContext{}
}

func (n *NoopSchedulingTrace) GetTraceContext(name string) TraceContext {
	return noopTraceContext{}
}

var _ SchedulingTrace = &schedulingTraceImpl{}

type schedulingTraceImpl struct {
	componentName   string
	rootSpanContext SpanContext
	// key is span name
	contexts map[string]TraceContext
	// basicSpanOptions will be applied to each span
	basicSpanOptions []trace.SpanOption
}

func NewSchedulingTrace(pod *v1.Pod, options ...trace.SpanOption) *schedulingTraceImpl {
	rootSpanContext := GetSpanContextFromPod(pod)

	options = []trace.SpanOption{
		trace.WithAttributes(attribute.String(PodTag, klog.KObj(pod).String())),
	}

	if rootSpanContext.IsEmpty() {
		rootTraceContext := createTraceContext(context.Background(), RootSpan, NewEmptySpanContext(), options...)
		rootSpanContext = rootTraceContext.SpanContext()
		rootTraceContext.Finish()
	}

	nOpts := len(options)
	return &schedulingTraceImpl{
		rootSpanContext:  rootSpanContext,
		basicSpanOptions: options[:nOpts:nOpts],
		contexts:         map[string]TraceContext{},
	}
}

func (p *schedulingTraceImpl) GetRootSpanContext() SpanContext {
	return p.rootSpanContext
}

func (p *schedulingTraceImpl) NewTraceContext(parentSpanName, name string, options ...trace.SpanOption) TraceContext {
	opts := append(p.basicSpanOptions, options...)
	parentSpanCtx := p.rootSpanContext
	if parentSpanName != RootSpan {
		pCtx := p.contexts[parentSpanName]
		if pCtx != nil {
			parentSpanCtx = pCtx.SpanContext()
		}
	}

	ctx := createTraceContext(context.Background(), name, parentSpanCtx, opts...)
	p.contexts[name] = ctx
	return ctx
}

func (p *schedulingTraceImpl) GetTraceContext(name string) TraceContext {
	return p.contexts[name]
}

func AsyncFinishTraceContext(t TraceContext, endTime time.Time) {
	if t == nil {
		return
	}
	go t.FinishWithOptions(trace.WithTimestamp(endTime))
}
