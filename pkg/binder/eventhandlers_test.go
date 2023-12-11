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
	"fmt"
	"testing"
	"time"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	clientsetfake "k8s.io/client-go/kubernetes/fake"

	"github.com/kubewharf/godel-scheduler/pkg/binder/cache"
	"github.com/kubewharf/godel-scheduler/pkg/binder/queue"
	podAnnotation "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

var testSchedulerName = "test-scheduler"

func TestAssignedPod(t *testing.T) {
	for _, test := range []struct {
		pod      *v1.Pod
		expected bool
		name     string
	}{
		{
			name: "pod has node assigned",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testPod",
					Namespace: "testNS",
					UID:       "testUID",
					Annotations: map[string]string{
						podAnnotation.AssumedNodeAnnotationKey: "node-1",
					},
				},
				Spec: v1.PodSpec{
					NodeName: "node-1",
				},
			},
			expected: true,
		},
		{
			name: "pod does not have node assigned",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testPod",
					Namespace: "testNS",
					UID:       "testUID",
					Annotations: map[string]string{
						podAnnotation.AssumedNodeAnnotationKey: "node-1",
					},
				},
			},
			expected: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, podutil.BoundPod(test.pod), test.name)
		})
	}
}

func TestNominatedPod(t *testing.T) {
	for _, test := range []struct {
		pod      *v1.Pod
		expected bool
		name     string
	}{
		{
			name: "pod has node nominated",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testPod",
					Namespace: "testNS",
					UID:       "testUID",
					Annotations: map[string]string{
						podAnnotation.PodStateAnnotationKey:        string(podAnnotation.PodAssumed),
						podAnnotation.PodResourceTypeAnnotationKey: string(podAnnotation.GuaranteedPod),
						podAnnotation.PodLauncherAnnotationKey:     string(podAnnotation.Kubelet),
						podAnnotation.AssumedNodeAnnotationKey:     "node-1",
						podAnnotation.SchedulerAnnotationKey:       "scheduler-1",
					},
				},
			},
			expected: true,
		},
		{
			name: "pod does not have node nominated",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testPod",
					Namespace: "testNS",
					UID:       "testUID",
					Annotations: map[string]string{
						podAnnotation.PodResourceTypeAnnotationKey: string(podAnnotation.GuaranteedPod),
						podAnnotation.PodLauncherAnnotationKey:     string(podAnnotation.Kubelet),
					},
				},
			},
			expected: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, podAnnotation.AssumedPod(test.pod), test.name)
		})
	}
}

func TestAddNominatedPod(t *testing.T) {
	ttl := 10 * time.Second
	stopCh := make(chan struct{})
	defer close(stopCh)

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testPod",
			Namespace: "testNS",
			UID:       "testUID",
			Annotations: map[string]string{
				podAnnotation.AssumedNodeAnnotationKey:     "node-1",
				podAnnotation.PodStateAnnotationKey:        string(podAnnotation.PodAssumed),
				podAnnotation.PodResourceTypeAnnotationKey: string(podAnnotation.GuaranteedPod),
				podAnnotation.PodLauncherAnnotationKey:     string(podAnnotation.Kubelet),
				podAnnotation.SchedulerAnnotationKey:       "scheduler-1",
			},
			ResourceVersion: "1",
		},
	}

	newPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testPod",
			Namespace: "testNS",
			UID:       "testUID2",
			Annotations: map[string]string{
				podAnnotation.AssumedNodeAnnotationKey:     "node-2",
				podAnnotation.SchedulerAnnotationKey:       "testScheduler",
				podAnnotation.PodStateAnnotationKey:        string(podutil.PodAssumed),
				podAnnotation.PodResourceTypeAnnotationKey: string(podAnnotation.GuaranteedPod),
				podAnnotation.PodLauncherAnnotationKey:     string(podAnnotation.Kubelet),
			},
			ResourceVersion: "1",
		},
	}

	binderCache := cache.New(ttl, stopCh, "")
	binderQueue := queue.NewPriorityQueue(nil, nil, nil)

	binder := &Binder{
		BinderCache:   binderCache,
		BinderQueue:   binderQueue,
		SchedulerName: &testSchedulerName,
	}

	// error scenario
	binder.addPodToBinderQueue(&pod)
	t.Log("test")
	return

	// error scenario -- ignore already assumed node
	pod.Spec.NodeName = "testnode"
	binder.addPodToBinderQueue(pod)
	pod.Spec.NodeName = ""

	binder.addPodToBinderQueue(pod)
	binder.deletePodFromBinderQueue(pod)

	// error scenario for update
	binder.updatePodInBinderQueue(&pod, newPod)
	binder.updatePodInBinderQueue(pod, newPod)

	// error scenario same version
	binder.updatePodInBinderQueue(pod, newPod)
	pod.ResourceVersion = "2" // update resource version

	// new pod is not assumed whereas old pod is assumed
	newPod.Spec.NodeName = "testnode"
	binder.updatePodInBinderQueue(pod, newPod)
	newPod.Spec.NodeName = ""

	// ignore already deleted pod in update
	newPod.DeletionTimestamp = &metav1.Time{
		Time: time.Now(),
	}
	binder.updatePodInBinderQueue(pod, newPod)

	// skip pod update for assumed pod
	binderCache.AssumePod(newPod)
	binder.updatePodInBinderQueue(pod, newPod)
	binderCache.ForgetPod(newPod)

	newPod.DeletionTimestamp = nil
	pod.Spec.NodeName = ""
	newPod.Spec.NodeName = ""
	binder.updatePodInBinderQueue(pod, newPod)
	u, err := binder.BinderQueue.Pop()
	assert.NoError(t, err)
	assert.Equal(t, u.GetPods()[0].Pod.UID, newPod.UID)
}

