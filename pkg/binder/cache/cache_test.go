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

package cache

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	godelfeatures "github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func deepEqualWithoutGeneration(actual framework.NodeInfo, expected framework.NodeInfo) error {
	if (actual == nil) != (expected == nil) {
		return errors.New("one of the actual or expected is nil and the other is not")
	}
	// Ignore generation field.
	if actual != nil {
		actual.SetGeneration(0)
	}
	if expected != nil {
		expected.SetGeneration(0)
	}

	// if actual != nil && !reflect.DeepEqual(actual, expected) {
	if actual != nil && !cmp.Equal(expected, actual, cmpopts.IgnoreUnexported(framework.NodeInfoImpl{})) {
		return fmt.Errorf("got node info %s, want %s", actual, expected)
	}

	return nil
}

type hostPortInfoParam struct {
	protocol, ip string
	port         int32
}

type hostPortInfoBuilder struct {
	inputs []hostPortInfoParam
}

func newHostPortInfoBuilder() *hostPortInfoBuilder {
	return &hostPortInfoBuilder{}
}

func (b *hostPortInfoBuilder) add(protocol, ip string, port int32) *hostPortInfoBuilder {
	b.inputs = append(b.inputs, hostPortInfoParam{protocol, ip, port})
	return b
}

func (b *hostPortInfoBuilder) build() framework.HostPortInfo {
	res := make(framework.HostPortInfo)
	for _, param := range b.inputs {
		res.Add(param.ip, param.protocol, param.port)
	}
	return res
}

func newNodeInfo(guaranteedRequestedResource *framework.Resource,
	guaranteedNonzeroRequest *framework.Resource,
	bestEffortRequestedResource *framework.Resource,
	bestEffortNonzeroRequest *framework.Resource,
	pods []*v1.Pod,
	usedPorts framework.HostPortInfo,
	imageStates map[string]*framework.ImageStateSummary,
) framework.NodeInfo {
	nodeInfo := framework.NewNodeInfo(pods...)
	nodeInfo.SetGuaranteedRequested(guaranteedRequestedResource)
	nodeInfo.SetGuaranteedNonZeroRequested(guaranteedNonzeroRequest)
	nodeInfo.SetBestEffortRequested(bestEffortRequestedResource)
	nodeInfo.SetBestEffortNonZeroRequested(bestEffortNonzeroRequest)
	nodeInfo.SetUsedPorts(usedPorts)
	nodeInfo.SetImageStates(imageStates)
	return nodeInfo
}

