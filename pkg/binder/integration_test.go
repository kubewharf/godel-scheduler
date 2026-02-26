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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	binderutils "github.com/kubewharf/godel-scheduler/pkg/binder/utils"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	fakecache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/fake"
	unitstatus "github.com/kubewharf/godel-scheduler/pkg/util/unitstatus"
)

// ---------------------------------------------------------------------------
// Integration test helpers
// ---------------------------------------------------------------------------

// integrationBinder constructs an EmbeddedBinder with full wiring: a fake K8s
// client, a fake CRD client, a fake scheduler cache, and an optional
// NodeGetter for node-ownership validation. Pods passed in are pre-registered
// with the fake client so that Bind API calls succeed.
func integrationBinder(t *testing.T, schedulerName string, config *EmbeddedBinderConfig, nodeGetter NodeGetter, pods ...*v1.Pod) (*EmbeddedBinder, *fake.Clientset) {
	t.Helper()
	objs := make([]runtime.Object, len(pods))
	for i, p := range pods {
		objs[i] = p
	}
	client := fake.NewSimpleClientset(objs...)
	crdClient := godelclientfake.NewSimpleClientset()

	assumedPods := sync.Map{}
	fc := &fakecache.Cache{
		AssumeFunc: func(pod *v1.Pod) {
			assumedPods.Store(pod.UID, pod)
		},
		ForgetFunc: func(pod *v1.Pod) {
			assumedPods.Delete(pod.UID)
		},
		IsAssumedPodFunc: func(pod *v1.Pod) bool {
			_, ok := assumedPods.Load(pod.UID)
			return ok
		},
		IsCachedPodFunc: func(pod *v1.Pod) bool { return false },
		GetPodFunc:      func(pod *v1.Pod) *v1.Pod { return pod },
		UnitStatus:      unitstatus.NewUnitStatusMap(),
	}
	eb := NewEmbeddedBinder(client, crdClient, fc, schedulerName, config, nodeGetter)
	return eb, client
}

// makeIntegrationPod is a lightweight helper that builds a Pod with the given name.
func makeIntegrationPod(name, namespace string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID("uid-" + name),
		},
	}
}

// makeNodeGetter returns a NodeGetter that supports a fixed set of nodes.
func makeNodeGetter(nodes map[string]string) NodeGetter {
	return func(nodeName string) (*v1.Node, error) {
		owner, ok := nodes[nodeName]
		if !ok {
			return nil, fmt.Errorf("node %q not found", nodeName)
		}
		return &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Annotations: map[string]string{
					"godel.bytedance.com/scheduler-name": owner,
				},
			},
		}, nil
	}
}

// ---------------------------------------------------------------------------
// Integration Test 1: End-to-end single Scheduler + embedded Binder binding
// Verifies the full Pod lifecycle: create → bind → success.
// ---------------------------------------------------------------------------
func TestIntegration_EndToEnd_SinglePodBind(t *testing.T) {
	pod := makeIntegrationPod("e2e-pod-1", "default")
	eb, _ := integrationBinder(t, "scheduler-A", DefaultEmbeddedBinderConfig(), nil, pod)
	require.NoError(t, eb.Start(context.Background()))
	defer eb.Stop()

	req := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          []*framework.QueuedPodInfo{{Pod: pod}},
		NodeName:      "node-1",
		SchedulerName: "scheduler-A",
	}
	result, err := eb.BindUnit(context.Background(), req)
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.AllSucceeded(), "single pod should bind successfully")
	assert.Len(t, result.SuccessfulPods, 1)
	assert.Equal(t, pod.UID, result.SuccessfulPods[0])
}

