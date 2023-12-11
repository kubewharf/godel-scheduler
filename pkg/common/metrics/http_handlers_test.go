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

package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type FakeHandler struct {
	dataSize int
}

func NewFakeHandler(size int) *FakeHandler {
	return &FakeHandler{size}
}

func (fh *FakeHandler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	resp.Write(make([]byte, fh.dataSize))
}

func Test_withLimitedLengthHandler(t *testing.T) {
	resetMetrics := false
	fakeHandlerFunc := func(len int) {
		if len > 10 {
			resetMetrics = true
		}
	}

	type args struct {
		h http.Handler
		f HandlerFunc
	}
	tests := []struct {
		name      string
		args      args
		wantReset bool
	}{
		{
			name: "small data size",
			args: args{
				h: NewFakeHandler(10),
				f: fakeHandlerFunc,
			},
			wantReset: false,
		},
		{
			name: "large data size",
			args: args{
				h: NewFakeHandler(100),
				f: fakeHandlerFunc,
			},
			wantReset: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := WithLimitedLengthHandler(tt.args.h, tt.args.f)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, new(http.Request))

			if tt.wantReset != resetMetrics {
				t.Errorf("resetMetrics = %v, wantReset %v", resetMetrics, tt.wantReset)
			}
		})
	}
}