// TestAssumePodScheduled tests that after a pod is assumed, its information is aggregated
// on node level.
func TestAssumePodScheduled(t *testing.T) {
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{string(godelfeatures.NonNativeResourceSchedulingSupport): true})

	nodeName := "node"
	testPods := []*v1.Pod{
		makeBasePod(t, nodeName, "test", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.GuaranteedPod),
		makeBasePod(t, nodeName, "test-1", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.BestEffortPod),
		makeBasePod(t, nodeName, "test-2", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}, podutil.GuaranteedPod),
		makeBasePod(t, nodeName, "test-nonzero", "", "", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.BestEffortPod),
		makeBasePod(t, nodeName, "test", "100m", "500", "example.com/foo:3", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.GuaranteedPod),
		makeBasePod(t, nodeName, "test-2", "200m", "1Ki", "example.com/foo:5", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}, podutil.BestEffortPod),
		makeBasePod(t, nodeName, "test", "100m", "500", "random-invalid-extended-key:100", []v1.ContainerPort{{}}, podutil.GuaranteedPod),
	}

	tests := []struct {
		pods []*v1.Pod

		wNodeInfo framework.NodeInfo
	}{
		{
			pods: []*v1.Pod{testPods[0]},
			wNodeInfo: newNodeInfo(
				&framework.Resource{
					MilliCPU: 100,
					Memory:   500,
				},
				&framework.Resource{
					MilliCPU: 100,
					Memory:   500,
				},
				&framework.Resource{},
				&framework.Resource{},
				[]*v1.Pod{testPods[0]},
				newHostPortInfoBuilder().add("TCP", "127.0.0.1", 80).build(),
				make(map[string]*framework.ImageStateSummary),
			),
		}, {
			pods: []*v1.Pod{testPods[1], testPods[2]},
			wNodeInfo: newNodeInfo(
				&framework.Resource{
					MilliCPU: 200,
					Memory:   1024,
				},
				&framework.Resource{
					MilliCPU: 200,
					Memory:   1024,
				},
				&framework.Resource{
					MilliCPU: 100,
					Memory:   500,
				},
				&framework.Resource{
					MilliCPU: 100,
					Memory:   500,
				},
				[]*v1.Pod{testPods[1], testPods[2]},
				newHostPortInfoBuilder().add("TCP", "127.0.0.1", 80).add("TCP", "127.0.0.1", 8080).build(),
				make(map[string]*framework.ImageStateSummary),
			),
		}, { // test non-zero request
			pods: []*v1.Pod{testPods[3]},
			wNodeInfo: newNodeInfo(
				&framework.Resource{},
				&framework.Resource{},
				&framework.Resource{
					MilliCPU: 0,
					Memory:   0,
				},
				&framework.Resource{
					MilliCPU: util.DefaultMilliCPURequest,
					Memory:   util.DefaultMemoryRequest,
				},
				[]*v1.Pod{testPods[3]},
				newHostPortInfoBuilder().add("TCP", "127.0.0.1", 80).build(),
				make(map[string]*framework.ImageStateSummary),
			),
		}, {
			pods: []*v1.Pod{testPods[4]},
			wNodeInfo: newNodeInfo(
				&framework.Resource{
					MilliCPU:        100,
					Memory:          500,
					ScalarResources: map[v1.ResourceName]int64{"example.com/foo": 3},
				},
				&framework.Resource{
					MilliCPU: 100,
					Memory:   500,
				},
				&framework.Resource{},
				&framework.Resource{},
				[]*v1.Pod{testPods[4]},
				newHostPortInfoBuilder().add("TCP", "127.0.0.1", 80).build(),
				make(map[string]*framework.ImageStateSummary),
			),
		}, {
			pods: []*v1.Pod{testPods[4], testPods[5]},
			wNodeInfo: newNodeInfo(
				&framework.Resource{
					MilliCPU:        100,
					Memory:          500,
					ScalarResources: map[v1.ResourceName]int64{"example.com/foo": 3},
				},
				&framework.Resource{
					MilliCPU: 100,
					Memory:   500,
				},
				&framework.Resource{
					MilliCPU:        200,
					Memory:          1024,
					ScalarResources: map[v1.ResourceName]int64{"example.com/foo": 5},
				},
				&framework.Resource{
					MilliCPU: 200,
					Memory:   1024,
				},
				[]*v1.Pod{testPods[4], testPods[5]},
				newHostPortInfoBuilder().add("TCP", "127.0.0.1", 80).add("TCP", "127.0.0.1", 8080).build(),
				make(map[string]*framework.ImageStateSummary),
			),
		}, {
			pods: []*v1.Pod{testPods[6]},
			wNodeInfo: newNodeInfo(
				&framework.Resource{
					MilliCPU: 100,
					Memory:   500,
				},
				&framework.Resource{
					MilliCPU: 100,
					Memory:   500,
				},
				&framework.Resource{},
				&framework.Resource{},
				[]*v1.Pod{testPods[6]},
				newHostPortInfoBuilder().build(),
				make(map[string]*framework.ImageStateSummary),
			),
		},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			cache := newBinderCache(time.Second, time.Second, nil, "")
			for _, pod := range tt.pods {
				if err := cache.AssumePod(pod); err != nil {
					t.Fatalf("AssumePod failed: %v", err)
				}
			}
			n := cache.nodeInfoMap[nodeName]
			if err := deepEqualWithoutGeneration(n, tt.wNodeInfo); err != nil {
				t.Error(err)
			}

			for _, pod := range tt.pods {
				if err := cache.ForgetPod(pod); err != nil {
					t.Fatalf("ForgetPod failed: %v", err)
				}
				if err := isForgottenFromCache(pod, cache); err != nil {
					t.Errorf("pod %s: %v", pod.Name, err)
				}
			}
		})
	}
}

type testExpirePodStruct struct {
	pod         *v1.Pod
	finishBind  bool
	assumedTime time.Time
}

func assumeAndFinishBinding(cache *binderCache, pod *v1.Pod, assumedTime time.Time) error {
	if err := cache.AssumePod(pod); err != nil {
		return err
	}
	return cache.finishBinding(pod, assumedTime)
}

// TestExpirePod tests that assumed pods will be removed if expired.
// The removal will be reflected in node info.
func TestExpirePod(t *testing.T) {
	nodeName := "node"
	testPods := []*v1.Pod{
		makeBasePod(t, nodeName, "test-1", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.BestEffortPod),
		makeBasePod(t, nodeName, "test-2", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}, podutil.GuaranteedPod),
		makeBasePod(t, nodeName, "test-3", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}, podutil.GuaranteedPod),
	}
	now := time.Now()
	ttl := 10 * time.Second
	tests := []struct {
		pods        []*testExpirePodStruct
		cleanupTime time.Time

		wNodeInfo framework.NodeInfo
	}{{ // assumed pod would expires
		pods: []*testExpirePodStruct{
			{pod: testPods[0], finishBind: true, assumedTime: now},
		},
		cleanupTime: now.Add(2 * ttl),
		wNodeInfo:   nil,
	}, { // first one would expire, second and third would not.
		pods: []*testExpirePodStruct{
			{pod: testPods[0], finishBind: true, assumedTime: now},
			{pod: testPods[1], finishBind: true, assumedTime: now.Add(3 * ttl / 2)},
			{pod: testPods[2]},
		},
		cleanupTime: now.Add(2 * ttl),
		wNodeInfo: newNodeInfo(
			&framework.Resource{
				MilliCPU: 400,
				Memory:   2048,
			},
			&framework.Resource{
				MilliCPU: 400,
				Memory:   2048,
			},
			&framework.Resource{},
			&framework.Resource{},
			// Order gets altered when removing pods.
			[]*v1.Pod{testPods[2], testPods[1]},
			newHostPortInfoBuilder().add("TCP", "127.0.0.1", 8080).build(),
			make(map[string]*framework.ImageStateSummary),
		),
	}}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			cache := newBinderCache(ttl, time.Second, nil, "")

			for _, pod := range tt.pods {
				if err := cache.AssumePod(pod.pod); err != nil {
					t.Fatal(err)
				}
				if !pod.finishBind {
					continue
				}
				if err := cache.finishBinding(pod.pod, pod.assumedTime); err != nil {
					t.Fatal(err)
				}
			}
			// pods that got bound and have assumedTime + ttl < cleanupTime will get
			// expired and removed
			cache.cleanupAssumedPods(tt.cleanupTime)
			n := cache.nodeInfoMap[nodeName]
			if err := deepEqualWithoutGeneration(n, tt.wNodeInfo); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestCleanupPodDeletionMarker(t *testing.T) {
	ttl := 10 * time.Second
	cleanupTime := time.Now().Add(700 * time.Second)
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "testPod",
			Namespace:       "testNS",
			UID:             "testUID",
			ResourceVersion: "1",
		},
		Spec: v1.PodSpec{
			NodeName: "node-1",
		},
	}

	preemptor := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "testPreemptor",
			Namespace:       "testNS",
			UID:             "testUID2",
			ResourceVersion: "1",
		},
	}

	cache := newBinderCache(ttl, time.Second, nil, "")

	cache.AddPod(pod)
	p, _ := cache.GetPod(pod)
	assert.NotEqual(t, nil, p)
	cache.MarkPodToDelete(pod, preemptor)
	markedToBeDeleted, _ := cache.IsPodMarkedToDelete(pod)
	assert.Equal(t, true, markedToBeDeleted)
	cache.cleanupPodDeletionMarker(cleanupTime)
	markedToBeDeleted, _ = cache.IsPodMarkedToDelete(pod)
	assert.Equal(t, false, markedToBeDeleted)
}

