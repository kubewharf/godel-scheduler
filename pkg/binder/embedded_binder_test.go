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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	fakecache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/fake"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

func newTestEmbeddedBinder(t *testing.T) (*EmbeddedBinder, *fake.Clientset) {
	t.Helper()
	client := fake.NewSimpleClientset()
	crdClient := godelclientfake.NewSimpleClientset()
	fc := &fakecache.Cache{
		AssumeFunc:       func(pod *v1.Pod) {},
		ForgetFunc:       func(pod *v1.Pod) {},
		IsAssumedPodFunc: func(pod *v1.Pod) bool { return false },
		IsCachedPodFunc:  func(pod *v1.Pod) bool { return false },
		GetPodFunc:       func(pod *v1.Pod) *v1.Pod { return pod },
		UnitStatus:       unitstatus.NewUnitStatusMap(),
	}
	eb := NewEmbeddedBinder(client, crdClient, fc, "test-scheduler", DefaultEmbeddedBinderConfig(), nil)
	return eb, client
}

func makeQueuedPodInfo(pod *v1.Pod) *framework.QueuedPodInfo {
	return &framework.QueuedPodInfo{
		Pod: pod,
	}
}

func makeTestBindRequest(podName, namespace, nodeName string) *BindRequest {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			UID:       types.UID(podName + "-uid"),
		},
	}
	return &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          []*framework.QueuedPodInfo{makeQueuedPodInfo(pod)},
		NodeName:      nodeName,
		SchedulerName: "test-scheduler",
	}
}

func TestEmbeddedBinder_New(t *testing.T) {
	eb, _ := newTestEmbeddedBinder(t)
	assert.NotNil(t, eb)
	assert.Equal(t, "test-scheduler", eb.SchedulerName())
	assert.NotNil(t, eb.Config())
	assert.Equal(t, DefaultMaxBindRetries, eb.Config().MaxBindRetries)
	assert.False(t, eb.IsRunning())
}

func TestEmbeddedBinder_New_NilCache(t *testing.T) {
	assert.Panics(t, func() {
		NewEmbeddedBinder(fake.NewSimpleClientset(), godelclientfake.NewSimpleClientset(), nil, "test", nil, nil)
	})
}

func TestEmbeddedBinder_New_NilConfig(t *testing.T) {
	fc := &fakecache.Cache{
		AssumeFunc:       func(pod *v1.Pod) {},
		ForgetFunc:       func(pod *v1.Pod) {},
		IsAssumedPodFunc: func(pod *v1.Pod) bool { return false },
		IsCachedPodFunc:  func(pod *v1.Pod) bool { return false },
		GetPodFunc:       func(pod *v1.Pod) *v1.Pod { return pod },
		UnitStatus:       unitstatus.NewUnitStatusMap(),
	}
	eb := NewEmbeddedBinder(fake.NewSimpleClientset(), godelclientfake.NewSimpleClientset(), fc, "test", nil, nil)
	assert.NotNil(t, eb.Config())
	assert.Equal(t, DefaultMaxBindRetries, eb.Config().MaxBindRetries)
}

func TestEmbeddedBinder_Start_Stop(t *testing.T) {
	eb, _ := newTestEmbeddedBinder(t)
	ctx := context.Background()

	err := eb.Start(ctx)
	assert.NoError(t, err)
	assert.True(t, eb.IsRunning())

	eb.Stop()
	assert.False(t, eb.IsRunning())
}

func TestEmbeddedBinder_Start_AlreadyRunning(t *testing.T) {
	eb, _ := newTestEmbeddedBinder(t)
	ctx := context.Background()

	err := eb.Start(ctx)
	assert.NoError(t, err)

	err = eb.Start(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already running")

	eb.Stop()
}

func TestEmbeddedBinder_Stop_Idempotent(t *testing.T) {
	eb, _ := newTestEmbeddedBinder(t)
	ctx := context.Background()

	_ = eb.Start(ctx)
	eb.Stop()
	eb.Stop() // should not panic
	assert.False(t, eb.IsRunning())
}

func TestEmbeddedBinder_BindUnit_NotRunning(t *testing.T) {
	eb, _ := newTestEmbeddedBinder(t)
	req := makeTestBindRequest("pod-1", "default", "node-1")

	result, err := eb.BindUnit(context.Background(), req)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrBinderNotRunning)
}

