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
)

type SpanInfo struct {
	finishOptions trace.LifeCycleOption
	startOptions  []trace.SpanOption
}

func NewSpanInfo(startOptions ...trace.SpanOption) *SpanInfo {
	return &SpanInfo{
		finishOptions: trace.WithAttributes(),
		startOptions:  startOptions,
	}
}

func (s *SpanInfo) WithFields(fields ...attribute.KeyValue) {
	s.finishOptions.ApplyEvent(trace.NewEventConfig(
		trace.WithTimestamp(time.Now()),
		trace.WithAttributes(fields...),
	))
}

func (s *SpanInfo) WithTags(tags ...trace.SpanOption) {
	s.startOptions = append(s.startOptions, tags...)
}

func (s *SpanInfo) FinishSpan(name string, parentSpanContext SpanContext, startTime time.Time) {
	s.startOptions = append(s.startOptions, WithStartTime(startTime))
	tc := createTraceContext(context.Background(), name, parentSpanContext, s.startOptions...)
	s.finishOptions = trace.WithTimestamp(time.Now())
	tc.FinishWithOptions(s.finishOptions)
}