// TestAddPodWillConfirm tests that a pod being Add()ed will be confirmed if assumed.
// The pod info should still exist after manually expiring unconfirmed pods.
func TestAddPodWillConfirm(t *testing.T) {
	nodeName := "node"
	now := time.Now()
	ttl := 10 * time.Second

	testPods := []*v1.Pod{
		makeBasePod(t, nodeName, "test-1", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.GuaranteedPod),
		makeBasePod(t, nodeName, "test-2", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}, podutil.BestEffortPod),
	}
	tests := []struct {
		podsToAssume []*v1.Pod
		podsToAdd    []*v1.Pod

		wNodeInfo framework.NodeInfo
	}{{ // two pod were assumed at same time. But first one is called Add() and gets confirmed.
		podsToAssume: []*v1.Pod{testPods[0], testPods[1]},
		podsToAdd:    []*v1.Pod{testPods[0]},
		wNodeInfo: newNodeInfo(
			&framework.Resource{
				MilliCPU: 100,
				Memory:   500,
			},
			&framework.Resource{
				MilliCPU: 100,
				Memory:   500,
			},
			&framework.Resource{},
			&framework.Resource{},
			[]*v1.Pod{testPods[0]},
			newHostPortInfoBuilder().add("TCP", "127.0.0.1", 80).build(),
			make(map[string]*framework.ImageStateSummary),
		),
	}}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			cache := newBinderCache(ttl, time.Second, nil, "")
			for _, podToAssume := range tt.podsToAssume {
				if err := assumeAndFinishBinding(cache, podToAssume, now); err != nil {
					t.Fatalf("assumePod failed: %v", err)
				}
			}
			for _, podToAdd := range tt.podsToAdd {
				if err := cache.AddPod(podToAdd); err != nil {
					t.Fatalf("AddPod failed: %v", err)
				}
			}
			cache.cleanupAssumedPods(now.Add(2 * ttl))
			// check after expiration. confirmed pods shouldn't be expired.
			n := cache.nodeInfoMap[nodeName]
			if err := deepEqualWithoutGeneration(n, tt.wNodeInfo); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestAddPodWillReplaceAssumed tests that a pod being Add()ed will replace any assumed pod.
func TestAddPodWillReplaceAssumed(t *testing.T) {
	now := time.Now()
	ttl := 10 * time.Second

	assumedPod := makeBasePod(t, "assumed-node-1", "test-1", "100m", "500", "", []v1.ContainerPort{{HostPort: 80}}, podutil.GuaranteedPod)
	addedPod := makeBasePod(t, "actual-node", "test-1", "100m", "500", "", []v1.ContainerPort{{HostPort: 80}}, podutil.BestEffortPod)
	updatedPod := makeBasePod(t, "actual-node", "test-1", "200m", "500", "", []v1.ContainerPort{{HostPort: 90}}, podutil.GuaranteedPod)

	tests := []struct {
		podsToAssume []*v1.Pod
		podsToAdd    []*v1.Pod
		podsToUpdate [][]*v1.Pod

		wNodeInfo map[string]framework.NodeInfo
	}{{
		podsToAssume: []*v1.Pod{assumedPod.DeepCopy()},
		podsToAdd:    []*v1.Pod{addedPod.DeepCopy()},
		podsToUpdate: [][]*v1.Pod{{addedPod.DeepCopy(), updatedPod.DeepCopy()}},
		wNodeInfo: map[string]framework.NodeInfo{
			"assumed-node": nil,
			"actual-node": newNodeInfo(
				&framework.Resource{
					MilliCPU: 200,
					Memory:   500,
				},
				&framework.Resource{
					MilliCPU: 200,
					Memory:   500,
				},
				&framework.Resource{},
				&framework.Resource{},
				[]*v1.Pod{updatedPod.DeepCopy()},
				newHostPortInfoBuilder().add("TCP", "0.0.0.0", 90).build(),
				make(map[string]*framework.ImageStateSummary),
			),
		},
	}}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			cache := newBinderCache(ttl, time.Second, nil, "")
			for _, podToAssume := range tt.podsToAssume {
				if err := assumeAndFinishBinding(cache, podToAssume, now); err != nil {
					t.Fatalf("assumePod failed: %v", err)
				}
			}
			for _, podToAdd := range tt.podsToAdd {
				if err := cache.AddPod(podToAdd); err != nil {
					t.Fatalf("AddPod failed: %v", err)
				}
			}
			for _, podToUpdate := range tt.podsToUpdate {
				if err := cache.UpdatePod(podToUpdate[0], podToUpdate[1]); err != nil {
					t.Fatalf("UpdatePod failed: %v", err)
				}
			}
			for nodeName, expected := range tt.wNodeInfo {
				n := cache.nodeInfoMap[nodeName]
				if err := deepEqualWithoutGeneration(n, expected); err != nil {
					t.Errorf("node %q: %v", nodeName, err)
				}
			}
		})
	}
}

