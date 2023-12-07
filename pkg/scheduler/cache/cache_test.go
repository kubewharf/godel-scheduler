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
	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	godelfeatures "github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	nodestore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/node_store"
	podstore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/pod_store"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func deepEqualWithoutGeneration(actual framework.NodeInfo, expected framework.NodeInfo) string {
	// Ignore generation field.
	if actual != nil {
		actual.SetGeneration(0)
	}
	if expected != nil {
		expected.SetGeneration(0)
	}
	return cmp.Diff(expected, actual, cmpopts.IgnoreUnexported(framework.NodeInfoImpl{}))
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
			cacheHandler := handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj()
			cache := newSchedulerCache(cacheHandler)
			for _, pod := range tt.pods {
				if err := cache.AssumePod(framework.MakeCachePodInfoWrapper().Pod(pod).Obj()); err != nil {
					t.Fatalf("AssumePod failed: %v", err)
				}
			}
			n := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).GetNodeInfo(nodeName)
			if diff := deepEqualWithoutGeneration(n, tt.wNodeInfo); len(diff) > 0 {
				t.Error(diff)
			}

			for _, pod := range tt.pods {
				if err := cache.ForgetPod(framework.MakeCachePodInfoWrapper().Pod(pod).Obj()); err != nil {
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

func assumeAndFinishReserving(cache *schedulerCache, pod *v1.Pod, assumedTime time.Time) error {
	if err := cache.AssumePod(framework.MakeCachePodInfoWrapper().Pod(pod).Obj()); err != nil {
		return err
	}
	return cache.finishReserving(pod, assumedTime)
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
			cacheHandler := handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(ttl).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj()
			cache := newSchedulerCache(cacheHandler)
			for _, pod := range tt.pods {
				if err := cache.AssumePod(framework.MakeCachePodInfoWrapper().Pod(pod.pod).Obj()); err != nil {
					t.Fatal(err)
				}
				if !pod.finishBind {
					continue
				}
				if err := cache.finishReserving(pod.pod, pod.assumedTime); err != nil {
					t.Fatal(err)
				}
			}
			// pods that got bound and have assumedTime + ttl < cleanupTime will get
			// expired and removed
			cache.storeSwitch.Find(podstore.Name).(*podstore.PodStore).CleanupExpiredAssumedPods(&cache.mu, tt.cleanupTime)
			obj := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(nodeName)
			if obj != nil {
				n := obj.(framework.NodeInfo)
				if diff := deepEqualWithoutGeneration(n, tt.wNodeInfo); len(diff) > 0 {
					t.Error(diff)
				}
			} else {
				if tt.wNodeInfo != nil {
					t.Errorf("node: %v expected to be %v but nil", nodeName, tt.wNodeInfo)
				}
			}
		})
	}
}

func findPodByUID(pods []*v1.Pod, pod *v1.Pod) *v1.Pod {
	for _, p := range pods {
		if p.UID == pod.UID {
			return p
		}
	}
	return nil
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
			cacheHandler := handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj()
			cache := newSchedulerCache(cacheHandler)
			for _, podToAssume := range tt.podsToAssume {
				if err := assumeAndFinishReserving(cache, podToAssume, now); err != nil {
					t.Fatalf("assumePod failed: %v", err)
				}
			}
			cache.handler.SetPodHandler(func(s string) (*framework.CachePodState, bool) {
				for _, p := range tt.podsToAssume {
					if string(p.UID) == s {
						return &framework.CachePodState{Pod: p}, false
					}
				}
				for _, p := range tt.podsToAdd {
					if string(p.UID) == s {
						return &framework.CachePodState{Pod: p}, false
					}
				}
				return nil, false
			})
			for _, podToAdd := range tt.podsToAdd {
				if err := cache.UpdatePod(findPodByUID(tt.podsToAssume, podToAdd), podToAdd); err != nil {
					t.Fatalf("AddPod failed: %v", err)
				}
			}
			cache.storeSwitch.Find(podstore.Name).(*podstore.PodStore).CleanupExpiredAssumedPods(&cache.mu, now.Add(2*ttl))
			// check after expiration. confirmed pods shouldn't be expired.
			n := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(nodeName)
			if diff := deepEqualWithoutGeneration(n, tt.wNodeInfo); len(diff) > 0 {
				t.Error(diff)
			}
		})
	}
}

func TestSnapshot(t *testing.T) {
	nodeName := "node"
	now := time.Now()

	testPods := []*v1.Pod{
		makeBasePod(t, nodeName, "test-1", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.GuaranteedPod),
		makeBasePod(t, nodeName, "test-2", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.BestEffortPod),
	}
	tests := []struct {
		podsToAssume []*v1.Pod
		podsToAdd    []*v1.Pod
	}{{ // two pod were assumed at same time. But first one is called Add() and gets confirmed.
		podsToAssume: []*v1.Pod{testPods[0], testPods[1]},
		podsToAdd:    []*v1.Pod{testPods[0]},
	}}

	for _, tt := range tests {
		cacheHandler := handler.MakeCacheHandlerWrapper().
			SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
			TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
			EnableStore("PreemptionStore").
			Obj()
		cache := newSchedulerCache(cacheHandler)
		for _, podToAssume := range tt.podsToAssume {
			if err := assumeAndFinishReserving(cache, podToAssume, now); err != nil {
				t.Errorf("assumePod failed: %v", err)
			}
		}
		for _, podToAdd := range tt.podsToAdd {
			if err := cache.UpdatePod(findPodByUID(tt.podsToAssume, podToAdd), podToAdd); err != nil {
				t.Errorf("AddPod failed: %v", err)
			}
		}

		snapshot := cache.Dump()
		if len(snapshot.Nodes) != cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len() {
			t.Errorf("Unequal number of nodes in the cache and its snapshot. expected: %v, got: %v", cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len(), len(snapshot.Nodes))
		}
		for name, ni := range snapshot.Nodes {
			obj := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(name)
			nItem := obj.(framework.NodeInfo)
			// if !reflect.DeepEqual(ni, nItem) {
			if !cmp.Equal(ni, nItem, cmpopts.IgnoreUnexported(framework.NodeInfoImpl{})) {
				t.Errorf("expect \n%+v; got \n%+v", nItem, ni)
			}
		}
		if !reflect.DeepEqual(snapshot.AssumedPods, cache.storeSwitch.Find(podstore.Name).(*podstore.PodStore).AssumedPods) {
			t.Errorf("expect \n%+v; got \n%+v", cache.storeSwitch.Find(podstore.Name).(*podstore.PodStore).AssumedPods, snapshot.AssumedPods)
		}
	}
}