// ---------------------------------------------------------------------------
// Integration Test 2: Config switch — embedded vs standalone mode
// Verifies that when nodeGetter is nil (standalone-compatible), binding still
// works, and when nodeGetter is provided, validation is enforced.
// ---------------------------------------------------------------------------
func TestIntegration_ConfigSwitch_EmbeddedVsStandalone(t *testing.T) {
	tests := []struct {
		name          string
		useNodeGetter bool
		wantSuccess   bool
	}{
		{
			name:          "standalone mode (no nodeGetter) — bind succeeds",
			useNodeGetter: false,
			wantSuccess:   true,
		},
		{
			name:          "embedded mode with owned node — bind succeeds",
			useNodeGetter: true,
			wantSuccess:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := makeIntegrationPod("switch-pod", "default")
			var ng NodeGetter
			if tt.useNodeGetter {
				ng = makeNodeGetter(map[string]string{"node-x": "test-sched"})
			}
			eb, _ := integrationBinder(t, "test-sched", DefaultEmbeddedBinderConfig(), ng, pod)
			require.NoError(t, eb.Start(context.Background()))
			defer eb.Stop()

			req := &BindRequest{
				Unit:          &framework.QueuedUnitInfo{},
				Pods:          []*framework.QueuedPodInfo{{Pod: pod}},
				NodeName:      "node-x",
				SchedulerName: "test-sched",
			}
			result, err := eb.BindUnit(context.Background(), req)
			if tt.wantSuccess {
				assert.NoError(t, err)
				require.NotNil(t, result)
				assert.True(t, result.AllSucceeded())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Integration Test 3: Multi-Pod PodGroup concurrent binding
// Verifies that a PodGroup with minMember=3 has all pods bound successfully.
// ---------------------------------------------------------------------------
func TestIntegration_PodGroup_MultiPodBind(t *testing.T) {
	const podCount = 3
	pods := make([]*v1.Pod, podCount)
	qPods := make([]*framework.QueuedPodInfo, podCount)
	for i := 0; i < podCount; i++ {
		pods[i] = makeIntegrationPod(fmt.Sprintf("pg-pod-%d", i), "default")
		qPods[i] = &framework.QueuedPodInfo{Pod: pods[i]}
	}

	eb, _ := integrationBinder(t, "scheduler-A", DefaultEmbeddedBinderConfig(), nil, pods...)
	require.NoError(t, eb.Start(context.Background()))
	defer eb.Stop()

	req := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          qPods,
		NodeName:      "node-pg",
		SchedulerName: "scheduler-A",
	}

	result, err := eb.BindUnit(context.Background(), req)
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.AllSucceeded())
	assert.Len(t, result.SuccessfulPods, podCount)
}

// ---------------------------------------------------------------------------
// Integration Test 4: Bind failure triggers retry-count increment + fallback
// Verifies that after MaxLocalRetries failures, the Pod annotations reflect
// the elevated failure count.
// ---------------------------------------------------------------------------
func TestIntegration_BindFailure_RetryCountAndFallback(t *testing.T) {
	pod := makeIntegrationPod("retry-pod", "default")
	// Pre-set failure count close to threshold
	binderutils.SetBindFailureCount(pod, 4)

	config := DefaultEmbeddedBinderConfig()
	config.MaxLocalRetries = 5

	eb, client := integrationBinder(t, "scheduler-A", config, nil, pod)

	// Force bind to fail with a non-retriable error
	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "binding" {
			return true, nil, fmt.Errorf("simulated API failure")
		}
		return false, nil, nil
	})

	require.NoError(t, eb.Start(context.Background()))
	defer eb.Stop()

	req := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          []*framework.QueuedPodInfo{{Pod: pod}},
		NodeName:      "node-retry",
		SchedulerName: "scheduler-A",
	}

	result, err := eb.BindUnit(context.Background(), req)
	assert.NoError(t, err) // BindUnit itself succeeds; failure in result
	require.NotNil(t, result)
	assert.True(t, result.AllFailed())
	assert.Len(t, result.FailedPods, 1)

	// Verify failure count was incremented
	assert.Equal(t, 5, binderutils.GetBindFailureCount(pod))

	// Verify failure reason was recorded
	reason := binderutils.GetLastBindFailureReason(pod)
	assert.Contains(t, reason, "simulated API failure")

	// At count=5 with maxLocalRetries=5, should dispatch to another scheduler
	assert.True(t, binderutils.ShouldDispatchToAnotherScheduler(pod, config.MaxLocalRetries),
		"should trigger dispatch to another scheduler after reaching retry threshold")
}

// ---------------------------------------------------------------------------
// Integration Test 5: Node reshuffle — NodeValidator blocks stale binding
// Verifies that when a node's scheduler-name annotation changes, the
// NodeValidator prevents the bind.
// ---------------------------------------------------------------------------
func TestIntegration_NodeReshuffle_ValidationBlocks(t *testing.T) {
	pod := makeIntegrationPod("reshuffle-pod", "default")

	// Node was originally owned by scheduler-A, but has been reshuffled to scheduler-B
	nodeGetter := makeNodeGetter(map[string]string{
		"node-reshuffled": "scheduler-B",
	})

	eb, _ := integrationBinder(t, "scheduler-A", DefaultEmbeddedBinderConfig(), nodeGetter, pod)
	require.NoError(t, eb.Start(context.Background()))
	defer eb.Stop()

	req := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          []*framework.QueuedPodInfo{{Pod: pod}},
		NodeName:      "node-reshuffled",
		SchedulerName: "scheduler-A",
	}

	result, err := eb.BindUnit(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, IsNodeOwnershipError(err), "error should be NodeOwnershipError")
}

