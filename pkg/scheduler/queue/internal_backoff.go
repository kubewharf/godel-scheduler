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
	"github.com/kubewharf/godel-scheduler/pkg/util"
)

// backoffHandler
type backoffHandler struct {
	// unit initial backoff duration.
	initialDuration time.Duration
	// unit maximum backoff duration.
	maxDuration time.Duration
	// clock holds the same clock with queue.
	clock util.Clock
}

func newBackoffHandler(initialDuration, maxDuration time.Duration, clock util.Clock) *backoffHandler {
	return &backoffHandler{
		initialDuration: initialDuration,
		maxDuration:     maxDuration,
		clock:           clock,
	}
}

// calculateBackoffDuration is a helper function for calculating the backoffDuration
// based on the number of attempts the pod has made.
func (h *backoffHandler) calculateBackoffDuration(unitInfo *framework.QueuedUnitInfo) time.Duration {
	if unitInfo.Attempts == 0 {
		return 0
	}
	duration := h.initialDuration
	for i := 1; i < unitInfo.Attempts; i++ {
		// Use subtraction instead of addition or multiplication to avoid overflow.
		if duration > h.maxDuration-duration {
			return h.maxDuration
		}
		duration += duration
	}
	return duration
}

// getBackoffTime returns the time that podInfo completes backoff
func (h *backoffHandler) getBackoffTime(unitInfo *framework.QueuedUnitInfo) time.Time {
	duration := h.calculateBackoffDuration(unitInfo)
	backoffTime := unitInfo.Timestamp.Add(duration)
	return backoffTime
}

// isUnitBackoff returns true if a unit is still waiting for its backoff timer.
// If this returns true, the unit should not be re-tried.
func (h *backoffHandler) isUnitBackoff(unitInfo *framework.QueuedUnitInfo) bool {
	boTime := h.getBackoffTime(unitInfo)
	return boTime.After(h.clock.Now())
}

func (h *backoffHandler) unitsCompareBackoffCompleted(u1, u2 interface{}) bool {
	bo1 := h.getBackoffTime(u1.(*framework.QueuedUnitInfo))
	bo2 := h.getBackoffTime(u2.(*framework.QueuedUnitInfo))
	return bo1.Before(bo2)
}
