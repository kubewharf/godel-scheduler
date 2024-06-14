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

package coscheduling

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	clientsetfake "k8s.io/client-go/kubernetes/fake"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	podgroupstore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/podgroup_store"
	schedulertesting "github.com/kubewharf/godel-scheduler/pkg/scheduler/testing"
	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	podAnnotations "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

var (
	testNodeName = "node1"
	testPod1     = &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testpod1",
			Namespace: "ns",
			UID:       types.UID("testpod1"),
		},
		Spec: v1.PodSpec{
			NodeName: testNodeName,
		},
	}
	testPod2 = &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testpod2",
			Namespace: "ns",
			UID:       types.UID("testpod2"),
		},
		Spec: v1.PodSpec{
			NodeName: testNodeName,
		},
	}
	testPod3 = &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testpod2",
			Namespace: "ns",
			UID:       types.UID("testpod2"),
		},
		Spec: v1.PodSpec{
			NodeName: testNodeName,
		},
	}
	testPod4 = &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testpod2",
			Namespace: "ns",
			UID:       types.UID("testpod2"),
		},
		Spec: v1.PodSpec{
			NodeName: testNodeName,
		},
	}
	testNode1 = testinghelper.MakeNode().Name(testNodeName).Obj()
)

func createPodGroup(namespace, name string, minMember int32) *schedulingv1a1.PodGroup {
	return &schedulingv1a1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(name),
		},
		Spec: schedulingv1a1.PodGroupSpec{
			MinMember: minMember,
		},
		Status: schedulingv1a1.PodGroupStatus{
			Phase: schedulingv1a1.PodGroupPending,
		},
	}
}

func AddPGAnnotations(pod *v1.Pod, pgName string) *v1.Pod {
	annotations := map[string]string{
		podAnnotations.PodGroupNameAnnotationKey: pgName,
	}
	pod.SetAnnotations(annotations)

	return pod
}

func TestPreFilter(t *testing.T) {
	pgName := "pg"
	podGroup := createPodGroup(testPod1.Namespace, pgName, 3)

	invalidPgName := "invalidpg"
	invalidPodGroup := createPodGroup(testPod1.Namespace, invalidPgName, 0)

	timeoutPgName := "timeoutpg"
	timeoutPodGroup := createPodGroup(testPod1.Namespace, timeoutPgName, 3)
	timeoutPodGroup.Status.Phase = schedulingv1a1.PodGroupTimeout

	podGroupInfoCache := map[string]*schedulingv1a1.PodGroup{
		fmt.Sprintf("%s/%s", podGroup.Namespace, podGroup.Name):               podGroup,
		fmt.Sprintf("%s/%s", invalidPodGroup.Namespace, invalidPodGroup.Name): invalidPodGroup,
		fmt.Sprintf("%s/%s", timeoutPodGroup.Namespace, timeoutPodGroup.Name): timeoutPodGroup,
	}

	client := clientsetfake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(client, 0)
	crdClient := godelclientfake.NewSimpleClientset()

	cache := godelcache.New(commoncache.MakeCacheHandlerWrapper().
		ComponentName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		PodAssumedTTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
		EnableStore("PreemptionStore").
		Obj())
	snapshot := godelcache.NewEmptySnapshot(commoncache.MakeCacheHandlerWrapper().
		SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
		EnableStore("PreemptionStore").
		Obj())

	for _, p := range []*v1.Pod{testPod1, testPod2, testPod3, testPod4} {
		cache.AddPod(p)
	}
	for _, n := range []*v1.Node{testNode1} {
		cache.AddNode(n)
	}
	for _, pg := range podGroupInfoCache {
		cache.AddPodGroup(pg)
	}

	cache.UpdateSnapshot(snapshot)

	fh, _ := schedulertesting.NewPodFrameworkHandle(client, crdClient, informerFactory, nil, cache, snapshot, nil, nil, nil, nil)
	coscheduling := &Coscheduling{frameworkHandler: fh, pluginHandle: fh.FindStore(podgroupstore.Name).(podgroupstore.StoreHandle)}

	// Normal pod without pod group annotation
	code := coscheduling.PreFilter(context.Background(), framework.NewCycleState(), testPod1)
	assert.True(t, code.IsSuccess())

	// pod with annotation but pod group not in cache
	AddPGAnnotations(testPod3, "not-exist-pg")
	code = coscheduling.PreFilter(context.Background(), framework.NewCycleState(), testPod3)
	assert.True(t, code.IsUnschedulable())
	assert.True(t, strings.Contains(code.Reasons()[0], "not found"))

	// pod with annotation but pod group's minMember is less than or equal to 0
	AddPGAnnotations(testPod3, invalidPgName)
	code = coscheduling.PreFilter(context.Background(), framework.NewCycleState(), testPod3)
	assert.True(t, code.IsUnschedulable())
	assert.True(t, strings.Contains(code.Reasons()[0], "<= 0"))

	// pod with timeout pod group annotation
	AddPGAnnotations(testPod2, timeoutPgName)
	code = coscheduling.PreFilter(context.Background(), framework.NewCycleState(), testPod2)
	assert.True(t, code.IsUnschedulable())
	assert.True(t, strings.Contains(code.Reasons()[0], "timeout"))

	// pod with valid pod group
	AddPGAnnotations(testPod4, pgName)
	code = coscheduling.PreFilter(context.Background(), framework.NewCycleState(), testPod4)
	assert.True(t, code.IsSuccess())
}
