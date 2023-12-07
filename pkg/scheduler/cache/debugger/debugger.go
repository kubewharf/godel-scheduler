/*
Copyright 2018 The Kubernetes Authors.

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

package debugger

import (
	"os"
	"os/signal"

	corelisters "k8s.io/client-go/listers/core/v1"

	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	godelqueue "github.com/kubewharf/godel-scheduler/pkg/scheduler/queue"
)

// CacheDebugger provides ways to check and write cache information for debugging.
// From https://github.com/kubernetes/kubernetes/pull/70500 The scheduler cache comparer
// and dumper are triggered together when the scheduler receives a SIGUSR2:
// kill -SIGUSR2 <the scheduler PID>
type CacheDebugger struct {
	Comparer CacheComparer
	Dumper   CacheDumper

	stop chan struct{}
}

// New creates a CacheDebugger.
func New(
	nodeLister corelisters.NodeLister,
	podLister corelisters.PodLister,
	cache godelcache.SchedulerCache,
	unitPodQueue godelqueue.SchedulingQueue,
) *CacheDebugger {
	return &CacheDebugger{
		Comparer: CacheComparer{
			NodeLister:   nodeLister,
			PodLister:    podLister,
			Cache:        cache,
			UnitPodQueue: unitPodQueue,
		},
		Dumper: CacheDumper{
			cache:        cache,
			unitPodQueue: unitPodQueue,
		},
		stop: make(chan struct{}),
	}
}

// TODO: server http

// Run starts a goroutine that will trigger the CacheDebugger's
// behavior when the process receives SIGINT (Windows) or SIGUSER2 (non-Windows).
func (d *CacheDebugger) Run() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, compareSignal)

	go func() {
		for {
			select {
			case <-d.stop:
				return
			case <-ch:
				d.Comparer.Compare()
				d.Dumper.DumpAll()
			}
		}
	}()
}

func (d *CacheDebugger) Close() {
	close(d.stop)
}
