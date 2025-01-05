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
	"fmt"
	"time"

	schedulerapi "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	"k8s.io/klog/v2"

	sche "github.com/kubewharf/godel-scheduler/pkg/dispatcher/internal/scheduler"
	"github.com/kubewharf/godel-scheduler/pkg/dispatcher/metrics"
)

// ActivateScheduler moves schedulers from inactive queue to active queue
func (maintainer *SchedulerMaintainer) ActivateScheduler(schedulerName string) error {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	klog.V(3).InfoS("Started to activate the schedulers", "schedulerName", schedulerName)
	if maintainer.generalSchedulers[schedulerName] != nil {
		if !maintainer.generalSchedulers[schedulerName].IsSchedulerActive() {
			metrics.SchedulerSizeInc(metrics.ActiveScheduler)
			metrics.SchedulerSizeDec(metrics.InactiveScheduler)
			maintainer.generalSchedulers[schedulerName].SetSchedulerActive()
		}
	}

	return nil
}

// DeactivateScheduler moves schedulers from active queue to inactive queue
func (maintainer *SchedulerMaintainer) DeactivateScheduler(schedulerName string) error {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	klog.V(3).InfoS("Started to deactivate the schedulers", "schedulerName", schedulerName)
	if maintainer.generalSchedulers[schedulerName] != nil {
		if maintainer.generalSchedulers[schedulerName].IsSchedulerActive() {
			metrics.SchedulerSizeDec(metrics.ActiveScheduler)
			metrics.SchedulerSizeInc(metrics.InactiveScheduler)
			maintainer.generalSchedulers[schedulerName].SetSchedulerInActive()
		}
	}

	return nil
}

// GetActiveSchedulers gets all active schedulers
func (maintainer *SchedulerMaintainer) GetActiveSchedulers() []string {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	activeSchedulers := make([]string, 0)
	for schedulerName, gs := range maintainer.generalSchedulers {
		if gs.IsSchedulerActive() {
			activeSchedulers = append(activeSchedulers, schedulerName)
		}
	}

	return activeSchedulers
}

// GetInactiveSchedulers gets all inactive schedulers
func (maintainer *SchedulerMaintainer) GetInactiveSchedulers() []string {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	inactiveSchedulers := make([]string, 0)
	for schedulerName, gs := range maintainer.generalSchedulers {
		if !gs.IsSchedulerActive() {
			inactiveSchedulers = append(inactiveSchedulers, schedulerName)
		}
	}

	return inactiveSchedulers
}

type SchedulerNodePartitionType string

const (
	// if schedulers crd is not updated for 2 minutes (MaxSchedulerCRDNotUpdateDuration), schedulers will be considered as inactive
	MaxSchedulerCRDNotUpdateDuration = 2 * time.Minute

	Physical SchedulerNodePartitionType = "Physical"
	Logical  SchedulerNodePartitionType = "Logical"
)

// IsSchedulerActive checks whether schedulers is active
func IsSchedulerActive(scheduler *schedulerapi.Scheduler) bool {
	now := time.Now()
	// TODO: need to figure out: if schedulers.Status.LastUpdateTime is not set, should we return ture ?
	if scheduler.Status.LastUpdateTime == nil {
		return false
	}
	// TODO: for now, we check schedulers status based on schedulers.Status.LastUpdateTime, do we need to adjust this ?
	//       目前，我们根据 schedulers.Status.LastUpdateTime 检查调度程序的状态，是否需要调整？
	//  如果 2 分钟（MaxSchedulerCRDNotUpdateDuration）内没有更新调度程序的 crd，调度程序将被视为非活动状态
	return now.Before(scheduler.Status.LastUpdateTime.Add(MaxSchedulerCRDNotUpdateDuration))
}

// GetEarliestInactiveScheduler gets the earliest inactive schedulers
func (maintainer *SchedulerMaintainer) GetEarliestInactiveCloneScheduler() *sche.GodelScheduler {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	if maintainer.generalSchedulers == nil || len(maintainer.generalSchedulers) == 0 {
		return nil
	}

	var result *sche.GodelScheduler
	for _, gs := range maintainer.generalSchedulers {
		if !gs.IsSchedulerActive() {
			if result == nil {
				result = gs
			} else if result.GetScheduler().Status.LastUpdateTime.After(gs.GetScheduler().Status.LastUpdateTime.Time) {
				result = gs
			}
		}
	}

	if result == nil {
		return nil
	} else {
		return result.Clone()
	}
}

func (maintainer *SchedulerMaintainer) SchedulerExist(schedulerName string) bool {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	return maintainer.generalSchedulers[schedulerName] != nil
}

// IsSchedulerInInactiveQueue checks whether the schedulers is inactive
func (maintainer *SchedulerMaintainer) IsSchedulerInInactiveQueue(schedulerName string) bool {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	return maintainer.generalSchedulers[schedulerName] != nil && !maintainer.generalSchedulers[schedulerName].IsSchedulerActive()
}

