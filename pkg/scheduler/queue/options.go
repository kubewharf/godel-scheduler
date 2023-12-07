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

package queue

import (
	"time"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/util"
)

type schedulingQueueOptions struct {
	switchType                    framework.SwitchType
	subCluster                    string
	clock                         util.Clock
	unitInitialBackoffDuration    time.Duration
	unitMaxBackoffDuration        time.Duration
	owner                         string
	attemptImpactFactorOnPriority float64
}

// Option configures a PriorityQueue
type Option func(*schedulingQueueOptions)

// WithClock sets clock for PriorityQueue, the default clock is util.RealClock.
func WithClock(clock util.Clock) Option {
	return func(o *schedulingQueueOptions) {
		o.clock = clock
	}
}

// WithUnitInitialBackoffDuration sets unit initial backoff duration for PriorityQueue.
func WithUnitInitialBackoffDuration(duration time.Duration) Option {
	return func(o *schedulingQueueOptions) {
		o.unitInitialBackoffDuration = duration
	}
}

// WithPodMaxBackoffDuration sets unit max backoff duration for PriorityQueue.
func WithPodMaxBackoffDuration(duration time.Duration) Option {
	return func(o *schedulingQueueOptions) {
		o.unitMaxBackoffDuration = duration
	}
}

// WithOwner sets owner name for PriorityQueue.
func WithOwner(owner string) Option {
	return func(o *schedulingQueueOptions) {
		o.owner = owner
	}
}

// WithSwitchType sets SwitchType for PriorityQueue.
func WithSwitchType(switchType framework.SwitchType) Option {
	return func(o *schedulingQueueOptions) {
		o.switchType = switchType
	}
}

// WithSubCluster sets SubCluster for PriorityQueue.
func WithSubCluster(subCluster string) Option {
	return func(o *schedulingQueueOptions) {
		o.subCluster = subCluster
	}
}

// WithAttemptImpactFactorOnPriority sets attemptImpactFactorOnPriority for PriorityQueue.
func WithAttemptImpactFactorOnPriority(attemptImpactFactorOnPriority float64) Option {
	return func(o *schedulingQueueOptions) {
		o.attemptImpactFactorOnPriority = attemptImpactFactorOnPriority
	}
}

var defaultPriorityQueueOptions = schedulingQueueOptions{
	clock:                         util.RealClock{},
	unitInitialBackoffDuration:    config.DefaultUnitInitialBackoffInSeconds * time.Second,
	unitMaxBackoffDuration:        config.DefaultUnitMaxBackoffInSeconds * time.Second,
	attemptImpactFactorOnPriority: config.DefaultAttemptImpactFactorOnPriority,
}