// TestAddPodWillReplaceAssumed tests that a pod being Add()ed will replace any assumed pod.
func TestAddPodWillReplaceAssumed(t *testing.T) {
	now := time.Now()

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
			cacheHandler := handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj()
			cache := newSchedulerCache(cacheHandler)
			for _, podToAssume := range tt.podsToAssume {
				if err := assumeAndFinishReserving(cache, podToAssume, now); err != nil {
					t.Fatalf("assumePod failed: %v", err)
				}
			}
			for _, podToAdd := range tt.podsToAdd {
				// After cache refactor, it's UpdatePod instead of AddPod.
				if err := cache.UpdatePod(findPodByUID(tt.podsToAssume, podToAdd), podToAdd); err != nil {
					t.Fatalf("AddPod failed: %v", err)
				}
			}
			for _, podToUpdate := range tt.podsToUpdate {
				if err := cache.UpdatePod(podToUpdate[0], podToUpdate[1]); err != nil {
					t.Fatalf("UpdatePod failed: %v", err)
				}
			}
			for nodeName, expected := range tt.wNodeInfo {
				n := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(nodeName)
				if n != nil {
					if diff := deepEqualWithoutGeneration(n, expected); len(diff) > 0 {
						t.Error(diff)
					}
				} else {
					if expected != nil {
						t.Errorf("node %q: expected to be %v but nil", nodeName, expected)
					}
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
			cacheHandler := handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj()
			cache := newSchedulerCache(cacheHandler)
			if err := assumeAndFinishReserving(cache, tt.pod, now); err != nil {
				t.Fatalf("assumePod failed: %v", err)
			}
			cache.storeSwitch.Find(podstore.Name).(*podstore.PodStore).CleanupExpiredAssumedPods(&cache.mu, now.Add(2*ttl))
			// It should be expired and removed.
			if err := isForgottenFromCache(tt.pod, cache); err != nil {
				t.Error(err)
			}
			if err := cache.UpdatePod(tt.pod, tt.pod); err != nil {
				t.Fatalf("AddPod failed: %v", err)
			}
			// check after expiration. confirmed pods shouldn't be expired.
			n := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(nodeName)
			if diff := deepEqualWithoutGeneration(n, tt.wNodeInfo); len(diff) > 0 {
				t.Error(diff)
			}
		})
	}
}

// TestUpdatePod tests that a pod will be updated if added before.
func TestUpdatePod(t *testing.T) {
	nodeName := "node"
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
			cacheHandler := handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj()
			cache := newSchedulerCache(cacheHandler)
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
				n := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(nodeName)
				if diff := deepEqualWithoutGeneration(n, tt.wNodeInfo[j-1]); len(diff) > 0 {
					t.Errorf("update %d: %v", j, diff)
				}
			}
		})
	}
}

// TestUpdatePodAndGet tests get always return latest pod state
func TestUpdatePodAndGet(t *testing.T) {
	nodeName := "node"
	testPods := []*v1.Pod{
		makeBasePod(t, nodeName, "test", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.GuaranteedPod),
		makeBasePod(t, nodeName, "test", "200m", "1Ki", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 8080, Protocol: "TCP"}}, podutil.BestEffortPod),
	}
	tests := []struct {
		pod *v1.Pod

		podToUpdate *v1.Pod
		handler     func(cache SchedulerCache, pod *v1.Pod) error

		assumePod bool
	}{
		{
			pod: testPods[0],

			podToUpdate: testPods[0],
			handler: func(cache SchedulerCache, pod *v1.Pod) error {
				return cache.AssumePod(framework.MakeCachePodInfoWrapper().Pod(pod).Obj())
			},
			assumePod: true,
		},
		{
			pod: testPods[0],

			podToUpdate: testPods[1],
			handler: func(cache SchedulerCache, pod *v1.Pod) error {
				return cache.AddPod(pod)
			},
			assumePod: false,
		},
	}

	for _, tt := range tests {
		cacheHandler := handler.MakeCacheHandlerWrapper().
			SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
			TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
			EnableStore("PreemptionStore").
			Obj()
		cache := newSchedulerCache(cacheHandler)
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
			cacheHandler := handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj()
			cache := newSchedulerCache(cacheHandler)
			for _, podToAssume := range tt.podsToAssume {
				if err := assumeAndFinishReserving(cache, podToAssume, now); err != nil {
					t.Fatalf("assumePod failed: %v", err)
				}
			}
			cache.storeSwitch.Find(podstore.Name).(*podstore.PodStore).CleanupExpiredAssumedPods(&cache.mu, now.Add(2*ttl))

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
				obj := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(nodeName)
				n := obj.(framework.NodeInfo)
				if diff := deepEqualWithoutGeneration(n, tt.wNodeInfo[j-1]); len(diff) > 0 {
					t.Errorf("update %d: %v", j, diff)
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
			cacheHandler := handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj()
			cache := newSchedulerCache(cacheHandler)
			if err := cache.AddPod(tt.pod); err != nil {
				t.Fatalf("AddPod failed: %v", err)
			}
			obj := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(nodeName)
			n := obj.(framework.NodeInfo)
			if diff := deepEqualWithoutGeneration(n, tt.wNodeInfo); len(diff) > 0 {
				t.Error(diff)
			}

			if err := cache.RemovePod(tt.pod); err != nil {
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
			cacheHandler := handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj()
			cache := newSchedulerCache(cacheHandler)
			// Add pod succeeds even before adding the nodes.
			if err := cache.AddPod(tt.pod); err != nil {
				t.Fatalf("AddPod failed: %v", err)
			}
			n := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(nodeName)
			if diff := deepEqualWithoutGeneration(n, tt.wNodeInfo); len(diff) > 0 {
				t.Error(diff)
			}
			for _, n := range tt.nodes {
				if err := cache.AddNode(n); err != nil {
					t.Error(err)
				}
			}

			if err := cache.RemovePod(tt.pod); err != nil {
				t.Fatalf("RemovePod failed: %v", err)
			}

			if _, err := cache.GetPod(tt.pod); err == nil {
				t.Errorf("pod was not deleted")
			}

			// Node that owned the Pod should be at the head of the list.
			obj := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Store.(generationstore.ListStore).Front().StoredObj
			if obj.(framework.NodeInfo).GetNode().Name != nodeName {
				t.Errorf("node %q is not at the head of the list", nodeName)
			}
		})
	}
}

