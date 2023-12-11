/*
Copyright 2019 The Kubernetes Authors.

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

	godelcache "github.com/kubewharf/godel-scheduler/pkg/binder/cache"
	godelqueue "github.com/kubewharf/godel-scheduler/pkg/binder/queue"
)

// CacheDebugger provides ways to check and write cache information for debugging.
// From https://github.com/kubernetes/kubernetes/pull/70500 The scheduler cache comparer
// and dumper are triggered together when the scheduler receives a SIGUSR2:
// kill -SIGUSR2 <the scheduler PID>
type CacheDebugger struct {
	Comparer CacheComparer
	Dumper   CacheDumper
}

// New creates a CacheDebugger.
func New(
	nodeLister corelisters.NodeLister,
	podLister corelisters.PodLister,
	cache godelcache.BinderCache,
	podQueue godelqueue.BinderQueue,
) *CacheDebugger {
	return &CacheDebugger{
		Comparer: CacheComparer{
			NodeLister: nodeLister,
			PodLister:  podLister,
			Cache:      cache,
			PodQueue:   podQueue,
		},
		Dumper: CacheDumper{
			cache:    cache,
			podQueue: podQueue,
		},
	}
}

// ListenForSignal starts a goroutine that will trigger the CacheDebugger's
// behavior when the process receives SIGINT (Windows) or SIGUSER2 (non-Windows).
func (d *CacheDebugger) ListenForSignal(stopCh <-chan struct{}) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, compareSignal)

	go func() {
		for {
			select {
			case <-stopCh:
				return
			case <-ch:
				d.Comparer.Compare()
				d.Dumper.DumpAll()
			}
		}
	}()
}