// IsSchedulerInActiveQueue checks whether the schedulers is active
func (maintainer *SchedulerMaintainer) IsSchedulerInActiveQueue(schedulerName string) bool {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	return maintainer.generalSchedulers[schedulerName] != nil && maintainer.generalSchedulers[schedulerName].IsSchedulerActive()
}

// GetNumberOfNodesFromActiveScheduler gets the number of nodes for general active schedulers
func (maintainer *SchedulerMaintainer) GetNumberOfNodesFromActiveScheduler(schedulerName string) int {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	if maintainer.generalSchedulers[schedulerName] == nil || !maintainer.generalSchedulers[schedulerName].IsSchedulerActive() {
		return 0
	}

	return len(maintainer.generalSchedulers[schedulerName].GetNodes())
}

// SchedulersWithLeastNumberOfNodes stores the schedulers names with the least number of nodes, as well as the number of nodes
type SchedulersWithLeastNumberOfNodes struct {
	LeastNumberOfNodesSchedulerName string
	LeastNumberOfNodes              int
}

// GetGeneralActiveSchedulerWithLeastNumberOfNodes gets the schedulers with the least number of nodes from general active schedulers
// GetGeneralActiveSchedulerWithLeastNumberOfNodes 从 schedulers 中获取节点数最少的 scheduler
func (maintainer *SchedulerMaintainer) GetGeneralActiveSchedulerWithLeastNumberOfNodes() *SchedulersWithLeastNumberOfNodes {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	var swlnn *SchedulersWithLeastNumberOfNodes
	for schedulerName, gs := range maintainer.generalSchedulers {
		if gs.IsSchedulerActive() {
			if swlnn == nil {
				swlnn = &SchedulersWithLeastNumberOfNodes{
					LeastNumberOfNodes:              len(gs.GetNodes()),
					LeastNumberOfNodesSchedulerName: schedulerName,
				}
			} else {
				if len(gs.GetNodes()) < swlnn.LeastNumberOfNodes {
					swlnn.LeastNumberOfNodesSchedulerName = schedulerName
					swlnn.LeastNumberOfNodes = len(gs.GetNodes())
				}
			}
		}
	}

	return swlnn
}

// SchedulersWithMostAndLeastNumberOfNodes stores the schedulers names with most and least number of nodes, as well as the number of nodes
type SchedulersWithMostAndLeastNumberOfNodes struct {
	MostNumberOfNodesSchedulerName  string
	MostNumberOfNodes               int
	LeastNumberOfNodesSchedulerName string
	LeastNumberOfNodes              int
}

// GetSchedulersWithMostAndLeastNumberOfNodes returns the schedulers info with most and least number of nodes
func (maintainer *SchedulerMaintainer) GetSchedulersWithMostAndLeastNumberOfNodes() *SchedulersWithMostAndLeastNumberOfNodes {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	var swmlnn *SchedulersWithMostAndLeastNumberOfNodes
	for schedulerName, gs := range maintainer.generalSchedulers {
		if gs.IsSchedulerActive() {
			if swmlnn == nil {
				swmlnn = &SchedulersWithMostAndLeastNumberOfNodes{
					MostNumberOfNodesSchedulerName:  schedulerName,
					MostNumberOfNodes:               len(gs.GetNodes()),
					LeastNumberOfNodesSchedulerName: schedulerName,
					LeastNumberOfNodes:              len(gs.GetNodes()),
				}
			} else {
				if len(gs.GetNodes()) > swmlnn.MostNumberOfNodes {
					swmlnn.MostNumberOfNodesSchedulerName = schedulerName
					swmlnn.MostNumberOfNodes = len(gs.GetNodes())
				} else if len(gs.GetNodes()) < swmlnn.LeastNumberOfNodes {
					swmlnn.LeastNumberOfNodesSchedulerName = schedulerName
					swmlnn.LeastNumberOfNodes = len(gs.GetNodes())
				}
			}
		}
	}

	return swmlnn
}

func (maintainer *SchedulerMaintainer) GetSomeNodeNamesFromGeneralActiveScheduler(numberOfNodes int, schedulerName string) ([]string, error) {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	if numberOfNodes <= 0 {
		klog.V(4).InfoS("The number of nodes to be moved was not greater than 0", "numberOfNodes", numberOfNodes)
		return nil, nil
	}

	if maintainer.generalSchedulers[schedulerName] == nil || !maintainer.generalSchedulers[schedulerName].IsSchedulerActive() {
		return nil, fmt.Errorf("can not find schedulers:%s in general active queue", schedulerName)
	}

	nodesOfThisScheduler := maintainer.generalSchedulers[schedulerName].GetNodes()
	if len(nodesOfThisScheduler) <= numberOfNodes {
		return nil, fmt.Errorf("the number of nodes in this schedulers:%s is no greater than requested: %d", schedulerName, numberOfNodes)
	}

	nodeNames := make([]string, numberOfNodes)
	i := 0
	for nodeName := range nodesOfThisScheduler {
		nodeNames[i] = nodeName
		i++
		if i == numberOfNodes {
			break
		}
	}
	return nodeNames, nil
}