func TestForgetPod(t *testing.T) {
	nodeName := "node"
	basePod := makeBasePod(t, nodeName, "test", "100m", "500", "", []v1.ContainerPort{{HostIP: "127.0.0.1", HostPort: 80, Protocol: "TCP"}}, podutil.GuaranteedPod)
	pods := []*v1.Pod{basePod}
	now := time.Now()
	cacheHandler := handler.MakeCacheHandlerWrapper().
		SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
		EnableStore("PreemptionStore").
		Obj()
	cache := newSchedulerCache(cacheHandler)
	for _, pod := range pods {
		if err := assumeAndFinishReserving(cache, pod, now); err != nil {
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
		if err := cache.ForgetPod(framework.MakeCachePodInfoWrapper().Pod(pod).Obj()); err != nil {
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

func TestNodeOps(t *testing.T) {
	// Test datas
	nodeName := "test-node"
	nodeName2 := "test-node2"
	nodeName3 := "test-node3"

	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				"topology.kubernetes.io/zone": "lf",
			},
		},
	}
	node2 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName2,
			Labels: map[string]string{
				"topology.kubernetes.io/zone": "lf",
			},
		},
	}
	node3 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName3,
			Labels: map[string]string{
				"topology.kubernetes.io/zone": "lf",
			},
		},
	}
	nmNode := &nodev1alpha1.NMNode{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
	}
	nmNode2 := &nodev1alpha1.NMNode{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName2,
		},
	}

	cacheHandler := handler.MakeCacheHandlerWrapper().
		SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
		EnableStore("PreemptionStore").
		Obj()
	cache := newSchedulerCache(cacheHandler)
	if err := cache.AddNode(node); err != nil {
		t.Fatal(err)
	}
	if err := cache.AddNMNode(nmNode); err != nil {
		t.Fatal(err)
	}
	if err := cache.AddNode(node2); err != nil {
		t.Fatal(err)
	}
	if err := cache.AddNMNode(nmNode2); err != nil {
		t.Fatal(err)
	}

	// Step 1: the node was added into cache successfully. the node was set out of partition of the scheduler by default.
	obj := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(node.Name)
	if obj == nil {
		t.Errorf("Failed to find node %v in internalcache.", node.Name)
	}

	// Step 2: dump cached nodes successfully.
	snapshotHandler := handler.MakeCacheHandlerWrapper().
		SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
		EnableStore("PreemptionStore").
		Obj()
	cachedNodes := NewEmptySnapshot(snapshotHandler)
	if err := cache.UpdateSnapshot(cachedNodes); err != nil {
		t.Error(err)
	}

	// Step 3: remove node.
	cache.RemoveNMNode(nmNode)

	if err := cache.UpdateSnapshot(cachedNodes); err != nil {
		t.Error(err)
	}

	count := cachedNodes.NumNodes()
	if count != 2 {
		t.Errorf("failed to dump cached nodes:\n got: %v \nexpected: %v", count, cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len())
	}
	// Step 4: add node.
	cache.AddNode(node3)
	if err := cache.UpdateSnapshot(cachedNodes); err != nil {
		t.Error(err)
	}
	count = cachedNodes.NumNodes()
	if count != 3 {
		t.Errorf("failed to dump cached nodes:\n got: %v \nexpected: %v", count, cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len())
	}

	// Step 5: remove node.
	cache.RemoveNode(node)
	if err := cache.UpdateSnapshot(cachedNodes); err != nil {
		t.Error(err)
	}

	count = cachedNodes.NumNodes()
	if count != 2 {
		t.Errorf("failed to dump cached nodes:\n got: %v \nexpected: %v", count, cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len())
	}
}

