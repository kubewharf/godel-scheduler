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

package binder

import (
	"context"
	"fmt"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	fakecache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/fake"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

// ---------------------------------------------------------------------------
// Benchmark helpers
// ---------------------------------------------------------------------------

func benchmarkEmbeddedBinder(b *testing.B) *EmbeddedBinder {
	b.Helper()
	client := fake.NewSimpleClientset()
	// No-op reactor for bindings — fastest possible path
	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "binding" {
			return true, nil, nil
		}
		return false, nil, nil
	})
	crdClient := godelclientfake.NewSimpleClientset()
	fc := &fakecache.Cache{
		AssumeFunc:       func(pod *v1.Pod) {},
		ForgetFunc:       func(pod *v1.Pod) {},
		IsAssumedPodFunc: func(pod *v1.Pod) bool { return false },
		IsCachedPodFunc:  func(pod *v1.Pod) bool { return false },
		GetPodFunc:       func(pod *v1.Pod) *v1.Pod { return pod },
		UnitStatus:       unitstatus.NewUnitStatusMap(),
	}
	eb := NewEmbeddedBinder(client, crdClient, fc, "bench-scheduler", DefaultEmbeddedBinderConfig(), nil)
	if err := eb.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { eb.Stop() })
	return eb
}

func makeBenchPod(idx int) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("bench-pod-%d", idx),
			Namespace: "default",
			UID:       types.UID(fmt.Sprintf("bench-uid-%d", idx)),
		},
	}
}

func makeBenchRequest(pod *v1.Pod, nodeName string) *BindRequest {
	return &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          []*framework.QueuedPodInfo{{Pod: pod}},
		NodeName:      nodeName,
		SchedulerName: "bench-scheduler",
	}
}

// ---------------------------------------------------------------------------
// Benchmark 1: Single Pod bind latency
// Measures the per-call latency of binding a single Pod through the embedded
// Binder pipeline.
// ---------------------------------------------------------------------------
func BenchmarkEmbeddedBinder_BindUnit_SinglePod(b *testing.B) {
	eb := benchmarkEmbeddedBinder(b)
	pod := makeBenchPod(0)
	req := makeBenchRequest(pod, "node-1")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = eb.BindUnit(context.Background(), req)
	}
}

// ---------------------------------------------------------------------------
// Benchmark 2: Parallel single-Pod bind throughput
// Measures throughput when multiple goroutines bind concurrently. This
// simulates realistic contention on the embedded Binder.
// ---------------------------------------------------------------------------
func BenchmarkEmbeddedBinder_BindUnit_Parallel(b *testing.B) {
	eb := benchmarkEmbeddedBinder(b)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		idx := 0
		for pb.Next() {
			pod := makeBenchPod(idx)
			req := makeBenchRequest(pod, fmt.Sprintf("node-%d", idx%10))
			_, _ = eb.BindUnit(context.Background(), req)
			idx++
		}
	})
}

// ---------------------------------------------------------------------------
// Benchmark 3: PodGroup bind (minMember=5) latency
// Measures the cost of binding a scheduling unit with multiple Pods.
// ---------------------------------------------------------------------------
func BenchmarkEmbeddedBinder_BindUnit_PodGroup(b *testing.B) {
	eb := benchmarkEmbeddedBinder(b)

	const groupSize = 5
	pods := make([]*framework.QueuedPodInfo, groupSize)
	for i := 0; i < groupSize; i++ {
		pods[i] = &framework.QueuedPodInfo{Pod: makeBenchPod(i)}
	}
	req := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          pods,
		NodeName:      "node-pg",
		SchedulerName: "bench-scheduler",
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = eb.BindUnit(context.Background(), req)
	}
}

// ---------------------------------------------------------------------------
// Benchmark 4: CacheAdapter AssumePod performance
// Measures the cost of the cache adapter's AssumePod operation.
// ---------------------------------------------------------------------------
func BenchmarkCacheAdapter_AssumePod(b *testing.B) {
	fc := &fakecache.Cache{
		AssumeFunc:       func(pod *v1.Pod) {},
		ForgetFunc:       func(pod *v1.Pod) {},
		IsAssumedPodFunc: func(pod *v1.Pod) bool { return false },
		IsCachedPodFunc:  func(pod *v1.Pod) bool { return false },
		GetPodFunc:       func(pod *v1.Pod) *v1.Pod { return pod },
		UnitStatus:       unitstatus.NewUnitStatusMap(),
	}
	adapter := NewCacheAdapter(fc)
	pod := makeBenchPod(0)
	podInfo := &framework.CachePodInfo{Pod: pod}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = adapter.AssumePod(podInfo)
		adapter.ForgetPod(podInfo)
	}
}

// ---------------------------------------------------------------------------
// Benchmark 5: NodeValidator.Validate performance
// Target: < 500ns per validation (with in-memory node lookup).
// ---------------------------------------------------------------------------
func BenchmarkNodeValidator_Validate(b *testing.B) {
	nv := NewNodeValidator("bench-scheduler", func(nodeName string) (*v1.Node, error) {
		return &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Annotations: map[string]string{
					"godel.bytedance.com/scheduler-name": "bench-scheduler",
				},
			},
		}, nil
	})

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = nv.Validate("node-1")
	}
}

// ---------------------------------------------------------------------------
// Benchmark 6: End-to-end with NodeValidator + bind
// The realistic path when node validation is enabled.
// ---------------------------------------------------------------------------
func BenchmarkEmbeddedBinder_BindUnit_WithNodeValidation(b *testing.B) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "binding" {
			return true, nil, nil
		}
		return false, nil, nil
	})
	crdClient := godelclientfake.NewSimpleClientset()
	fc := &fakecache.Cache{
		AssumeFunc:       func(pod *v1.Pod) {},
		ForgetFunc:       func(pod *v1.Pod) {},
		IsAssumedPodFunc: func(pod *v1.Pod) bool { return false },
		IsCachedPodFunc:  func(pod *v1.Pod) bool { return false },
		GetPodFunc:       func(pod *v1.Pod) *v1.Pod { return pod },
		UnitStatus:       unitstatus.NewUnitStatusMap(),
	}
	nodeGetter := NodeGetter(func(nodeName string) (*v1.Node, error) {
		return &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Annotations: map[string]string{
					"godel.bytedance.com/scheduler-name": "bench-scheduler",
				},
			},
		}, nil
	})
	eb := NewEmbeddedBinder(client, crdClient, fc, "bench-scheduler", DefaultEmbeddedBinderConfig(), nodeGetter)
	if err := eb.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	defer eb.Stop()

	pod := makeBenchPod(0)
	req := makeBenchRequest(pod, "node-1")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = eb.BindUnit(context.Background(), req)
	}
}

// ---------------------------------------------------------------------------
// Benchmark 7: Scaling test — N pods sequentially
// Helps quantify linear scaling characteristics.
// ---------------------------------------------------------------------------
func BenchmarkEmbeddedBinder_BindUnit_ScalingN(b *testing.B) {
	for _, n := range []int{1, 5, 10, 50} {
		b.Run(fmt.Sprintf("pods=%d", n), func(b *testing.B) {
			eb := benchmarkEmbeddedBinder(b)

			pods := make([]*framework.QueuedPodInfo, n)
			for i := 0; i < n; i++ {
				pods[i] = &framework.QueuedPodInfo{Pod: makeBenchPod(i)}
			}
			req := &BindRequest{
				Unit:          &framework.QueuedUnitInfo{},
				Pods:          pods,
				NodeName:      "node-scale",
				SchedulerName: "bench-scheduler",
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = eb.BindUnit(context.Background(), req)
			}
		})
	}
}
