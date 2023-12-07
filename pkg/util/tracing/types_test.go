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
	"testing"
)

func TestSpanContext(t *testing.T) {
	spanContext := NewEmptySpanContext()
	carrier := spanContext.Carrier()
	carrier["logid"] = "123"
	carrier["traceid"] = "456"
	t.Log(spanContext.Marshal())

	newSpanContext := NewSpanContextFromString(spanContext.Marshal())
	if newSpanContext.Carrier()["logid"] != "123" {
		t.Error("logid not equal")
	}

	if newSpanContext.Carrier()["traceid"] != "456" {
		t.Error("traceid not equal")
	}
}