func TestSchedulerCache_UpdateSubClusterSnapshot(t *testing.T) {
	testSubClusterKey := "nodeLevel"
	framework.SetGlobalSubClusterKey(testSubClusterKey)

	alphaSubClusterName := "alpha"
	sigmaSubClusterName := "sigma"

	var cache *schedulerCache
	var snapshots []*Snapshot

	type operation = func()
	addNode := func(node *v1.Node) operation {
		return func() {
			if err := cache.AddNode(node); err != nil {
				t.Error(err)
			}
		}
	}
	removeNode := func(node *v1.Node) operation {
		return func() {
			if err := cache.RemoveNode(node); err != nil {
				t.Error(err)
			}
		}
	}
	updateNode := func(oldNode, newNode *v1.Node) operation {
		return func() {
			if err := cache.UpdateNode(oldNode, newNode); err != nil {
				t.Error(err)
			}
		}
	}
	updateSnapshot := func() operation {
		return func() {
			for i := 0; i < len(snapshots); i++ {
				if err := cache.UpdateSnapshot(snapshots[i]); err != nil {
					t.Error(err)
				}
			}
		}
	}

	tests := []struct {
		name       string
		operations []operation
		expected   []*v1.Node
		subCluster [][]*v1.Node
	}{
		{
			name:       "Empty cache",
			operations: []operation{},
			expected:   []*v1.Node{},
			subCluster: [][]*v1.Node{
				{},
				{},
				{},
			},
		},
		{
			name: "Add nodes with default subcluster and alpha subcluster",
			operations: []operation{
				addNode(testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, framework.DefaultSubCluster).Obj()),
				addNode(testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, alphaSubClusterName).Obj()),
				updateSnapshot(),
			},
			expected: []*v1.Node{
				testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, alphaSubClusterName).Obj(),
				testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, framework.DefaultSubCluster).Obj(),
			},
			subCluster: [][]*v1.Node{
				{
					testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, framework.DefaultSubCluster).Obj(),
					testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, alphaSubClusterName).Obj(),
				},
				{
					testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, alphaSubClusterName).Obj(),
				},
				{},
			},
		},
		{
			name: "Add nodes, remove nodes",
			operations: []operation{
				addNode(testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, framework.DefaultSubCluster).Obj()),
				addNode(testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, alphaSubClusterName).Obj()),
				addNode(testing_helper.MakeNode().Name("n3").Label(testSubClusterKey, sigmaSubClusterName).Obj()),
				updateSnapshot(),
				removeNode(testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, framework.DefaultSubCluster).Obj()),
				removeNode(testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, alphaSubClusterName).Obj()),
				removeNode(testing_helper.MakeNode().Name("n3").Label(testSubClusterKey, sigmaSubClusterName).Obj()),
				updateSnapshot(),
			},
			expected: []*v1.Node{},
			subCluster: [][]*v1.Node{
				{},
				{},
				{},
			},
		},
		{
			name: "Add nodes with default subcluster and alpha subcluster, update to sigma subcluster",
			operations: []operation{
				addNode(testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, framework.DefaultSubCluster).Obj()),
				addNode(testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, alphaSubClusterName).Obj()),
				updateSnapshot(),
				updateNode(
					testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, framework.DefaultSubCluster).Obj(),
					testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
				),
				updateNode(
					testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, alphaSubClusterName).Obj(),
					testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
				),
				updateSnapshot(),
			},
			expected: []*v1.Node{
				testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
				testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
			},
			subCluster: [][]*v1.Node{
				{
					testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
					testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
				},
				{},
				{
					testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
					testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
				},
			},
		},
		{
			name: "Add nodes with default subcluster and alpha subcluster, update to sigma subcluster, then remove them",
			operations: []operation{
				addNode(testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, framework.DefaultSubCluster).Obj()),
				addNode(testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, alphaSubClusterName).Obj()),
				addNode(testing_helper.MakeNode().Name("n3").Label(testSubClusterKey, sigmaSubClusterName).Obj()),
				updateSnapshot(),
				updateNode(
					testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, framework.DefaultSubCluster).Obj(),
					testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
				),
				updateNode(
					testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, alphaSubClusterName).Obj(),
					testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
				),
				updateSnapshot(),
				removeNode(testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, sigmaSubClusterName).Obj()),
				removeNode(testing_helper.MakeNode().Name("n3").Label(testSubClusterKey, sigmaSubClusterName).Obj()),
				updateSnapshot(),
			},
			expected: []*v1.Node{
				testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
			},
			subCluster: [][]*v1.Node{
				{
					testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
				},
				{},
				{
					testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
				},
			},
		},
		{
			name: "Add nodes with default subcluster and alpha subcluster, update to sigma subcluster, then update again",
			operations: []operation{
				addNode(testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, framework.DefaultSubCluster).Obj()),
				addNode(testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, alphaSubClusterName).Obj()),
				addNode(testing_helper.MakeNode().Name("n3").Label(testSubClusterKey, sigmaSubClusterName).Obj()),
				updateSnapshot(),
				updateNode(
					testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, framework.DefaultSubCluster).Obj(),
					testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
				),
				updateNode(
					testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, alphaSubClusterName).Obj(),
					testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
				),
				updateSnapshot(),
				updateNode(
					testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
					testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, framework.DefaultSubCluster).Obj(),
				),
				updateNode(
					testing_helper.MakeNode().Name("n3").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
					testing_helper.MakeNode().Name("n3").Label(testSubClusterKey, framework.DefaultSubCluster).Obj(),
				),
				updateSnapshot(),
			},
			expected: []*v1.Node{
				testing_helper.MakeNode().Name("n3").Label(testSubClusterKey, framework.DefaultSubCluster).Obj(),
				testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, framework.DefaultSubCluster).Obj(),
				testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
			},
			subCluster: [][]*v1.Node{
				{
					testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
					testing_helper.MakeNode().Name("n2").Label(testSubClusterKey, framework.DefaultSubCluster).Obj(),
					testing_helper.MakeNode().Name("n3").Label(testSubClusterKey, framework.DefaultSubCluster).Obj(),
				},
				{},
				{
					testing_helper.MakeNode().Name("n1").Label(testSubClusterKey, sigmaSubClusterName).Obj(),
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cacheHandler := handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				Obj()
			cache = newSchedulerCache(cacheHandler)
			snapshots = []*Snapshot{
				NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
					SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
					Obj()),
				NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
					SubCluster(alphaSubClusterName).SwitchType(framework.DefaultSubClusterSwitchType).
					Obj()),
				NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
					SubCluster(sigmaSubClusterName).SwitchType(framework.DefaultSubClusterSwitchType).
					Obj()),
			}

			for _, op := range test.operations {
				op()
			}

			if len(test.expected) != cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len() {
				t.Errorf("unexpected number of nodes. Expected: %v, got: %v", len(test.expected), cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len())
			}
			var i int
			// Check that cache is in the expected state.
			for e := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Store.(generationstore.ListStore).Front(); e != nil; e = e.Next() {
				nodeInfo := e.StoredObj.(framework.NodeInfo)
				if nodeInfo.GetNode() != nil && nodeInfo.GetNode().Name != test.expected[i].Name {
					t.Errorf("unexpected node. Expected: %v, got: %v, index: %v", test.expected[i].Name, nodeInfo.GetNode().Name, i)
				}
				i++
			}
			// Make sure we visited all the cached nodes in the above for loop.
			if i != cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len() {
				t.Errorf("Not all the nodes were visited by following the NodeInfo linked list. Expected to see %v nodes, saw %v.", cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len(), i)
			}

			// Update snapshot and check nodes.
			for i := 0; i < len(snapshots); i++ {
				snapshot := snapshots[i]
				if snapshot.nodeSlices.inPartitionNodeSlice.Len() > 0 {
					t.Errorf("unexpected inPartitionNodeSlice length. expect: %v, got: %v", 0, snapshot.nodeSlices.inPartitionNodeSlice.Len())
				}
				if snapshot.nodeSlices.outOfPartitionNodeSlice.Len() != len(test.subCluster[i]) {
					t.Errorf("unexpected subcluster snapshot nodeslice num. expect: %v, got: %v", len(test.subCluster[i]), snapshot.nodeSlices.outOfPartitionNodeSlice.Len())
				}
				for j := 0; j < len(test.subCluster[i]); j++ {
					expect := test.subCluster[i][j]
					got := snapshot.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(expect.Name).GetNode()
					if diff := cmp.Diff(expect, got); len(diff) > 0 {
						t.Errorf("unexpected node. diff: %v index: %v/%v", diff, i, j)
					}
				}
				snapshot.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Store.Range(func(s string, so generationstore.StoredObj) {
					got := so.(framework.NodeInfo)
					shouldBePlaceHolder := true
					for j := 0; j < len(test.subCluster[i]); j++ {
						expect := test.subCluster[i][j]
						if expect.Name == s {
							shouldBePlaceHolder = false
							break
						}
					}
					if shouldBePlaceHolder {
						if !reflect.DeepEqual(nodestore.GlobalNodeInfoPlaceHolder, got) {
							t.Errorf("unexpected node, expect: %v, got %v.", nodestore.GlobalNodeInfoPlaceHolder, got)
						}
						// if diff := cmp.Diff(globalNodeInfoPlaceHolder, got); len(diff) > 0 {
						// 	t.Errorf("unexpected node, should be PlaceHolder. diff: %v", diff)
						// }
					}
				})
			}
		})
	}
}