// ---------------------------------------------------------------------------
// Integration Test 6: Functional equivalence — same input, same outcome
// Verifies that two independent EmbeddedBinder instances with the same
// configuration and inputs produce the same bind results.
// ---------------------------------------------------------------------------
func TestIntegration_FunctionalEquivalence(t *testing.T) {
	const podCount = 5

	// Build identical pod sets and bind requests
	buildPods := func() ([]*v1.Pod, []*framework.QueuedPodInfo) {
		pods := make([]*v1.Pod, podCount)
		qPods := make([]*framework.QueuedPodInfo, podCount)
		for i := 0; i < podCount; i++ {
			pods[i] = makeIntegrationPod(fmt.Sprintf("equiv-pod-%d", i), "ns-equiv")
			qPods[i] = &framework.QueuedPodInfo{Pod: pods[i]}
		}
		return pods, qPods
	}

	// Instance A
	podsA, qPodsA := buildPods()
	ebA, _ := integrationBinder(t, "sched-equiv", DefaultEmbeddedBinderConfig(), nil, podsA...)
	require.NoError(t, ebA.Start(context.Background()))
	defer ebA.Stop()

	// Instance B (identical config)
	podsB, qPodsB := buildPods()
	ebB, _ := integrationBinder(t, "sched-equiv", DefaultEmbeddedBinderConfig(), nil, podsB...)
	require.NoError(t, ebB.Start(context.Background()))
	defer ebB.Stop()

	reqA := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          qPodsA,
		NodeName:      "node-equiv",
		SchedulerName: "sched-equiv",
	}
	reqB := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          qPodsB,
		NodeName:      "node-equiv",
		SchedulerName: "sched-equiv",
	}

	resultA, errA := ebA.BindUnit(context.Background(), reqA)
	resultB, errB := ebB.BindUnit(context.Background(), reqB)

	// Both should succeed or both should fail identically
	assert.Equal(t, errA == nil, errB == nil, "both instances should agree on error/no-error")
	require.NotNil(t, resultA)
	require.NotNil(t, resultB)
	assert.Equal(t, len(resultA.SuccessfulPods), len(resultB.SuccessfulPods),
		"both instances should bind the same number of pods")
	assert.Equal(t, len(resultA.FailedPods), len(resultB.FailedPods),
		"both instances should fail the same number of pods")
}

// ---------------------------------------------------------------------------
// Integration Test 7: End-to-end with node validation + multiple pods
// Combines node validation with a multi-pod bind to verify the complete
// pipeline functions correctly.
// ---------------------------------------------------------------------------
func TestIntegration_EndToEnd_WithNodeValidation_MultiPod(t *testing.T) {
	const podCount = 4
	pods := make([]*v1.Pod, podCount)
	qPods := make([]*framework.QueuedPodInfo, podCount)
	for i := 0; i < podCount; i++ {
		pods[i] = makeIntegrationPod(fmt.Sprintf("full-e2e-%d", i), "default")
		qPods[i] = &framework.QueuedPodInfo{Pod: pods[i]}
	}

	nodeGetter := makeNodeGetter(map[string]string{
		"node-e2e": "sched-full",
	})

	eb, _ := integrationBinder(t, "sched-full", DefaultEmbeddedBinderConfig(), nodeGetter, pods...)
	require.NoError(t, eb.Start(context.Background()))
	defer eb.Stop()

	req := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          qPods,
		NodeName:      "node-e2e",
		SchedulerName: "sched-full",
	}

	result, err := eb.BindUnit(context.Background(), req)
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.AllSucceeded())
	assert.Len(t, result.SuccessfulPods, podCount)
}