func TestAddAssignedPod(t *testing.T) {
	ttl := 10 * time.Second
	stopCh := make(chan struct{})
	defer close(stopCh)

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testPod",
			Namespace: "testNS",
			UID:       "testUID",
		},
		Spec: v1.PodSpec{
			NodeName: "node-1",
		},
	}

	newPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testPod",
			Namespace: "testNS",
			UID:       "testUID2",
		},
		Spec: v1.PodSpec{
			NodeName: "node-2",
		},
	}

	binderCache := cache.New(ttl, stopCh, "")
	binderQueue := queue.NewPriorityQueue(nil, nil, nil)

	binder := &Binder{
		BinderCache: binderCache,
		BinderQueue: binderQueue,
	}

	binder.addPodToCache(pod)
	binder.updatePodInCache(pod, newPod)

	p, err := binder.BinderCache.GetPod(newPod)
	assert.NoError(t, err)
	assert.Equal(t, p.UID, newPod.UID)

	binder.deletePodFromCache(newPod)
	_, err = binder.BinderCache.GetPod(newPod)
	assert.Error(t, err)
	binder.deletePodFromCache(newPod)
	binder.deletePodFromCache(newPod)
}

func TestDeletePodWithNominatedNode(t *testing.T) {
	ttl := 10 * time.Second
	stopCh := make(chan struct{})
	defer close(stopCh)

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testPod",
			Namespace: "testNS",
			UID:       "testUID",
			Annotations: map[string]string{
				podAnnotation.AssumedNodeAnnotationKey:     "node-1",
				podAnnotation.PodStateAnnotationKey:        string(podAnnotation.PodAssumed),
				podAnnotation.PodResourceTypeAnnotationKey: string(podAnnotation.GuaranteedPod),
				podAnnotation.PodLauncherAnnotationKey:     string(podAnnotation.Kubelet),
				podAnnotation.SchedulerAnnotationKey:       testSchedulerName,
			},
			ResourceVersion: "1",
		},
		Spec: v1.PodSpec{
			NodeName: "node-1",
		},
	}

	preemptor := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testPreemptor",
			Namespace: "testNS",
			UID:       "testUID2",
			Annotations: map[string]string{
				podAnnotation.SchedulerAnnotationKey:       testSchedulerName,
				podAnnotation.PodStateAnnotationKey:        string(podutil.PodAssumed),
				podAnnotation.PodResourceTypeAnnotationKey: string(podAnnotation.GuaranteedPod),
				podAnnotation.PodLauncherAnnotationKey:     string(podAnnotation.Kubelet),
				podAnnotation.NominatedNodeAnnotationKey:   fmt.Sprintf(`{"node":"node-1","victims":[{"name": "%s", "namespace": "%s", "uid": "%s"}]}`, pod.Name, pod.Namespace, pod.UID),
			},
			ResourceVersion: "1",
		},
	}

	binderCache := cache.New(ttl, stopCh, "")
	binderQueue := queue.NewPriorityQueue(nil, nil, nil)

	client, crdClient := clientsetfake.NewSimpleClientset(), godelclientfake.NewSimpleClientset()

	binder := &Binder{
		BinderCache:   binderCache,
		BinderQueue:   binderQueue,
		SchedulerName: &testSchedulerName,
		handle: NewFrameworkHandle(
			client, crdClient,
			informers.NewSharedInformerFactory(client, 0), crdinformers.NewSharedInformerFactory(crdClient, 0),
			binderOptions{},
			binderCache, volumeBindingTimeoutSeconds,
		),
	}

	binder.addPodToCache(pod)
	p, _ := binderCache.GetPod(pod)
	assert.NotEqual(t, nil, p)
	binder.addPodToBinderQueue(preemptor)
	binderCache.MarkPodToDelete(pod, preemptor)

	markedToBeDeleted, _ := binderCache.IsPodMarkedToDelete(pod)
	assert.Equal(t, true, markedToBeDeleted)

	binder.deletePodFromBinderQueue(preemptor)
	markedToBeDeleted, _ = binderCache.IsPodMarkedToDelete(pod)
	assert.Equal(t, false, markedToBeDeleted)
}