func TestEmbeddedBinder_BindUnit_InvalidRequest(t *testing.T) {
	eb, _ := newTestEmbeddedBinder(t)
	_ = eb.Start(context.Background())
	defer eb.Stop()

	result, err := eb.BindUnit(context.Background(), &BindRequest{})
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrInvalidRequest)
}

func TestEmbeddedBinder_BindUnit_SinglePod(t *testing.T) {
	eb, client := newTestEmbeddedBinder(t)
	_ = eb.Start(context.Background())
	defer eb.Stop()

	bindCalled := false
	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "binding" {
			bindCalled = true
			return true, nil, nil
		}
		return false, nil, nil
	})

	req := makeTestBindRequest("pod-1", "default", "node-1")
	result, err := eb.BindUnit(context.Background(), req)

	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.AllSucceeded())
	assert.Len(t, result.SuccessfulPods, 1)
	assert.True(t, bindCalled)
}

func TestEmbeddedBinder_BindUnit_MultiplePods(t *testing.T) {
	eb, client := newTestEmbeddedBinder(t)
	_ = eb.Start(context.Background())
	defer eb.Stop()

	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "binding" {
			return true, nil, nil
		}
		return false, nil, nil
	})

	pods := make([]*framework.QueuedPodInfo, 3)
	for i := 0; i < 3; i++ {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("pod-%d", i),
				Namespace: "default",
				UID:       types.UID(fmt.Sprintf("uid-%d", i)),
			},
		}
		pods[i] = makeQueuedPodInfo(pod)
	}

	req := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          pods,
		NodeName:      "node-1",
		SchedulerName: "test-scheduler",
	}
	result, err := eb.BindUnit(context.Background(), req)

	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.AllSucceeded())
	assert.Len(t, result.SuccessfulPods, 3)
}

func TestEmbeddedBinder_BindUnit_APIServerError(t *testing.T) {
	eb, client := newTestEmbeddedBinder(t)
	_ = eb.Start(context.Background())
	defer eb.Stop()

	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "binding" {
			return true, nil, fmt.Errorf("internal server error")
		}
		return false, nil, nil
	})

	req := makeTestBindRequest("pod-1", "default", "node-1")
	result, err := eb.BindUnit(context.Background(), req)

	assert.NoError(t, err) // BindUnit itself succeeds; individual pod failures in result
	require.NotNil(t, result)
	assert.True(t, result.AllFailed())
	assert.Len(t, result.FailedPods, 1)
}

func TestEmbeddedBinder_BindUnit_ConflictRetry(t *testing.T) {
	eb, client := newTestEmbeddedBinder(t)
	// Set a fast config for testing
	eb.config.MaxBindRetries = 3
	eb.config.BindTimeout = 10 * time.Second

	_ = eb.Start(context.Background())
	defer eb.Stop()

	attempts := 0
	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "binding" {
			attempts++
			if attempts < 3 {
				return true, nil, apierrors.NewConflict(
					schema.GroupResource{Resource: "pods"}, "pod-1", fmt.Errorf("conflict"))
			}
			return true, nil, nil // succeed on 3rd attempt
		}
		return false, nil, nil
	})

	req := makeTestBindRequest("pod-1", "default", "node-1")
	result, err := eb.BindUnit(context.Background(), req)

	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.AllSucceeded())
	assert.Equal(t, 3, attempts, "should have retried on conflict")
}

func TestEmbeddedBinder_BindUnit_Timeout(t *testing.T) {
	eb, client := newTestEmbeddedBinder(t)
	eb.config.BindTimeout = 100 * time.Millisecond

	_ = eb.Start(context.Background())
	defer eb.Stop()

	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "binding" {
			// Slow down to trigger timeout
			time.Sleep(200 * time.Millisecond)
			return true, nil, nil
		}
		return false, nil, nil
	})

	req := makeTestBindRequest("pod-1", "default", "node-1")
	result, err := eb.BindUnit(context.Background(), req)

	assert.NoError(t, err)
	require.NotNil(t, result)
	// The pod may either fail or succeed depending on timing; we verify no panic
	assert.NotNil(t, result)
}