// TestAddPodAfterExpiration tests that a pod being Add()ed will be added back if expired.
func TestAddPodAfterExpiration(t *testing.T) {
	nodeName := "node"
	ttl := 10 * time.Second
	basePod := makeBasePod(t, nodeName, "test", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.GuaranteedPod)
	tests := []struct {
		pod *v1.Pod

		wNodeInfo framework.NodeInfo
	}{{
		pod: basePod,
		wNodeInfo: newNodeInfo(
			&framework.Resource{
				MilliCPU: 100,
				Memory:   500,
			},
			&framework.Resource{
				MilliCPU: 100,
				Memory:   500,
			},
			&framework.Resource{},
			&framework.Resource{},
			[]*v1.Pod{basePod},
			newHostPortInfoBuilder().add("TCP", "127.0.0.1", 80).build(),
			make(map[string]*framework.ImageStateSummary),
		),
	}}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			now := time.Now()
			cache := newBinderCache(ttl, time.Second, nil, "")
			if err := assumeAndFinishBinding(cache, tt.pod, now); err != nil {
				t.Fatalf("assumePod failed: %v", err)
			}
			cache.cleanupAssumedPods(now.Add(2 * ttl))
			// It should be expired and removed.
			if err := isForgottenFromCache(tt.pod, cache); err != nil {
				t.Error(err)
			}
			if err := cache.AddPod(tt.pod); err != nil {
				t.Fatalf("AddPod failed: %v", err)
			}
			// check after expiration. confirmed pods shouldn't be expired.
			n := cache.nodeInfoMap[nodeName]
			if err := deepEqualWithoutGeneration(n, tt.wNodeInfo); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestUpdatePod tests that a pod will be updated if added before.
func TestUpdatePod(t *testing.T) {
	nodeName := "node"
	ttl := 10 * time.Second
	testPods := []*v1.Pod{
		makeBasePod(t, nodeName, "test", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.GuaranteedPod),
		makeBasePod(t, nodeName, "test", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}, podutil.GuaranteedPod),
	}
	tests := []struct {
		podsToAdd    []*v1.Pod
		podsToUpdate []*v1.Pod

		wNodeInfo []framework.NodeInfo
	}{
		{ // add a pod and then update it twice
			podsToAdd:    []*v1.Pod{testPods[0]},
			podsToUpdate: []*v1.Pod{testPods[0], testPods[1], testPods[0]},
			wNodeInfo: []framework.NodeInfo{newNodeInfo(
				&framework.Resource{
					MilliCPU: 200,
					Memory:   1024,
				},
				&framework.Resource{
					MilliCPU: 200,
					Memory:   1024,
				},
				&framework.Resource{},
				&framework.Resource{},
				[]*v1.Pod{testPods[1]},
				newHostPortInfoBuilder().add("TCP", "127.0.0.1", 8080).build(),
				make(map[string]*framework.ImageStateSummary),
			), newNodeInfo(
				&framework.Resource{
					MilliCPU: 100,
					Memory:   500,
				},
				&framework.Resource{
					MilliCPU: 100,
					Memory:   500,
				},
				&framework.Resource{},
				&framework.Resource{},
				[]*v1.Pod{testPods[0]},
				newHostPortInfoBuilder().add("TCP", "127.0.0.1", 80).build(),
				make(map[string]*framework.ImageStateSummary),
			)},
		},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			cache := newBinderCache(ttl, time.Second, nil, "")
			for _, podToAdd := range tt.podsToAdd {
				if err := cache.AddPod(podToAdd); err != nil {
					t.Fatalf("AddPod failed: %v", err)
				}
			}

			for j := range tt.podsToUpdate {
				if j == 0 {
					continue
				}
				if err := cache.UpdatePod(tt.podsToUpdate[j-1], tt.podsToUpdate[j]); err != nil {
					t.Fatalf("UpdatePod failed: %v", err)
				}
				// check after expiration. confirmed pods shouldn't be expired.
				n := cache.nodeInfoMap[nodeName]
				if err := deepEqualWithoutGeneration(n, tt.wNodeInfo[j-1]); err != nil {
					t.Errorf("update %d: %v", j, err)
				}
			}
		})
	}
}