func TestNode(t *testing.T) {
	ttl := 10 * time.Second
	stopCh := make(chan struct{})
	defer close(stopCh)

	testNode := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 20)},
	}

	testUpdateNode := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 10, 20)},
	}

	binderCache := cache.New(ttl, stopCh, "")
	binderQueue := queue.NewPriorityQueue(nil, nil, nil)

	binder := &Binder{
		BinderCache: binderCache,
		BinderQueue: binderQueue,
	}

	binder.addNodeToCache(testNode)
	binder.updateNodeInCache(testNode, testUpdateNode)

	_, err := binder.BinderCache.GetNode(testNode.Name)
	assert.NoError(t, err)

	binder.deleteNodeFromCache(testNode)
}

func TestNode_Error(t *testing.T) {
	ttl := 10 * time.Second
	stopCh := make(chan struct{})
	defer close(stopCh)

	testNode := v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 20)},
	}

	testUpdateNode := v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status:     v1.NodeStatus{Capacity: makeResources(10, 20, 32, 20).Capacity, Allocatable: makeAllocatableResources(10, 20, 10, 20)},
	}

	binderCache := cache.New(ttl, stopCh, "")
	binderQueue := queue.NewPriorityQueue(nil, nil, nil)

	binder := &Binder{
		BinderCache: binderCache,
		BinderQueue: binderQueue,
	}

	binder.addNodeToCache(testNode)
	binder.updateNodeInCache(testNode, testUpdateNode)
	binder.updateNodeInCache(&testNode, testUpdateNode)
	binder.deleteNodeFromCache(testNode)
}

func TestNMNode(t *testing.T) {
	ttl := 10 * time.Second
	stopCh := make(chan struct{})
	defer close(stopCh)

	testNMNode := &nodev1alpha1.NMNode{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
	}

	testUpdateNMNode := &nodev1alpha1.NMNode{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status: nodev1alpha1.NMNodeStatus{
			NodeStatus: "v1alpha1.NodePhase",
		},
	}

	binderCache := cache.New(ttl, stopCh, "")
	binderQueue := queue.NewPriorityQueue(nil, nil, nil)

	binder := &Binder{
		BinderCache: binderCache,
		BinderQueue: binderQueue,
	}

	binder.addNMNodeToCache(testNMNode)
	binder.updateNMNodeInCache(testNMNode, testUpdateNMNode)

	_, err := binder.BinderCache.GetNode(testNMNode.Name)
	assert.NoError(t, err)

	binder.deleteNMNodeFromCache(testNMNode)
}

func TestNMNode_Error(t *testing.T) {
	ttl := 10 * time.Second
	stopCh := make(chan struct{})
	defer close(stopCh)

	testNMNode := nodev1alpha1.NMNode{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
	}

	testUpdateNMNode := nodev1alpha1.NMNode{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", UID: types.UID("machine1")},
		Status: nodev1alpha1.NMNodeStatus{
			NodeStatus: "v1alpha1.NodePhase",
		},
	}

	binderCache := cache.New(ttl, stopCh, "")
	binderQueue := queue.NewPriorityQueue(nil, nil, nil)

	binder := &Binder{
		BinderCache: binderCache,
		BinderQueue: binderQueue,
	}

	binder.addNMNodeToCache(testNMNode)
	binder.updateNMNodeInCache(testNMNode, testUpdateNMNode)
	binder.updateNMNodeInCache(&testNMNode, testUpdateNMNode)
	binder.deleteNMNodeFromCache(testNMNode)
}
