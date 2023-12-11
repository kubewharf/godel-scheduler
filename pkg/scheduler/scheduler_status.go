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

package scheduler

import (
	"fmt"
	"time"

	"github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/util"
)

const (
	// maxUpdateRetries is the number of immediate, successive retries the Scheduler will attempt
	// when renewing the Scheduler status before it waits for the renewal interval before trying again,
	// similar to what we do for node status retries
	maxUpdateRetries = 5
	// sleep is the default interval for retry
	sleep = 100 * time.Millisecond
)

// StatusMaintainer manages creating and renewing the status for this Scheduler
type StatusMaintainer interface {
	Run(stopCh <-chan struct{})
}

type maintainer struct {
	crdClient     godelclient.Interface
	schedulerName string
	renewInterval time.Duration
	clock         clock.Clock
}

// NewSchedulerStatusMaintainer constructs and returns a maintainer
func NewSchedulerStatusMaintainer(clock clock.Clock, client godelclient.Interface, schedulerName string, renewIntervalSeconds int64) StatusMaintainer {
	renewInterval := time.Duration(renewIntervalSeconds) * time.Second
	return &maintainer{
		crdClient:     client,
		schedulerName: schedulerName,
		renewInterval: renewInterval,
		clock:         clock,
	}
}

// Run runs the maintainer
func (c *maintainer) Run(stopCh <-chan struct{}) {
	if c.crdClient == nil {
		klog.ErrorS(nil, "Exited the scheduler status maintainer because the CRD client was nil", "schedulerName", c.schedulerName)
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	wait.Until(c.sync, c.renewInterval, stopCh)
}

// sync attempts to update the status for Scheduler
// update Status.LastUpdateTime at the moment
func (c *maintainer) sync() {
	if err := ensureSchedulerUpToDate(c.crdClient, c.clock, c.schedulerName); err != nil {
		klog.InfoS("Failed to update scheduler status, will retry later", "schedulerName", c.schedulerName, "renewInterval", c.renewInterval)
	}
}

// ensureSchedulerUpToDate try to update scheduler status, if failed, retry after sleep duration, at most maxUpdateRetries
func ensureSchedulerUpToDate(client godelclient.Interface, clock clock.Clock, schedulerName string) error {
	for i := 0; i < maxUpdateRetries; i++ {
		err := updateSchedulerStatus(client, schedulerName)
		if err != nil {
			klog.InfoS("Failed to update scheduler, will retry later", "schedulerName", schedulerName, "err", err)
			clock.Sleep(sleep)
			continue
		}
		return nil
	}
	return fmt.Errorf("failed %d attempts to update scheduler status", maxUpdateRetries)
}

// updateSchedulerStatus tries to update Scheduler status to apiserver, if Scheduler not exists, add new Scheduler to apiserver
func updateSchedulerStatus(client godelclient.Interface, schedulerName string) error {
	existed, err := util.GetScheduler(client, schedulerName)
	now := metav1.Now()
	if err == nil && existed != nil {
		// if scheduler crd exists, update lastUpdateTime
		updated := existed.DeepCopy()
		updated.Status.LastUpdateTime = &now

		if _, err := util.UpdateSchedulerStatus(client, updated); err != nil {
			err = fmt.Errorf("failed to update scheduler %v, will retry later, error is %v", schedulerName, err)
			return err
		}
		return nil
	}

	if !apierrors.IsNotFound(err) {
		err = fmt.Errorf("failed to get scheduler %v, will retry later, error is %v", schedulerName, err)
		return err
	}

	klog.InfoS("WARN: scheduler was gone, should check this", "schedulerName", schedulerName, "err", err)
	// if scheduler crd not exists, create a new scheduler crd
	schedulerCRD := &v1alpha1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{
			Name: schedulerName,
		},
	}
	created, err := util.PostScheduler(client, schedulerCRD)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			klog.InfoS("WARN: skipped register because scheduler already existed", "schedulerName", schedulerName, "err", err)
			return nil
		}
		err = fmt.Errorf("failed to update scheduler %v, will retry later, error is %v", schedulerName, err)
		return err
	}
	// status subresource is not updated with scheduler creation, so need another updating for scheduler crd
	created.Status.LastUpdateTime = &now
	if _, err := util.UpdateSchedulerStatus(client, created); err != nil {
		err = fmt.Errorf("failed to update scheduler %v, will retry later, error is %v", schedulerName, err)
		return err
	}
	return nil
}