// TestUpdateAssumedPod tests that a pod will be updated if added before.
func TestUpdateAssumedPod(t *testing.T) {
	nodeName := "node"
	ttl := 10 * time.Second
	testDispatchedPod := makeBasePod(t, nodeName, "test", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}, podutil.GuaranteedPod).DeepCopy()
	testDispatchedPod.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodDispatched)
	testDispatchedPod.Annotations[podutil.PodLauncherAnnotationKey] = string(podutil.Kubelet)
	testAssumedPod := testDispatchedPod.DeepCopy()
	testAssumedPod.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodAssumed)

	tests := []struct {
		podToAdd    *v1.Pod
		podToUpdate *v1.Pod
		wNodeInfo   framework.NodeInfo
	}{
		{ // add a pod and then update it twice
			podToAdd:    testDispatchedPod,
			podToUpdate: testAssumedPod,
			wNodeInfo: newNodeInfo(
				&framework.Resource{
					MilliCPU: 100,
					Memory:   500,
				},
				&framework.Resource{
					MilliCPU: 100,
					Memory:   500,
				},
				&framework.Resource{},
				&framework.Resource{},
				[]*v1.Pod{testAssumedPod},
				newHostPortInfoBuilder().add("TCP", "127.0.0.1", 8080).build(),
				make(map[string]*framework.ImageStateSummary),
			),
		},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			cache := newBinderCache(ttl, time.Second, nil, "")
			if err := cache.AddPod(tt.podToAdd); err != nil {
				t.Fatalf("AddPod failed: %v", err)
			}

			if err := cache.UpdatePod(tt.podToAdd, tt.podToUpdate); err != nil {
				t.Fatalf("UpdatePod failed: %v", err)
			}
			// check after expiration. confirmed pods shouldn't be expired.
			n := cache.nodeInfoMap[nodeName]
			if err := deepEqualWithoutGeneration(n, tt.wNodeInfo); err != nil {
				t.Errorf("update: %v", err)
			}
		})
	}
}

// TestUpdatePodAndGet tests get always return latest pod state
func TestUpdatePodAndGet(t *testing.T) {
	nodeName := "node"
	ttl := 10 * time.Second
	testPods := []*v1.Pod{
		makeBasePod(t, nodeName, "test", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.GuaranteedPod),
		makeBasePod(t, nodeName, "test", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}, podutil.BestEffortPod),
	}
	tests := []struct {
		pod *v1.Pod

		podToUpdate *v1.Pod
		handler     func(cache BinderCache, pod *v1.Pod) error

		assumePod bool
	}{
		{
			pod: testPods[0],

			podToUpdate: testPods[0],
			handler: func(cache BinderCache, pod *v1.Pod) error {
				return cache.AssumePod(pod)
			},
			assumePod: true,
		},
		{
			pod: testPods[0],

			podToUpdate: testPods[1],
			handler: func(cache BinderCache, pod *v1.Pod) error {
				return cache.AddPod(pod)
			},
			assumePod: false,
		},
	}

	for _, tt := range tests {
		cache := newBinderCache(ttl, time.Second, nil, "")

		if err := tt.handler(cache, tt.pod); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}

		if !tt.assumePod {
			if err := cache.UpdatePod(tt.pod, tt.podToUpdate); err != nil {
				t.Fatalf("UpdatePod failed: %v", err)
			}
		}

		cachedPod, err := cache.GetPod(tt.pod)
		if err != nil {
			t.Fatalf("GetPod failed: %v", err)
		}
		if !reflect.DeepEqual(tt.podToUpdate, cachedPod) {
			t.Fatalf("pod get=%s, want=%s", cachedPod, tt.podToUpdate)
		}
	}
}