func TestEmbeddedBinder_BindUnit_WithVictims(t *testing.T) {
	eb, client := newTestEmbeddedBinder(t)
	_ = eb.Start(context.Background())
	defer eb.Stop()

	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "binding" {
			return true, nil, nil
		}
		return false, nil, nil
	})

	req := makeTestBindRequest("pod-1", "default", "node-1")
	req.Victims = []*v1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "victim-1", Namespace: "default"}},
	}

	result, err := eb.BindUnit(context.Background(), req)
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.AllSucceeded())
}

func TestEmbeddedBinder_ConcurrentBind(t *testing.T) {
	eb, client := newTestEmbeddedBinder(t)
	_ = eb.Start(context.Background())
	defer eb.Stop()

	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "binding" {
			return true, nil, nil
		}
		return false, nil, nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := makeTestBindRequest(
				fmt.Sprintf("pod-%d", idx),
				"default",
				fmt.Sprintf("node-%d", idx%3),
			)
			result, err := eb.BindUnit(context.Background(), req)
			assert.NoError(t, err)
			assert.NotNil(t, result)
		}(i)
	}
	wg.Wait()
}

func TestEmbeddedBinder_BindUnit_NilPodInList(t *testing.T) {
	eb, _ := newTestEmbeddedBinder(t)
	_ = eb.Start(context.Background())
	defer eb.Stop()

	req := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          []*framework.QueuedPodInfo{{Pod: nil}},
		NodeName:      "node-1",
		SchedulerName: "test-scheduler",
	}
	result, err := eb.BindUnit(context.Background(), req)
	assert.NoError(t, err)
	require.NotNil(t, result)
	// nil Pod is skipped
	assert.Empty(t, result.SuccessfulPods)
	assert.Empty(t, result.FailedPods)
}

// --- NodeValidator integration tests ---

func TestEmbeddedBinder_BindUnit_NodeValidation_OwnedNode(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-valid-node",
			Namespace: "default",
			UID:       types.UID("uid-pod-valid-node"),
		},
	}
	client := fake.NewSimpleClientset(pod)
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
				Name:        nodeName,
				Annotations: map[string]string{"godel.bytedance.com/scheduler-name": "test-scheduler"},
			},
		}, nil
	})
	eb := NewEmbeddedBinder(client, crdClient, fc, "test-scheduler", DefaultEmbeddedBinderConfig(), nodeGetter)
	require.NoError(t, eb.Start(context.Background()))
	defer eb.Stop()

	req := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          []*framework.QueuedPodInfo{makeQueuedPodInfo(pod)},
		NodeName:      "node-owned",
		SchedulerName: "test-scheduler",
	}
	result, err := eb.BindUnit(context.Background(), req)
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.SuccessfulPods, 1)
}

func TestEmbeddedBinder_BindUnit_NodeValidation_OtherNode(t *testing.T) {
	client := fake.NewSimpleClientset()
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
				Name:        nodeName,
				Annotations: map[string]string{"godel.bytedance.com/scheduler-name": "scheduler-B"},
			},
		}, nil
	})
	eb := NewEmbeddedBinder(client, crdClient, fc, "test-scheduler", DefaultEmbeddedBinderConfig(), nodeGetter)
	require.NoError(t, eb.Start(context.Background()))
	defer eb.Stop()

	req := makeTestBindRequest("pod-bad-node", "default", "node-other")
	result, err := eb.BindUnit(context.Background(), req)
	assert.Error(t, err, "expected node validation to fail for node belonging to another scheduler")
	assert.Nil(t, result)
	assert.True(t, IsNodeOwnershipError(err) || containsNodeOwnership(err), "expected NodeOwnershipError")
}

func TestEmbeddedBinder_BindUnit_NodeValidation_Disabled(t *testing.T) {
	// When nodeGetter is nil, node validation is skipped and binding proceeds.
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-no-validation",
			Namespace: "default",
			UID:       types.UID("uid-pod-no-validation"),
		},
	}
	client := fake.NewSimpleClientset(pod)
	crdClient := godelclientfake.NewSimpleClientset()
	fc := &fakecache.Cache{
		AssumeFunc:       func(pod *v1.Pod) {},
		ForgetFunc:       func(pod *v1.Pod) {},
		IsAssumedPodFunc: func(pod *v1.Pod) bool { return false },
		IsCachedPodFunc:  func(pod *v1.Pod) bool { return false },
		GetPodFunc:       func(pod *v1.Pod) *v1.Pod { return pod },
		UnitStatus:       unitstatus.NewUnitStatusMap(),
	}
	// nodeGetter is nil — validation disabled
	eb := NewEmbeddedBinder(client, crdClient, fc, "test-scheduler", DefaultEmbeddedBinderConfig(), nil)
	require.NoError(t, eb.Start(context.Background()))
	defer eb.Stop()

	req := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          []*framework.QueuedPodInfo{makeQueuedPodInfo(pod)},
		NodeName:      "any-node",
		SchedulerName: "test-scheduler",
	}
	result, err := eb.BindUnit(context.Background(), req)
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.SuccessfulPods, 1, "with nil nodeGetter, bind should succeed without validation")
}