func TestSchedulerCache_UpdateSnapshot(t *testing.T) {
	// Create a few nodes to be used in tests.
	nodes := []*v1.Node{}
	for i := 0; i < 10; i++ {
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("test-node%v", i),
			},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU:    resource.MustParse("1000m"),
					v1.ResourceMemory: resource.MustParse("100m"),
				},
			},
		}
		nodes = append(nodes, node)
	}
	// Create a few nodes as updated versions of the above nodes
	updatedNodes := []*v1.Node{}
	for _, n := range nodes {
		updatedNode := n.DeepCopy()
		updatedNode.Status.Allocatable = v1.ResourceList{
			v1.ResourceCPU:    resource.MustParse("2000m"),
			v1.ResourceMemory: resource.MustParse("500m"),
		}
		updatedNodes = append(updatedNodes, updatedNode)
	}

	// Create a few pods for tests.
	pods := []*v1.Pod{}
	for i := 0; i < 20; i++ {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("test-pod%v", i),
				Namespace: "test-ns",
				UID:       types.UID(fmt.Sprintf("test-puid%v", i)),
				Annotations: map[string]string{
					string(podutil.PodResourceTypeAnnotationKey): string(podutil.GuaranteedPod),
				},
			},
			Spec: v1.PodSpec{
				NodeName: fmt.Sprintf("test-node%v", i%10),
			},
		}
		pods = append(pods, pod)
	}

	// Create a few pods as updated versions of the above pods.
	updatedPods := []*v1.Pod{}
	for _, p := range pods {
		updatedPod := p.DeepCopy()
		priority := int32(1000)
		updatedPod.Spec.Priority = &priority
		updatedPods = append(updatedPods, updatedPod)
	}

	// Add a couple of pods with affinity, on the first and seconds nodes.
	podsWithAffinity := []*v1.Pod{}
	for i := 0; i < 2; i++ {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("test-pod%v", i),
				Namespace: "test-ns",
				UID:       types.UID(fmt.Sprintf("test-puid%v", i)),
				Annotations: map[string]string{
					string(podutil.PodResourceTypeAnnotationKey): string(podutil.GuaranteedPod),
				},
			},
			Spec: v1.PodSpec{
				NodeName: fmt.Sprintf("test-node%v", i),
				Affinity: &v1.Affinity{
					PodAffinity: &v1.PodAffinity{},
				},
			},
		}
		podsWithAffinity = append(podsWithAffinity, pod)
	}

	podWithVictims := []*v1.Pod{}
	for i := 0; i < 1; i++ {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("test-pod%v", i),
				Namespace: "test-ns",
				UID:       types.UID(fmt.Sprintf("test-puid%v", i)),
				Annotations: map[string]string{
					string(podutil.PodResourceTypeAnnotationKey): string(podutil.GuaranteedPod),
					podutil.NominatedNodeAnnotationKey:           fmt.Sprintf("{\"node\":\"test-node%v\",\"victims\":[{\"name\":\"p1\",\"namespace\":\"p1\",\"uid\":\"p1\"}]}", i),
				},
			},
		}
		podWithVictims = append(podWithVictims, pod)
	}

	var cache *schedulerCache
	var snapshot *Snapshot
	type operation = func()

	addNode := func(i int) operation {
		return func() {
			if err := cache.AddNode(nodes[i]); err != nil {
				t.Error(err)
			}
		}
	}
	removeNode := func(i int) operation {
		return func() {
			if err := cache.RemoveNode(nodes[i]); err != nil {
				t.Error(err)
			}
		}
	}
	updateNode := func(i int) operation {
		return func() {
			if err := cache.UpdateNode(nodes[i], updatedNodes[i]); err != nil {
				t.Error(err)
			}
		}
	}
	addPod := func(i int) operation {
		return func() {
			if err := cache.AddPod(pods[i]); err != nil {
				t.Error(err)
			}
		}
	}
	addPodWithAffinity := func(i int) operation {
		return func() {
			if err := cache.AddPod(podsWithAffinity[i]); err != nil {
				t.Error(err)
			}
		}
	}
	removePod := func(i int) operation {
		return func() {
			if err := cache.RemovePod(pods[i]); err != nil {
				t.Error(err)
			}
		}
	}
	removePodWithAffinity := func(i int) operation {
		return func() {
			if err := cache.RemovePod(podsWithAffinity[i]); err != nil {
				t.Error(err)
			}
		}
	}
	addPodWithVictims := func(i int) operation {
		return func() {
			if err := cache.AddPod(podWithVictims[i]); err != nil {
				t.Error(err)
			}
		}
	}
	updatePod := func(i int) operation {
		return func() {
			if err := cache.UpdatePod(pods[i], updatedPods[i]); err != nil {
				t.Error(err)
			}
		}
	}
	updateSnapshot := func() operation {
		return func() {
			cache.UpdateSnapshot(snapshot)
			if err := compareCacheWithNodeInfoSnapshot(cache, snapshot); err != nil {
				t.Error(err)
			}
		}
	}

	tests := []struct {
		name                         string
		operations                   []operation
		expected                     []*v1.Node
		expectedHavePodsWithAffinity int
	}{
		{
			name:       "Empty cache",
			operations: []operation{},
			expected:   []*v1.Node{},
		},
		{
			name:       "Single node",
			operations: []operation{addNode(1)},
			expected:   []*v1.Node{nodes[1]},
		},
		{
			name: "Add node, remove it, add it again",
			operations: []operation{
				addNode(1), updateSnapshot(), removeNode(1), addNode(1),
			},
			expected: []*v1.Node{nodes[1]},
		},
		{
			name: "Add node and remove it in the same cycle, add it again",
			operations: []operation{
				addNode(1), updateSnapshot(), addNode(2), removeNode(1),
			},
			expected: []*v1.Node{nodes[2]},
		},
		{
			name: "Add a few nodes, and snapshot in the middle",
			operations: []operation{
				addNode(0), updateSnapshot(), addNode(1), updateSnapshot(), addNode(2),
				updateSnapshot(), addNode(3),
			},
			expected: []*v1.Node{nodes[3], nodes[2], nodes[1], nodes[0]},
		},
		{
			name: "Add a few nodes, and snapshot in the end",
			operations: []operation{
				addNode(0), addNode(2), addNode(5), addNode(6),
			},
			expected: []*v1.Node{nodes[6], nodes[5], nodes[2], nodes[0]},
		},
		{
			name: "Update some nodes",
			operations: []operation{
				addNode(0), addNode(1), addNode(5), updateSnapshot(), updateNode(1),
			},
			expected: []*v1.Node{nodes[1], nodes[5], nodes[0]},
		},
		{
			name: "Add a few nodes, and remove all of them",
			operations: []operation{
				addNode(0), addNode(2), addNode(5), addNode(6), updateSnapshot(),
				removeNode(0), removeNode(2), removeNode(5), removeNode(6),
			},
			expected: []*v1.Node{},
		},
		{
			name: "Add a few nodes, and remove some of them",
			operations: []operation{
				addNode(0), addNode(2), addNode(5), addNode(6), updateSnapshot(),
				removeNode(0), removeNode(6),
			},
			expected: []*v1.Node{nodes[5], nodes[2]},
		},
		{
			name: "Add a few nodes, remove all of them, and add more",
			operations: []operation{
				addNode(2), addNode(5), addNode(6), updateSnapshot(),
				removeNode(2), removeNode(5), removeNode(6), updateSnapshot(),
				addNode(7), addNode(9),
			},
			expected: []*v1.Node{nodes[9], nodes[7]},
		},
		{
			name: "Update nodes in particular order",
			operations: []operation{
				addNode(8), updateNode(2), updateNode(8), updateSnapshot(),
				addNode(1),
			},
			expected: []*v1.Node{nodes[1], nodes[8], nodes[2]},
		},
		{
			name: "Add some nodes and some pods",
			operations: []operation{
				addNode(0), addNode(2), addNode(8), updateSnapshot(),
				addPod(8), addPod(2),
			},
			expected: []*v1.Node{nodes[2], nodes[8], nodes[0]},
		},
		{
			name: "Updating a pod moves its node to the head",
			operations: []operation{
				addNode(0), addPod(0), addNode(2), addNode(4), updatePod(0),
			},
			expected: []*v1.Node{nodes[0], nodes[4], nodes[2]},
		},
		{
			name: "Add pod before its node",
			operations: []operation{
				addNode(0), addPod(1), updatePod(1), addNode(1),
			},
			expected: []*v1.Node{nodes[1], nodes[0]},
		},
		{
			name: "Remove node before its pods",
			operations: []operation{
				addNode(0), addNode(1), addPod(1), addPod(11),
				removeNode(1), updatePod(1), updatePod(11), removePod(1), removePod(11),
			},
			expected: []*v1.Node{nodes[0]},
		},
		{
			name: "Add Pods with affinity",
			operations: []operation{
				addNode(0), addPodWithAffinity(0), updateSnapshot(), addNode(1),
			},
			expected:                     []*v1.Node{nodes[1], nodes[0]},
			expectedHavePodsWithAffinity: 1,
		},
		{
			name: "Add multiple nodes with pods with affinity",
			operations: []operation{
				addNode(0), addPodWithAffinity(0), updateSnapshot(), addNode(1), addPodWithAffinity(1), updateSnapshot(),
			},
			expected:                     []*v1.Node{nodes[1], nodes[0]},
			expectedHavePodsWithAffinity: 2,
		},
		{
			name: "Add then Remove pods with affinity",
			operations: []operation{
				addNode(0), addNode(1), addPodWithAffinity(0), updateSnapshot(), removePodWithAffinity(0), updateSnapshot(),
			},
			expected:                     []*v1.Node{nodes[0], nodes[1]},
			expectedHavePodsWithAffinity: 0,
		},
		{
			name:                         "Add pod with victims",
			operations:                   []operation{addNode(0), addPodWithVictims(0)},
			expected:                     []*v1.Node{nodes[0]},
			expectedHavePodsWithAffinity: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cacheHandler := handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj()
			snapshotHandler := handler.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				EnableStore("PreemptionStore").
				Obj()
			cache = newSchedulerCache(cacheHandler)
			snapshot = NewEmptySnapshot(snapshotHandler)

			for _, op := range test.operations {
				op()
			}

			if len(test.expected) != cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len() {
				t.Errorf("unexpected number of nodes. Expected: %v, got: %v", len(test.expected), cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len())
			}
			var i int
			// Check that cache is in the expected state.
			for e := cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Store.(generationstore.ListStore).Front(); e != nil; e = e.Next() {
				nodeInfo := e.StoredObj.(framework.NodeInfo)
				if nodeInfo.GetNode() != nil && nodeInfo.GetNode().Name != test.expected[i].Name {
					t.Errorf("unexpected node. Expected: %v, got: %v, index: %v", test.expected[i].Name, nodeInfo.GetNode().Name, i)
				}
				i++
			}
			// Make sure we visited all the cached nodes in the above for loop.
			if i != cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len() {
				t.Errorf("Not all the nodes were visited by following the NodeInfo linked list. Expected to see %v nodes, saw %v.", cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len(), i)
			}

			// Check number of nodes with pods with affinity
			if snapshot.nodeSlices.havePodsWithAffinityNodeSlice.Len() != test.expectedHavePodsWithAffinity {
				t.Errorf("unexpected number of HavePodsWithAffinity nodes. Expected: %v, got: %v", test.expectedHavePodsWithAffinity, snapshot.nodeSlices.havePodsWithAffinityNodeSlice.Len())
			}

			// Always update the snapshot at the end of operations and compare it.
			if err := cache.UpdateSnapshot(snapshot); err != nil {
				t.Error(err)
			}
			if err := compareCacheWithNodeInfoSnapshot(cache, snapshot); err != nil {
				t.Error(err)
			}
		})
	}
}