// TestExpireAddUpdatePod test the sequence that a pod is expired, added, then updated
func TestExpireAddUpdatePod(t *testing.T) {
	nodeName := "node"
	ttl := 10 * time.Second
	testPods := []*v1.Pod{
		makeBasePod(t, nodeName, "test", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.GuaranteedPod),
		makeBasePod(t, nodeName, "test", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}, podutil.BestEffortPod),
	}
	tests := []struct {
		podsToAssume []*v1.Pod
		podsToAdd    []*v1.Pod
		podsToUpdate []*v1.Pod

		wNodeInfo []framework.NodeInfo
	}{{ // Pod is assumed, expired, and added. Then it would be updated twice.
		podsToAssume: []*v1.Pod{testPods[0]},
		podsToAdd:    []*v1.Pod{testPods[0]},
		podsToUpdate: []*v1.Pod{testPods[0], testPods[1], testPods[0]},
		wNodeInfo: []framework.NodeInfo{newNodeInfo(
			&framework.Resource{},
			&framework.Resource{},
			&framework.Resource{
				MilliCPU: 200,
				Memory:   1024,
			},
			&framework.Resource{
				MilliCPU: 200,
				Memory:   1024,
			},
			[]*v1.Pod{testPods[1]},
			newHostPortInfoBuilder().add("TCP", "127.0.0.1", 8080).build(),
			make(map[string]*framework.ImageStateSummary),
		), newNodeInfo(
			&framework.Resource{
				MilliCPU: 100,
				Memory:   500,
			},
			&framework.Resource{
				MilliCPU: 100,
				Memory:   500,
			},
			&framework.Resource{},
			&framework.Resource{},
			[]*v1.Pod{testPods[0]},
			newHostPortInfoBuilder().add("TCP", "127.0.0.1", 80).build(),
			make(map[string]*framework.ImageStateSummary),
		)},
	}}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			now := time.Now()
			cache := newBinderCache(ttl, time.Second, nil, "")
			for _, podToAssume := range tt.podsToAssume {
				if err := assumeAndFinishBinding(cache, podToAssume, now); err != nil {
					t.Fatalf("assumePod failed: %v", err)
				}
			}
			cache.cleanupAssumedPods(now.Add(2 * ttl))

			for _, podToAdd := range tt.podsToAdd {
				if err := cache.AddPod(podToAdd); err != nil {
					t.Fatalf("AddPod failed: %v", err)
				}
			}

			for j := range tt.podsToUpdate {
				if j == 0 {
					continue
				}
				if err := cache.UpdatePod(tt.podsToUpdate[j-1], tt.podsToUpdate[j]); err != nil {
					t.Fatalf("UpdatePod failed: %v", err)
				}
				// check after expiration. confirmed pods shouldn't be expired.
				n := cache.nodeInfoMap[nodeName]
				if err := deepEqualWithoutGeneration(n, tt.wNodeInfo[j-1]); err != nil {
					t.Errorf("update %d: %v", j, err)
				}
			}
		})
	}
}

func makePodWithEphemeralStorage(nodeName, ephemeralStorage string) *v1.Pod {
	req := v1.ResourceList{
		v1.ResourceEphemeralStorage: resource.MustParse(ephemeralStorage),
	}
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default-namespace",
			Name:      "pod-with-ephemeral-storage",
			UID:       types.UID("pod-with-ephemeral-storage"),
			Annotations: map[string]string{
				string(podutil.PodResourceTypeAnnotationKey): string(podutil.GuaranteedPod),
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{{
				Resources: v1.ResourceRequirements{
					Requests: req,
				},
			}},
			NodeName: nodeName,
		},
	}
}

func TestEphemeralStorageResource(t *testing.T) {
	nodeName := "node"
	podE := makePodWithEphemeralStorage(nodeName, "500")
	tests := []struct {
		pod       *v1.Pod
		wNodeInfo framework.NodeInfo
	}{
		{
			pod: podE,
			wNodeInfo: newNodeInfo(
				&framework.Resource{
					EphemeralStorage: 500,
				},
				&framework.Resource{
					MilliCPU: util.DefaultMilliCPURequest,
					Memory:   util.DefaultMemoryRequest,
				},
				&framework.Resource{},
				&framework.Resource{},
				[]*v1.Pod{podE},
				framework.HostPortInfo{},
				make(map[string]*framework.ImageStateSummary),
			),
		},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			cache := newBinderCache(time.Second, time.Second, nil, "")
			if err := cache.AddPod(tt.pod); err != nil {
				t.Fatalf("AddPod failed: %v", err)
			}
			n := cache.nodeInfoMap[nodeName]
			if err := deepEqualWithoutGeneration(n, tt.wNodeInfo); err != nil {
				t.Error(err)
			}

			if err := cache.DeletePod(tt.pod); err != nil {
				t.Fatalf("RemovePod failed: %v", err)
			}
			if _, err := cache.GetPod(tt.pod); err == nil {
				t.Errorf("pod was not deleted")
			}
		})
	}
}

// TestRemovePod tests after added pod is removed, its information should also be subtracted.
func TestRemovePod(t *testing.T) {
	basePod := makeBasePod(t, "node-1", "test", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.GuaranteedPod)
	tests := []struct {
		nodes     []*v1.Node
		pod       *v1.Pod
		wNodeInfo framework.NodeInfo
	}{{
		nodes: []*v1.Node{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
			},
		},
		pod: basePod,
		wNodeInfo: newNodeInfo(
			&framework.Resource{
				MilliCPU: 100,
				Memory:   500,
			},
			&framework.Resource{
				MilliCPU: 100,
				Memory:   500,
			},
			&framework.Resource{},
			&framework.Resource{},
			[]*v1.Pod{basePod},
			newHostPortInfoBuilder().add("TCP", "127.0.0.1", 80).build(),
			make(map[string]*framework.ImageStateSummary),
		),
	}}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			nodeName := tt.pod.Spec.NodeName
			cache := newBinderCache(time.Second, time.Second, nil, "")
			// Add pod succeeds even before adding the nodes.
			if err := cache.AddPod(tt.pod); err != nil {
				t.Fatalf("AddPod failed: %v", err)
			}
			n := cache.nodeInfoMap[nodeName]
			if err := deepEqualWithoutGeneration(n, tt.wNodeInfo); err != nil {
				t.Error(err)
			}
			for _, n := range tt.nodes {
				if err := cache.AddNode(n); err != nil {
					t.Error(err)
				}
			}

			if err := cache.DeletePod(tt.pod); err != nil {
				t.Fatalf("RemovePod failed: %v", err)
			}

			if _, err := cache.GetPod(tt.pod); err == nil {
				t.Errorf("pod was not deleted")
			}
		})
	}
}

