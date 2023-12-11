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

package scheduler_maintainer

import (
	"context"
	"sync"
	"time"

	crdclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	schedulerlister "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	sche "github.com/kubewharf/godel-scheduler/pkg/dispatcher/internal/scheduler"
)

// TODO: figure out if we need a separate CRD for Node Partition
type SchedulerMaintainer struct {
	schedulerMux sync.RWMutex

	// TODO: support dynamic node partition type switching
	// NodePartitionType indicates the type of node partition (physical or logical)
	NodePartitionType string

	crdClient crdclient.Interface

	schedulerLister schedulerlister.SchedulerLister

	// TODO: support customized schedulers
	// customizedSchedulers stores the schedulers with specific requirements, for example: specific task selector, specific node selector
	// the nodes satisfy schedulers node selector can not be managed by general schedulers any more.
	// TODO: we may need to specify/adjust preemption policy for this later
	// customizedSchedulers map[string]*sche.GodelScheduler

	// TODO: add a more fine-grained lock for generalSchedulers later if we want to remove schedulerMux
	// schedulers CRUD operations should be atomic and should be locked.
	// generalSchedulers are the schedulers who are not designed for specific pods and nodes
	generalSchedulers map[string]*sche.GodelScheduler
}

// NewSchedulerMaintainer creates a new SchedulerMaintainer struct object
func NewSchedulerMaintainer(crdClient crdclient.Interface, schedulerLister schedulerlister.SchedulerLister) *SchedulerMaintainer {
	return &SchedulerMaintainer{
		NodePartitionType: string(Logical),
		crdClient:         crdClient,
		schedulerLister:   schedulerLister,
		generalSchedulers: make(map[string]*sche.GodelScheduler),
	}
}

// Run runs all necessary workers
func (maintainer *SchedulerMaintainer) Run(stopCh <-chan struct{}) {
	// populate schedulers periodically
	go wait.Until(maintainer.PopulateSchedulers, 1*time.Minute, stopCh)

	go wait.Until(maintainer.SyncUpSchedulersStatus, 30*time.Second, stopCh)

	<-stopCh
}

// PopulateSchedulers will populate existing schedulers to active queue
func (maintainer *SchedulerMaintainer) PopulateSchedulers() {
	schedulers, err := maintainer.schedulerLister.List(labels.Everything())
	if err != nil {
		klog.InfoS("Failed to list schedulers", "err", err)
	}
	// add existing schedulers
	for _, scheduler := range schedulers {
		maintainer.AddScheduler(scheduler)
	}
}

// SyncUpSchedulersStatus will be responsible for syncing up schedulers between active queue and inactive queue
// TODO: we need to handle this scenario: schedulers exists in both active queue and inactive queue
// be careful about the race condition when we handle the scenario above
// we can delete schedulers from one of the queues based on schedulers actual status
func (maintainer *SchedulerMaintainer) SyncUpSchedulersStatus() {
	activeSchedulers := maintainer.GetActiveSchedulers()
	for _, schedulerName := range activeSchedulers {
		scheduler, err := maintainer.schedulerLister.Get(schedulerName)
		if err != nil && !errors.IsNotFound(err) {
			klog.InfoS("Failed to get the schedulers CRD", "schedulerName", schedulerName, "err", err)
			continue
		}
		if errors.IsNotFound(err) {
			maintainer.DeactivateScheduler(schedulerName)
			continue
		}
		if !IsSchedulerActive(scheduler) {
			klog.V(3).InfoS("Started to delete the inactive schedulers", "schedulerName", schedulerName)
			// schedulers is still there and it is not active, delete it.
			err := maintainer.crdClient.SchedulingV1alpha1().Schedulers().Delete(context.TODO(), scheduler.Name, metav1.DeleteOptions{})
			if err != nil {
				klog.InfoS("Failed to delete the inactive schedulers", "schedulerName", scheduler.Name, "err", err)
			}
		}
	}

	inactiveSchedulers := maintainer.GetInactiveSchedulers()
	for _, schedulerName := range inactiveSchedulers {
		scheduler, err := maintainer.schedulerLister.Get(schedulerName)
		if err != nil && !errors.IsNotFound(err) {
			klog.InfoS("Failed to get the schedulers CRD", "schedulerName", schedulerName, "err", err)
			continue
		}
		if errors.IsNotFound(err) {
			klog.V(4).InfoS("The schedulers didn't exist any more", "schedulerName", schedulerName)
			continue
		}
		if IsSchedulerActive(scheduler) {
			maintainer.ActivateScheduler(scheduler.Name)
		} else {
			klog.V(3).InfoS("Started to delete the inactive schedulers", "schedulerName", schedulerName)
			// schedulers is still there and it is not active, delete it.
			err := maintainer.crdClient.SchedulingV1alpha1().Schedulers().Delete(context.TODO(), scheduler.Name, metav1.DeleteOptions{})
			if err != nil {
				klog.InfoS("Failed to delete the inactive schedulers", "schedulerName", scheduler.Name, "err", err)
			}
		}
	}
}

func (maintainer *SchedulerMaintainer) CleanupInActiveSchedulers() {
	// TODO: if number of nodes in inactive schedulers's partition is 0, remove this inactive schedulers
}

func (maintainer *SchedulerMaintainer) SyncupNodePartitionType() {
	// TODO: implement dynamic node partition type switching
	// TODO: based on node resource usage water level ?
}
