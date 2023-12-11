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
	"math"

	utilfeature "k8s.io/apiserver/pkg/util/feature"

	"github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

const (
	// Name is the name of the plugin used in the plugin registry and configurations.
	Name = "GodelSort"
)

func (p *PriorityQueue) getMinPriorityInQueueIfExists() (int32, bool) {
	rawUnit := p.priorityHeap.Peek()
	if rawUnit == nil {
		return 0, false
	}
	unitInfo := rawUnit.(*framework.QueuedUnitInfo)
	return unitInfo.GetPriority(), true
}

func (p *PriorityQueue) setQueuedPriorityScore(unitInfo *framework.QueuedUnitInfo) {
	priority := unitInfo.GetPriority()
	unitInfo.QueuePriorityScore = float64(priority)
	if utilfeature.DefaultFeatureGate.Enabled(features.DynamicSchedulingPriority) {
		minPriority, exists := p.getMinPriorityInQueueIfExists()
		if !exists {
			minPriority = priority
		}
		unitInfo.QueuePriorityScore = getScore(int64(priority), int64(minPriority), int64(unitInfo.Attempts), p.attemptImpactFactorOnPriority)
	}
}

// getScore returns score, in the range minScore <= score <= priority.
// If attempts is 0, score is priority. When attempts increases, score should reduce step.
// If priority - attempts * step < minScore - step, then score should be priority.
// For example, if priority is 10, minScore is 0, step is 5:
// case 1: attempts is 0, score is 10.
// case 2: attempts is 2, score is 0
// case 3: attempts is 3, score is -5.
// case 4: attempts is 4, score is 10.
// case 5: attempts is 6, score is 0.
// case 6: attempts is 7, score is -5.
// case 6: attempts is 8, score is 10.
func getScore(priority, minPriority, attempts int64, step float64) float64 {
	if attempts == 0 {
		return float64(priority)
	}
	gap := priority - minPriority
	count := int64(math.Max(2, math.Floor(float64(gap)/step)+2))
	score := float64(priority) - float64(attempts%count)*step
	return score
}