func TestForgetPod(t *testing.T) {
	nodeName := "node"
	basePod := makeBasePod(t, nodeName, "test", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.GuaranteedPod)
	pods := []*v1.Pod{basePod}
	now := time.Now()
	ttl := 10 * time.Second

	cache := newBinderCache(ttl, time.Second, nil, "")
	for _, pod := range pods {
		if err := assumeAndFinishBinding(cache, pod, now); err != nil {
			t.Fatalf("assumePod failed: %v", err)
		}
		isAssumed, err := cache.IsAssumedPod(pod)
		if err != nil {
			t.Fatalf("IsAssumedPod failed: %v.", err)
		}
		if !isAssumed {
			t.Fatalf("Pod is expected to be assumed.")
		}
		assumedPod, err := cache.GetPod(pod)
		if err != nil {
			t.Fatalf("GetPod failed: %v.", err)
		}
		if assumedPod.Namespace != pod.Namespace {
			t.Errorf("assumedPod.Namespace != pod.Namespace (%s != %s)", assumedPod.Namespace, pod.Namespace)
		}
		if assumedPod.Name != pod.Name {
			t.Errorf("assumedPod.Name != pod.Name (%s != %s)", assumedPod.Name, pod.Name)
		}
	}
	for _, pod := range pods {
		if err := cache.ForgetPod(pod); err != nil {
			t.Fatalf("ForgetPod failed: %v", err)
		}
		if err := isForgottenFromCache(pod, cache); err != nil {
			t.Errorf("pod %q: %v", pod.Name, err)
		}
	}
}

// buildNodeInfo creates a NodeInfo by simulating node operations in cache.
func buildNodeInfo(node *v1.Node, pods []*v1.Pod) framework.NodeInfo {
	expected := framework.NewNodeInfo()
	expected.SetNode(node)
	expected.SetGuaranteedAllocatable(framework.NewResource(node.Status.Allocatable))
	expected.SetGeneration(expected.GetGeneration() + 1)
	for _, pod := range pods {
		expected.AddPod(pod)
	}
	return expected
}