// containsNodeOwnership checks if the error chain contains a NodeOwnershipError.
func containsNodeOwnership(err error) bool {
	if err == nil {
		return false
	}
	return IsNodeOwnershipError(err)
}

// --- Per-pod NodeNames tests ---

func TestEmbeddedBinder_BindUnit_PerPodNodeNames(t *testing.T) {
	eb, client := newTestEmbeddedBinder(t)
	_ = eb.Start(context.Background())
	defer eb.Stop()

	// Track which (pod, node) pairs were bound.
	var mu sync.Mutex
	bindings := make(map[string]string) // pod name → node name
	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "binding" {
			ca := action.(k8stesting.CreateAction)
			binding := ca.GetObject().(*v1.Binding)
			mu.Lock()
			bindings[binding.Name] = binding.Target.Name
			mu.Unlock()
			return true, nil, nil
		}
		return false, nil, nil
	})

	pod1 := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pg-pod-0", Namespace: "default", UID: "uid-pg-0"}}
	pod2 := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pg-pod-1", Namespace: "default", UID: "uid-pg-1"}}
	pod3 := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pg-pod-2", Namespace: "default", UID: "uid-pg-2"}}

	req := &BindRequest{
		Unit: &framework.QueuedUnitInfo{},
		Pods: []*framework.QueuedPodInfo{
			makeQueuedPodInfo(pod1),
			makeQueuedPodInfo(pod2),
			makeQueuedPodInfo(pod3),
		},
		NodeNames: map[types.UID]string{
			"uid-pg-0": "node-A",
			"uid-pg-1": "node-B",
			"uid-pg-2": "node-A", // same node as pod-0
		},
		SchedulerName: "test-scheduler",
	}

	result, err := eb.BindUnit(context.Background(), req)
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.AllSucceeded())
	assert.Len(t, result.SuccessfulPods, 3)

	// Verify each pod was bound to the correct node.
	assert.Equal(t, "node-A", bindings["pg-pod-0"], "pod-0 should bind to node-A")
	assert.Equal(t, "node-B", bindings["pg-pod-1"], "pod-1 should bind to node-B")
	assert.Equal(t, "node-A", bindings["pg-pod-2"], "pod-2 should bind to node-A")
}

func TestEmbeddedBinder_BindUnit_PerPodNodeNames_Validation(t *testing.T) {
	// NodeNames validates every unique node; if one node fails ownership → entire request rejected.
	client := fake.NewSimpleClientset()
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
		// node-A belongs to test-scheduler; node-B belongs to other-scheduler
		owner := "test-scheduler"
		if nodeName == "node-B" {
			owner = "other-scheduler"
		}
		return &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:        nodeName,
				Annotations: map[string]string{"godel.bytedance.com/scheduler-name": owner},
			},
		}, nil
	})
	eb := NewEmbeddedBinder(client, crdClient, fc, "test-scheduler", DefaultEmbeddedBinderConfig(), nodeGetter)
	require.NoError(t, eb.Start(context.Background()))
	defer eb.Stop()

	pod1 := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns", UID: "u1"}}
	pod2 := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p2", Namespace: "ns", UID: "u2"}}
	req := &BindRequest{
		Unit: &framework.QueuedUnitInfo{},
		Pods: []*framework.QueuedPodInfo{
			makeQueuedPodInfo(pod1),
			makeQueuedPodInfo(pod2),
		},
		NodeNames: map[types.UID]string{
			"u1": "node-A",
			"u2": "node-B", // belongs to other-scheduler
		},
		SchedulerName: "test-scheduler",
	}
	result, err := eb.BindUnit(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "node validation failed")
}