func compareCacheWithNodeInfoSnapshot(cache *schedulerCache, snapshot *Snapshot) error {
	// Compare the map.
	if snapshot.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len() != cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len() {
		return fmt.Errorf("unexpected number of nodes in the snapshot. Expected: %v, got: %v", cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len(), snapshot.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len())
	}
	for key, nodeInfo := range cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).AllNodesClone() {
		// if !reflect.DeepEqual(snapshot.nodeStore.Get(key), nodeInfo) {
		if !cmp.Equal(snapshot.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(key), nodeInfo, cmpopts.IgnoreUnexported(framework.NodeInfoImpl{})) {
			return fmt.Errorf("unexpected node info for node %q. Expected: %+v, got: %+v", key, nodeInfo, snapshot.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(key))
		}
	}

	// Compare the lists.
	if snapshot.nodeSlices.outOfPartitionNodeSlice.Len() != cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len() {
		return fmt.Errorf("unexpected number of nodes in NodeInfoList. Expected: %v, got: %v", cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len(), snapshot.nodeSlices.outOfPartitionNodeSlice.Len())
	}

	expectedNodeInfoList := make([]framework.NodeInfo, 0, cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len())
	expectedHavePodsWithAffinityNodeInfoList := make([]framework.NodeInfo, 0, cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Len())
	if cache.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Store.ConditionRange(func(nodeName string, _ generationstore.StoredObj) bool {
		if obj := snapshot.storeSwitch.Find(nodestore.Name).(*nodestore.NodeStore).Get(nodeName); obj != nil {
			n := obj
			expectedNodeInfoList = append(expectedNodeInfoList, n)
			if len(n.GetPodsWithAffinity()) > 0 {
				expectedHavePodsWithAffinityNodeInfoList = append(expectedHavePodsWithAffinityNodeInfoList, n)
			}
			return true
		}
		return false
	}) {
		return fmt.Errorf("node exist in nodeTree but not in NodeInfoMap, this should not happen")
	}

	/*
		// When we get nodeNames in an unordered way, we don't need to perform this judgment.
		for i, expected := range expectedNodeInfoList {
			got := snapshot.outOfPartitionNodeInfoList[i]
			if expected != got {
				return fmt.Errorf("unexpected NodeInfo pointer in NodeInfoList. Expected: %p, got: %p", expected, got)
			}
		}
	*/

	for _, expected := range expectedHavePodsWithAffinityNodeInfoList {
		find := false
		for _, got := range snapshot.nodeSlices.havePodsWithAffinityNodeSlice.Nodes() {
			if got == expected {
				find = true
			}
		}
		if !find {
			return fmt.Errorf("can not find node in snapshot %v", expected.GetNodeName())
		}
	}

	return nil
}