// TestNodeOperators tests node operations of cache, including add, update
// and remove.
func TestNodeOperators(t *testing.T) {
	// Test datas
	nodeName := "test-node"
	cpu1 := resource.MustParse("1000m")
	mem100m := resource.MustParse("100m")
	cpuHalf := resource.MustParse("500m")
	mem50m := resource.MustParse("50m")
	resourceFooName := "example.com/foo"
	resourceFoo := resource.MustParse("1")

	tests := []struct {
		node *v1.Node
		pods []*v1.Pod
	}{
		{
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Status: v1.NodeStatus{
					Allocatable: v1.ResourceList{
						v1.ResourceCPU:                   cpu1,
						v1.ResourceMemory:                mem100m,
						v1.ResourceName(resourceFooName): resourceFoo,
					},
				},
				Spec: v1.NodeSpec{
					Taints: []v1.Taint{
						{
							Key:    "test-key",
							Value:  "test-value",
							Effect: v1.TaintEffectPreferNoSchedule,
						},
					},
				},
			},
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pod1",
						UID:  types.UID("pod1"),
					},
					Spec: v1.PodSpec{
						NodeName: nodeName,
						Containers: []v1.Container{
							{
								Resources: v1.ResourceRequirements{
									Requests: v1.ResourceList{
										v1.ResourceCPU:    cpuHalf,
										v1.ResourceMemory: mem50m,
									},
								},
								Ports: []v1.ContainerPort{
									{
										Name:          "http",
										HostPort:      80,
										ContainerPort: 80,
									},
								},
							},
						},
					},
				},
			},
		},
		{
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Status: v1.NodeStatus{
					Allocatable: v1.ResourceList{
						v1.ResourceCPU:                   cpu1,
						v1.ResourceMemory:                mem100m,
						v1.ResourceName(resourceFooName): resourceFoo,
					},
				},
				Spec: v1.NodeSpec{
					Taints: []v1.Taint{
						{
							Key:    "test-key",
							Value:  "test-value",
							Effect: v1.TaintEffectPreferNoSchedule,
						},
					},
				},
			},
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pod1",
						UID:  types.UID("pod1"),
					},
					Spec: v1.PodSpec{
						NodeName: nodeName,
						Containers: []v1.Container{
							{
								Resources: v1.ResourceRequirements{
									Requests: v1.ResourceList{
										v1.ResourceCPU:    cpuHalf,
										v1.ResourceMemory: mem50m,
									},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pod2",
						UID:  types.UID("pod2"),
					},
					Spec: v1.PodSpec{
						NodeName: nodeName,
						Containers: []v1.Container{
							{
								Resources: v1.ResourceRequirements{
									Requests: v1.ResourceList{
										v1.ResourceCPU:    cpuHalf,
										v1.ResourceMemory: mem50m,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			expected := buildNodeInfo(test.node, test.pods)
			node := test.node

			cache := newBinderCache(time.Second, time.Second, nil, "")
			if err := cache.AddNode(node); err != nil {
				t.Fatal(err)
			}
			for _, pod := range test.pods {
				if err := cache.AddPod(pod); err != nil {
					t.Fatal(err)
				}
			}

			// the node was added into cache successfully.
			_, found := cache.nodeInfoMap[node.Name]
			if !found {
				t.Errorf("Failed to find node %v in internalcache.", node.Name)
			}

			// update node attribute successfully.
			node.Status.Allocatable[v1.ResourceMemory] = mem50m
			expected.GetGuaranteedAllocatable().Memory = mem50m.Value()

			if err := cache.UpdateNode(nil, node); err != nil {
				t.Error(err)
			}
			_, found = cache.nodeInfoMap[node.Name]
			if !found {
				t.Errorf("Failed to find node %v in schedulertypes after UpdateNode.", node.Name)
			}

			// the node can be removed even if it still has pods.
			if err := cache.DeleteNode(node); err != nil {
				t.Error(err)
			}
			if n, err := cache.GetNode(node.Name); err != nil {
				t.Errorf("The node %v should still have a ghost entry: %v", node.Name, err)
			} else if n == nil {
				t.Errorf("The node object for %v should not be nil", node.Name)
			}

			// Pods are still in the pods cache.
			for _, p := range test.pods {
				if _, err := cache.GetPod(p); err != nil {
					t.Error(err)
				}
			}

			// removing pods for the removed node still succeeds.
			for _, p := range test.pods {
				if err := cache.DeletePod(p); err != nil {
					t.Error(err)
				}
				if _, err := cache.GetPod(p); err == nil {
					t.Errorf("pod %q still in cache", p.Name)
				}
			}
		})
	}
}

func BenchmarkExpirePods(b *testing.B) {
	podNums := []int{
		100,
		1000,
		10000,
	}
	for _, podNum := range podNums {
		name := fmt.Sprintf("%dPods", podNum)
		b.Run(name, func(b *testing.B) {
			benchmarkExpire(b, podNum)
		})
	}
}

func benchmarkExpire(b *testing.B, podNum int) {
	now := time.Now()
	for n := 0; n < b.N; n++ {
		b.StopTimer()
		cache := setupCacheWithAssumedPods(b, podNum, now)
		b.StartTimer()
		cache.cleanupAssumedPods(now.Add(2 * time.Second))
	}
}

type testingMode interface {
	Fatalf(format string, args ...interface{})
}

func makeBasePod(t testingMode, nodeName, objName, cpu, mem, extended string, ports []v1.ContainerPort, resourceType podutil.PodResourceType) *v1.Pod {
	req := v1.ResourceList{}
	if cpu != "" {
		req = v1.ResourceList{
			v1.ResourceCPU:    resource.MustParse(cpu),
			v1.ResourceMemory: resource.MustParse(mem),
		}
		if extended != "" {
			parts := strings.Split(extended, ":")
			if len(parts) != 2 {
				t.Fatalf("Invalid extended resource string: \"%s\"", extended)
			}
			req[v1.ResourceName(parts[0])] = resource.MustParse(parts[1])
		}
	}
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       types.UID(objName),
			Namespace: "node_info_cache_test",
			Name:      objName,
			Annotations: map[string]string{
				podutil.PodResourceTypeAnnotationKey: string(resourceType),
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{{
				Resources: v1.ResourceRequirements{
					Requests: req,
				},
				Ports: ports,
			}},
			NodeName: nodeName,
		},
	}
}

func setupCacheOf1kNodes30kPods(b *testing.B) BinderCache {
	cache := newBinderCache(time.Second, time.Second, nil, "")
	for i := 0; i < 1000; i++ {
		nodeName := fmt.Sprintf("node-%d", i)
		for j := 0; j < 30; j++ {
			objName := fmt.Sprintf("%s-pod-%d", nodeName, j)
			pod := makeBasePod(b, nodeName, objName, "0", "0", "", nil, podutil.GuaranteedPod)

			if err := cache.AddPod(pod); err != nil {
				b.Fatalf("AddPod failed: %v", err)
			}
		}
	}
	return cache
}

func setupCacheWithAssumedPods(b *testing.B, podNum int, assumedTime time.Time) *binderCache {
	cache := newBinderCache(time.Second, time.Second, nil, "")
	for i := 0; i < podNum; i++ {
		nodeName := fmt.Sprintf("node-%d", i/10)
		objName := fmt.Sprintf("%s-pod-%d", nodeName, i%10)
		pod := makeBasePod(b, nodeName, objName, "0", "0", "", nil, podutil.GuaranteedPod)

		err := assumeAndFinishBinding(cache, pod, assumedTime)
		if err != nil {
			b.Fatalf("assumePod failed: %v", err)
		}
	}
	return cache
}

func isForgottenFromCache(p *v1.Pod, c *binderCache) error {
	if assumed, err := c.IsAssumedPod(p); err != nil {
		return err
	} else if assumed {
		return errors.New("still assumed")
	}
	if _, err := c.GetPod(p); err == nil {
		return errors.New("still in cache")
	}
	return nil
}

// getNodeInfo returns cached data for the node name.
func (cache *binderCache) getNodeInfo(nodeName string) (*v1.Node, error) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()

	n, ok := cache.nodeInfoMap[nodeName]
	if !ok {
		return nil, fmt.Errorf("node %q not found in cache", nodeName)
	}

	return n.GetNode(), nil
}