// ---------------------------------------------------------------------------
// Integration Test 8: Concurrent multi-scheduler simulation
// Verifies that multiple EmbeddedBinder instances (simulating multiple
// Schedulers) can bind concurrently without interference.
// ---------------------------------------------------------------------------
func TestIntegration_ConcurrentMultiScheduler(t *testing.T) {
	const numSchedulers = 3
	const podsPerScheduler = 5

	var wg sync.WaitGroup

	for si := 0; si < numSchedulers; si++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			schedName := fmt.Sprintf("scheduler-%d", idx)
			nodeName := fmt.Sprintf("node-%d", idx)

			pods := make([]*v1.Pod, podsPerScheduler)
			qPods := make([]*framework.QueuedPodInfo, podsPerScheduler)
			for pi := 0; pi < podsPerScheduler; pi++ {
				pods[pi] = makeIntegrationPod(fmt.Sprintf("s%d-pod-%d", idx, pi), "default")
				qPods[pi] = &framework.QueuedPodInfo{Pod: pods[pi]}
			}

			eb, _ := integrationBinder(t, schedName, DefaultEmbeddedBinderConfig(), nil, pods...)
			require.NoError(t, eb.Start(context.Background()))
			defer eb.Stop()

			req := &BindRequest{
				Unit:          &framework.QueuedUnitInfo{},
				Pods:          qPods,
				NodeName:      nodeName,
				SchedulerName: schedName,
			}

			result, err := eb.BindUnit(context.Background(), req)
			assert.NoError(t, err)
			require.NotNil(t, result)
			assert.True(t, result.AllSucceeded(),
				"scheduler %d should bind all pods successfully", idx)
			assert.Len(t, result.SuccessfulPods, podsPerScheduler)
		}(si)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Integration Test 9: Graceful lifecycle (start, process, stop, restart)
// ---------------------------------------------------------------------------
func TestIntegration_Lifecycle_StartStopRestart(t *testing.T) {
	pod1 := makeIntegrationPod("lifecycle-1", "default")
	pod2 := makeIntegrationPod("lifecycle-2", "default")
	eb, _ := integrationBinder(t, "sched-lc", DefaultEmbeddedBinderConfig(), nil, pod1, pod2)

	// Phase 1: start and bind
	require.NoError(t, eb.Start(context.Background()))
	req1 := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          []*framework.QueuedPodInfo{{Pod: pod1}},
		NodeName:      "node-lc",
		SchedulerName: "sched-lc",
	}
	result, err := eb.BindUnit(context.Background(), req1)
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.AllSucceeded())

	// Phase 2: stop
	eb.Stop()
	assert.False(t, eb.IsRunning())

	// Phase 3: bind while stopped should fail
	_, err = eb.BindUnit(context.Background(), req1)
	assert.ErrorIs(t, err, ErrBinderNotRunning)

	// Phase 4: restart
	require.NoError(t, eb.Start(context.Background()))
	assert.True(t, eb.IsRunning())

	req2 := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          []*framework.QueuedPodInfo{{Pod: pod2}},
		NodeName:      "node-lc",
		SchedulerName: "sched-lc",
	}
	result2, err := eb.BindUnit(context.Background(), req2)
	assert.NoError(t, err)
	require.NotNil(t, result2)
	assert.True(t, result2.AllSucceeded())
	eb.Stop()
}

// ---------------------------------------------------------------------------
// Integration Test 10: Timeout propagation across multiple pods
// ---------------------------------------------------------------------------
func TestIntegration_Timeout_MultiplePods(t *testing.T) {
	const podCount = 5
	pods := make([]*v1.Pod, podCount)
	qPods := make([]*framework.QueuedPodInfo, podCount)
	for i := 0; i < podCount; i++ {
		pods[i] = makeIntegrationPod(fmt.Sprintf("timeout-pod-%d", i), "default")
		qPods[i] = &framework.QueuedPodInfo{Pod: pods[i]}
	}

	config := DefaultEmbeddedBinderConfig()
	config.BindTimeout = 150 * time.Millisecond

	eb, client := integrationBinder(t, "scheduler-timeout", config, nil, pods...)

	// Make each bind take 60ms — 5 pods × 60ms = 300ms > 150ms timeout
	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() == "binding" {
			time.Sleep(60 * time.Millisecond)
			return true, nil, nil
		}
		return false, nil, nil
	})

	require.NoError(t, eb.Start(context.Background()))
	defer eb.Stop()

	req := &BindRequest{
		Unit:          &framework.QueuedUnitInfo{},
		Pods:          qPods,
		NodeName:      "node-slow",
		SchedulerName: "scheduler-timeout",
	}

	result, err := eb.BindUnit(context.Background(), req)
	assert.NoError(t, err)
	require.NotNil(t, result)

	// Some pods should succeed, some should fail due to timeout
	total := len(result.SuccessfulPods) + len(result.FailedPods)
	assert.Equal(t, podCount, total, "all pods should be accounted for")
	// At least the first couple should succeed before timeout
	assert.Greater(t, len(result.SuccessfulPods), 0, "at least some pods should bind before timeout")
	// Some should fail
	assert.Greater(t, len(result.FailedPods), 0, "some pods should fail due to timeout")
}