func TestSchedulerCache_updateNodeInfoSnapshotList(t *testing.T) {
	// Create a few nodes to be used in tests.
	nodes := []*v1.Node{}
	i := 0
	// List of number of nodes per zone, zone 0 -> 2, zone 1 -> 6
	for zone, nb := range []int{2, 6} {
		for j := 0; j < nb; j++ {
			nodes = append(nodes, &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("node-%d", i),
					Labels: map[string]string{
						v1.LabelZoneRegion:        fmt.Sprintf("region-%d", zone),
						v1.LabelZoneFailureDomain: fmt.Sprintf("zone-%d", zone),
					},
				},
			})
			i++
		}
	}

	var cache *schedulerCache
	var snapshot *Snapshot

	addNode := func(t *testing.T, i int) {
		if err := cache.AddNode(nodes[i]); err != nil {
			t.Error(err)
		}
		// After refactoring the cache, we do not update the entire NodeList at the time of the updateNodesSnapshot.
		//
		// obj := snapshot.nodeStore.Get(nodes[i].Name)
		// if obj == nil {
		// 	snapshot.nodeStore.Set(nodes[i].Name, cache.nodeStore.Get(nodes[i].Name))
		// }
	}
	updateSnapshot := func(t *testing.T) {
		cache.storeSwitch.Find(nodestore.Name).UpdateSnapshot(snapshot.storeSwitch.Find(nodestore.Name))
		if err := compareCacheWithNodeInfoSnapshot(cache, snapshot); err != nil {
			t.Error(err)
		}
	}

	tests := []struct {
		name       string
		operations func(t *testing.T)
		expected   sets.String
	}{
		{
			name:       "Empty cache",
			operations: func(t *testing.T) {},
			expected:   sets.NewString(),
		},
		{
			name: "Single node",
			operations: func(t *testing.T) {
				addNode(t, 0)
			},
			expected: sets.NewString("node-0"),
		},
		{
			name: "Two nodes",
			operations: func(t *testing.T) {
				addNode(t, 0)
				updateSnapshot(t)
				addNode(t, 1)
			},
			expected: sets.NewString("node-0", "node-1"),
		},
		{
			name: "two nodes, update the snapshot and add two nodes in different zones",
			operations: func(t *testing.T) {
				addNode(t, 2)
				addNode(t, 3)
				updateSnapshot(t)
				addNode(t, 4)
				addNode(t, 0)
			},
			expected: sets.NewString("node-0", "node-2", "node-3", "node-4"),
		},
		{
			name: "6 nodes, one in a different zone",
			operations: func(t *testing.T) {
				addNode(t, 2)
				addNode(t, 3)
				addNode(t, 4)
				addNode(t, 5)
				updateSnapshot(t)
				addNode(t, 6)
				addNode(t, 0)
			},
			expected: sets.NewString("node-0", "node-2", "node-3", "node-4", "node-5", "node-6"),
		},
		{
			name: "7 nodes, two in a different zone",
			operations: func(t *testing.T) {
				addNode(t, 2)
				updateSnapshot(t)
				addNode(t, 3)
				addNode(t, 4)
				updateSnapshot(t)
				addNode(t, 5)
				addNode(t, 6)
				addNode(t, 0)
				addNode(t, 1)
			},
			expected: sets.NewString("node-0", "node-1", "node-2", "node-3", "node-4", "node-5", "node-6"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cacheHandler := handler.MakeCacheHandlerWrapper().
				SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
				TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
				EnableStore("PreemptionStore").
				Obj()
			snapshotHandler := handler.MakeCacheHandlerWrapper().
				SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
				EnableStore("PreemptionStore").
				Obj()
			cache = newSchedulerCache(cacheHandler)
			snapshot = NewEmptySnapshot(snapshotHandler)

			test.operations(t)

			// Always update the snapshot at the end of operations and compare it.
			cache.storeSwitch.Find(nodestore.Name).UpdateSnapshot(snapshot.storeSwitch.Find(nodestore.Name))
			if err := compareCacheWithNodeInfoSnapshot(cache, snapshot); err != nil {
				t.Error(err)
			}
			nodeNames := sets.NewString()
			for _, nodeInfo := range snapshot.nodeSlices.outOfPartitionNodeSlice.Nodes() {
				nodeNames.Insert(nodeInfo.GetNodeName())
			}
			if !test.expected.Equal(nodeNames) {
				t.Errorf("The nodeInfoList is incorrect. Expected %v , got %v", test.expected, nodeNames)
			}
		})
	}
}

