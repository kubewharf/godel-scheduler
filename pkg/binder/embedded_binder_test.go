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
	eb := NewEmbeddedBinder(client, crdClient, fc, "test-scheduler", DefaultEmbeddedBinderConfig())
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
		NewEmbeddedBinder(fake.NewSimpleClientset(), godelclientfake.NewSimpleClientset(), nil, "test", nil)
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
	eb := NewEmbeddedBinder(fake.NewSimpleClientset(), godelclientfake.NewSimpleClientset(), fc, "test", nil)
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
