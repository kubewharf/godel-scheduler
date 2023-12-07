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

package node_shuffler

import (
	"fmt"
)

// schedulerNameShouldBeUpdated checks if the scheduler name of this node should be updated
// We assume that we already check the scheduler name before calling this function, that is to say: schedulerName != ""
func (ns *NodeShuffler) schedulerNameShouldBeUpdated(schedulerName string) (shouldUpdate bool, selectedScheduler string) {
	// TODO: add more complex checking method
	// TODO: if this node matches one NodeSelector of the specific schedulers, add separate logic for that

	schedulerInfo := ns.schedulerMaintainer.GetGeneralActiveSchedulerWithLeastNumberOfNodes()
	if schedulerInfo == nil || schedulerName == schedulerInfo.LeastNumberOfNodesSchedulerName {
		return false, schedulerName
	}

	if !ns.schedulerMaintainer.SchedulerExist(schedulerName) || ns.schedulerMaintainer.IsSchedulerInInactiveQueue(schedulerName) {
		return true, schedulerInfo.LeastNumberOfNodesSchedulerName
	}

	// We now assume scheduler is in active queue
	numberOfNodes := ns.schedulerMaintainer.GetNumberOfNodesFromActiveScheduler(schedulerName)
	if numberOfNodes > schedulerInfo.LeastNumberOfNodes*2 {
		return true, schedulerInfo.LeastNumberOfNodesSchedulerName
	}

	return false, ""
}

// chooseOneScheduler chooses one suitable active scheduler to add a node to its partition
func (ns *NodeShuffler) chooseOneSchedulerForThisNode( /*node *v1.Node, nmNode *nodev1alpha1.NMNode*/ ) (schedulerName string, err error) {
	// TODO: if this node matches one NodeSelector of the specific schedulers, add node to that scheduler

	// if no, select the general active scheduler with the least number of nodes
	schedulerInfo := ns.schedulerMaintainer.GetGeneralActiveSchedulerWithLeastNumberOfNodes()
	if schedulerInfo == nil {
		return "", fmt.Errorf("no active schedulers are found")
	}

	return schedulerInfo.LeastNumberOfNodesSchedulerName, nil
}