func BenchmarkUpdate1kNodes30kPods(b *testing.B) {
	cache := setupCacheOf1kNodes30kPods(b)
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		cachedNodes := NewEmptySnapshot(handler.MakeCacheHandlerWrapper().
			SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
			EnableStore("PreemptionStore").
			Obj())
		cache.UpdateSnapshot(cachedNodes)
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
		cache.storeSwitch.Find(podstore.Name).(*podstore.PodStore).CleanupExpiredAssumedPods(&cache.mu, now.Add(2*time.Second))
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

func setupCacheOf1kNodes30kPods(b *testing.B) SchedulerCache {
	cacheHandler := handler.MakeCacheHandlerWrapper().
		SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
		EnableStore("PreemptionStore").
		Obj()
	cache := newSchedulerCache(cacheHandler)
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

func setupCacheWithAssumedPods(b *testing.B, podNum int, assumedTime time.Time) *schedulerCache {
	cacheHandler := handler.MakeCacheHandlerWrapper().
		SchedulerName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		TTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
		EnableStore("PreemptionStore").
		Obj()
	cache := newSchedulerCache(cacheHandler)
	for i := 0; i < podNum; i++ {
		nodeName := fmt.Sprintf("node-%d", i/10)
		objName := fmt.Sprintf("%s-pod-%d", nodeName, i%10)
		pod := makeBasePod(b, nodeName, objName, "0", "0", "", nil, podutil.GuaranteedPod)

		err := assumeAndFinishReserving(cache, pod, assumedTime)
		if err != nil {
			b.Fatalf("assumePod failed: %v", err)
		}
	}
	return cache
}

func isForgottenFromCache(p *v1.Pod, c *schedulerCache) error {
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
