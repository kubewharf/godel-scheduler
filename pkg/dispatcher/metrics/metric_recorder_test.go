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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kubewharf/godel-scheduler/pkg/common/metrics"
)

var _ metrics.MetricRecorder = &fakePodsRecorder{}

type fakePodsRecorder struct {
	counter   int64
	durations []float64
}

func (r *fakePodsRecorder) Inc(_ interface{}) {
	atomic.AddInt64(&r.counter, 1)
}

func (r *fakePodsRecorder) Dec(_ interface{}) {
	atomic.AddInt64(&r.counter, -1)
}

func (r *fakePodsRecorder) Clear() {
	atomic.StoreInt64(&r.counter, 0)
}

func (r *fakePodsRecorder) AddingLatencyInSeconds(_ interface{}, duration float64) {
	r.durations = append(r.durations, duration)
}

func TestInc(t *testing.T) {
	fakeRecorder := fakePodsRecorder{}
	var wg sync.WaitGroup
	loops := 100
	wg.Add(loops)
	for i := 0; i < loops; i++ {
		go func() {
			fakeRecorder.Inc(nil)
			wg.Done()
		}()
	}
	wg.Wait()
	if fakeRecorder.counter != int64(loops) {
		t.Errorf("Expected %v, got %v", loops, fakeRecorder.counter)
	}
}

func TestDec(t *testing.T) {
	fakeRecorder := fakePodsRecorder{counter: 100}
	var wg sync.WaitGroup
	loops := 100
	wg.Add(loops)
	for i := 0; i < loops; i++ {
		go func() {
			fakeRecorder.Dec(nil)
			wg.Done()
		}()
	}
	wg.Wait()
	if fakeRecorder.counter != int64(0) {
		t.Errorf("Expected %v, got %v", loops, fakeRecorder.counter)
	}
}

func TestClear(t *testing.T) {
	fakeRecorder := fakePodsRecorder{}
	var wg sync.WaitGroup
	incLoops, decLoops := 100, 80
	wg.Add(incLoops + decLoops)
	for i := 0; i < incLoops; i++ {
		go func() {
			fakeRecorder.Inc(nil)
			wg.Done()
		}()
	}
	for i := 0; i < decLoops; i++ {
		go func() {
			fakeRecorder.Dec(nil)
			wg.Done()
		}()
	}
	wg.Wait()
	if fakeRecorder.counter != int64(incLoops-decLoops) {
		t.Errorf("Expected %v, got %v", incLoops-decLoops, fakeRecorder.counter)
	}
	// verify Clear() works
	fakeRecorder.Clear()
	if fakeRecorder.counter != int64(0) {
		t.Errorf("Expected %v, got %v", 0, fakeRecorder.counter)
	}
}
