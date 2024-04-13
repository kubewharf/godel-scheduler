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

package reconciler

import (
	"context"
	"time"

	nodelisterv1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/node/v1alpha1"
	schedulerv1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/dispatcher/internal/store"
	schemaintainer "github.com/kubewharf/godel-scheduler/pkg/dispatcher/scheduler-maintainer"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// PodStateReconciler stores all abnormal state pods and try to reset pod state
type PodStateReconciler struct {
	client                   kubernetes.Interface
	podLister                listerv1.PodLister
	nodeLister               listerv1.NodeLister
	schedulerLister          schedulerv1alpha1.SchedulerLister
	nmNodeLister             nodelisterv1alpha1.NMNodeLister
	abnormalPodsQueue        workqueue.Interface
	staleDispatchedPodsQueue workqueue.Interface
	schedulerName            string

	dispatchedPodsStore store.DispatchInfo

	schedulerMaintainer *schemaintainer.SchedulerMaintainer
	populator           *DispatchedPodsPopulator
}

// NewPodStateReconciler creates a new PodStateReconciler struct
func NewPodStateReconciler(client kubernetes.Interface,
	podLister listerv1.PodLister,
	nodeLister listerv1.NodeLister,
	schedulerLister schedulerv1alpha1.SchedulerLister,
	nmNodeLister nodelisterv1alpha1.NMNodeLister,
	schedulerName string,
	dispatchedPodsStore store.DispatchInfo,
	maintainer *schemaintainer.SchedulerMaintainer,
) *PodStateReconciler {
	staleDispatchedPodsQueue := workqueue.NewNamed("stale-dispatched-pods-queue")
	populator := NewDispatchedPodsPopulator(schedulerName, podLister, staleDispatchedPodsQueue, maintainer)

	return &PodStateReconciler{
		schedulerName:            schedulerName,
		client:                   client,
		podLister:                podLister,
		nodeLister:               nodeLister,
		schedulerLister:          schedulerLister,
		nmNodeLister:             nmNodeLister,
		abnormalPodsQueue:        workqueue.NewNamed("abnormal-pods-queue"),
		staleDispatchedPodsQueue: staleDispatchedPodsQueue,
		populator:                populator,
		schedulerMaintainer:      maintainer,
		dispatchedPodsStore:      dispatchedPodsStore,
	}
}

// Run runs pod state syncer worker
func (psr *PodStateReconciler) Run(stop <-chan struct{}) {
	go wait.Until(psr.AbnormalStatePodsSyncer, time.Second, stop)
	go wait.Until(psr.StaleDispatchedPodsSyncer, time.Second, stop)

	go psr.populator.Run(stop)
}

// AbnormalPodsEnqueue adds obj to abnormal queue
func (psr *PodStateReconciler) AbnormalPodsEnqueue(obj interface{}) {
	psr.abnormalPodsQueue.Add(obj)
}

// StaleDispatchedPodsEnqueue adds obj to stale dispatched queue
func (psr *PodStateReconciler) StaleDispatchedPodsEnqueue(obj interface{}) {
	psr.staleDispatchedPodsQueue.Add(obj)
}

func (psr *PodStateReconciler) StaleDispatchedPodsSyncer() {
	workFunc := func() bool {
		podKeyObj, quit := psr.staleDispatchedPodsQueue.Get()
		if quit {
			return true
		}
		defer psr.staleDispatchedPodsQueue.Done(podKeyObj)
		podKey := podKeyObj.(string)

		namespace, name, err := cache.SplitMetaNamespaceKey(podKey)
		if err != nil {
			klog.InfoS("Failed to get namespace & name of the pod from informer", "pod", podKey, "err", err)
			return false
		}

		klog.V(3).InfoS("The StaleDispatchedPodsSyncer started to process", "pod", klog.KRef(namespace, name))

		pod, err := psr.podLister.Pods(namespace).Get(name)
		if err == nil {
			// The pod still exists in informer cache
			if err := psr.updateStaleDispatchedStatePod(pod); err != nil {
				// re-add the pod to the queue
				psr.staleDispatchedPodsQueue.Add(podKey)
			}
			return false
		}
		if !errors.IsNotFound(err) {
			klog.InfoS("Failed to get the pod from informer", "pod", podKey, "err", err)
			// re-add the pod to the queue
			psr.staleDispatchedPodsQueue.Add(podKey)
			return false
		}

		// if err is Not Found, the pod should have been deleted, return directly
		return false
	}

	for {
		if quit := workFunc(); quit {
			klog.InfoS("Shut down the worker queue for the stale dispatched pods syncer")
			return
		}
	}
}

func (psr *PodStateReconciler) updateStaleDispatchedStatePod(pod *corev1.Pod) error {
	if podutil.DispatchedPodOfGodel(pod, psr.schedulerName) {
		schedulerName := pod.Annotations[podutil.SchedulerAnnotationKey]
		if psr.schedulerMaintainer.IsSchedulerInInactiveQueue(schedulerName) || !psr.schedulerMaintainer.SchedulerExist(schedulerName) {
			klog.V(3).InfoS("Reset the dispatched pod to Pending state on inactive/nonexistent scheduler", "pod", klog.KObj(pod), "schedulerName", schedulerName)
			return psr.resetPodToPendingState(pod)
		}
		return nil
	}
	// if pod is not dispatched now, ignore this pod
	return nil
}

// AbnormalStatePodsSyncer tries reset abnormal state pods
func (psr *PodStateReconciler) AbnormalStatePodsSyncer() {
	workFunc := func() bool {
		podKeyObj, quit := psr.abnormalPodsQueue.Get()
		if quit {
			return true
		}
		defer psr.abnormalPodsQueue.Done(podKeyObj)
		podKey := podKeyObj.(string)

		namespace, name, err := cache.SplitMetaNamespaceKey(podKey)
		if err != nil {
			klog.InfoS("Failed to get namespace & name of the pod from informer", "pod", podKey, "err", err)
			return false
		}

		klog.V(3).InfoS("The AbnormalStatePodsSyncer started to process", "pod", klog.KRef(namespace, name))

		pod, err := psr.podLister.Pods(namespace).Get(name)
		if err == nil {
			// The pod still exists in informer cache
			if err := psr.updateAbnormalStatePod(pod); err != nil {
				// re-add the pod to the queue
				psr.abnormalPodsQueue.Add(podKey)
			}
			return false
		}
		if !errors.IsNotFound(err) {
			klog.InfoS("Failed to get the pod from informer", "pod", podKey, "err", err)
			// re-add the pod to the queue
			psr.abnormalPodsQueue.Add(podKey)
			return false
		}

		// if err is Not Found, the pod should have been deleted, return directly
		return false
	}

	for {
		if quit := workFunc(); quit {
			klog.InfoS("Shut down the worker queue for the abnormal state pods syncer")
			return
		}
	}
}

// updatePodState tries to update pod state if it is abnormal
func (psr *PodStateReconciler) updateAbnormalStatePod(pod *corev1.Pod) error {
	abnormal := podutil.AbnormalPodStateOfGodel(pod, psr.schedulerName)
	if abnormal {
		klog.V(3).InfoS("Reset the abnormal pod to Pending state", "pod", klog.KObj(pod))
		// pod is still abnormal
		// blindly resetting to pending state
		// TODO: add more fine-grained checking and resetting operations
		return psr.resetPodToPendingState(pod)
	}
	// pod returns back to normal state, return directly
	return nil
}

// resetPodToPendingState resets pod state to Pending
func (psr *PodStateReconciler) resetPodToPendingState(pod *corev1.Pod) error {
	podClone := pod.DeepCopy()
	if podClone.Annotations == nil {
		podClone.Annotations = make(map[string]string)
	}
	podClone.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodPending)
	delete(podClone.Annotations, podutil.SchedulerAnnotationKey)
	delete(podClone.Annotations, podutil.AssumedNodeAnnotationKey)
	delete(podClone.Annotations, podutil.NominatedNodeAnnotationKey)
	_, err := psr.client.CoreV1().Pods(podClone.Namespace).Update(context.TODO(), podClone, metav1.UpdateOptions{})
	return err
}
