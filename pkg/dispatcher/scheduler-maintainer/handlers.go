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
	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	schedulerapi "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"

	sche "github.com/kubewharf/godel-scheduler/pkg/dispatcher/internal/scheduler"
	"github.com/kubewharf/godel-scheduler/pkg/dispatcher/metrics"
	nodeutil "github.com/kubewharf/godel-scheduler/pkg/util/node"
)

// AddScheduler adds schedulers to maintainer cache
func (maintainer *SchedulerMaintainer) AddScheduler(scheduler *schedulerapi.Scheduler) {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	maintainer.updateSchedulerBasedOnSchedulerCRD(scheduler)
}

// updateSchedulerBasedOnSchedulerCRD updates gs based on schedulers crd
// we assume the caller has already got the lock
func (maintainer *SchedulerMaintainer) updateSchedulerBasedOnSchedulerCRD(scheduler *schedulerapi.Scheduler) {
	if maintainer.generalSchedulers[scheduler.Name] == nil {
		// schedulers is not added before, add it to active schedulers map directly
		gs := sche.NewGodelSchedulerWithSchedulerCRD(scheduler)
		metrics.SchedulerSizeInc(metrics.ActiveScheduler)
		maintainer.generalSchedulers[scheduler.Name] = gs
		return
	}

	maintainer.generalSchedulers[scheduler.Name].SetScheduler(scheduler)
	/*if maintainer.generalInactiveSchedulers[schedulers.Name] != nil {
		// TODO: update some gs fields here if necessary based on schedulers crd
	}
	if maintainer.generalActiveSchedulers[schedulers.Name] != nil {
		// TODO: update some gs fields here if necessary based on schedulers crd
	}*/
	if IsSchedulerActive(scheduler) {
		if !maintainer.generalSchedulers[scheduler.Name].IsSchedulerActive() {
			metrics.SchedulerSizeInc(metrics.ActiveScheduler)
			metrics.SchedulerSizeDec(metrics.InactiveScheduler)
			maintainer.generalSchedulers[scheduler.Name].SetSchedulerActive()
		}
	} else {
		if maintainer.generalSchedulers[scheduler.Name].IsSchedulerActive() {
			metrics.SchedulerSizeInc(metrics.InactiveScheduler)
			metrics.SchedulerSizeDec(metrics.ActiveScheduler)
			maintainer.generalSchedulers[scheduler.Name].SetSchedulerInActive()
		}
	}
}

// UpdateScheduler updates schedulers info in maintainer cache
func (maintainer *SchedulerMaintainer) UpdateScheduler(oldScheduler *schedulerapi.Scheduler, newScheduler *schedulerapi.Scheduler) {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	maintainer.updateSchedulerBasedOnSchedulerCRD(newScheduler)
}

// DeleteScheduler moves schedulers from active queue to inactive queue to trigger shuffle operation later
func (maintainer *SchedulerMaintainer) DeleteScheduler(scheduler *schedulerapi.Scheduler) {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	// deactivate the schedulers
	if maintainer.generalSchedulers[scheduler.Name] != nil {
		// TODO: if we add more fields into GodelScheduler,
		// add more checks here (for example: check if dispatched pods are all dispatched) if we want to delete it from generalInactiveSchedulers
		if len(maintainer.generalSchedulers[scheduler.Name].GetNodes()) == 0 {
			delete(maintainer.generalSchedulers, scheduler.Name)
		} else {
			maintainer.generalSchedulers[scheduler.Name].SetSchedulerInActive()
		}
	}
}

// getGodelScheduler gets the GodelScheduler by schedulers name
// we assume the caller has get the lock
func (maintainer *SchedulerMaintainer) getGodelScheduler(schedulerName string) *sche.GodelScheduler {
	return maintainer.generalSchedulers[schedulerName]
}

func (maintainer *SchedulerMaintainer) addNodeToGodelScheduler(schedulerName string, nodeName string) {
	scheduler := maintainer.getGodelScheduler(schedulerName)
	newlyCreate := false
	if scheduler == nil {
		scheduler = sche.NewGodelSchedulerWithSchedulerName(schedulerName)
		newlyCreate = true
		metrics.SchedulerSizeInc(metrics.ActiveScheduler)
	}

	// node and nmnode objects share the same node name, so only one node will be added to this map
	scheduler.AddNode(nodeName)
	if newlyCreate {
		// we directly add node to active schedulers here,
		// schedulers will be moved to inactive schedulers by a separate sync-up goroutine if it is not alive
		maintainer.generalSchedulers[schedulerName] = scheduler
	}
}

// AddNodeToGodelSchedulerIfNotPresent adds node to specific godel schedulers
func (maintainer *SchedulerMaintainer) AddNodeToGodelSchedulerIfNotPresent(node *v1.Node) error {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	if len(node.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]) == 0 {
		// annotation is nil or does not contain GodelSchedulerNodeAnnotationKey,
		// this kind of node will be handled by a separate sync-up goroutine, so skip directly here
		return nil
	}

	schedulerName := node.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]
	maintainer.addNodeToGodelScheduler(schedulerName, node.Name)

	return nil
}

// AddNMNodeToGodelSchedulerIfNotPresent adds NMNode to specific godel schedulers
func (maintainer *SchedulerMaintainer) AddNMNodeToGodelSchedulerIfNotPresent(nmNode *nodev1alpha1.NMNode) error {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	if len(nmNode.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]) == 0 {
		// annotation is nil or does not contain GodelSchedulerNodeAnnotationKey,
		// this kind of node will be handled by a separate sync-up goroutine, so skip directly here
		return nil
	}

	schedulerName := nmNode.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]
	maintainer.addNodeToGodelScheduler(schedulerName, nmNode.Name)

	return nil
}

// we assume the caller has already get the lock
func (maintainer *SchedulerMaintainer) nodeExistsInGodelScheduler(nodeName string, schedulerName string) bool {
	if maintainer.generalSchedulers[schedulerName] == nil {
		// schedulers does not exist in maintainer, return false
		return false
	}

	_, found := maintainer.generalSchedulers[schedulerName].GetNodes()[nodeName]
	return found
}

// UpdateNodeInGodelSchedulerIfNecessary updates node info for specific godel schedulers
func (maintainer *SchedulerMaintainer) UpdateNodeInGodelSchedulerIfNecessary(oldNode *v1.Node, newNode *v1.Node) error {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	oldSchedulerName := oldNode.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]
	newSchedulerName := newNode.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]
	// schedulers name exists and is not updated
	if oldSchedulerName == newSchedulerName && len(newSchedulerName) > 0 {
		nodeExist := maintainer.nodeExistsInGodelScheduler(newNode.Name, newSchedulerName)
		if !nodeExist {
			maintainer.addNodeToGodelScheduler(newSchedulerName, newNode.Name)
		}

		// schedulers name is not updated and node is already in godel schedulers partition, return directly
		return nil
	}

	if len(oldSchedulerName) > 0 {
		if maintainer.generalSchedulers[oldSchedulerName] != nil {
			maintainer.generalSchedulers[oldSchedulerName].RemoveNode(oldNode.Name)
		}
	}

	if len(newSchedulerName) > 0 {
		maintainer.addNodeToGodelScheduler(newSchedulerName, newNode.Name)
	}
	return nil
}

// UpdateNMNodeInGodelSchedulerIfNecessary updates nmnode info for specific godel schedulers
func (maintainer *SchedulerMaintainer) UpdateNMNodeInGodelSchedulerIfNecessary(oldNMNode *nodev1alpha1.NMNode, newNMNode *nodev1alpha1.NMNode) error {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	oldSchedulerName := oldNMNode.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]
	newSchedulerName := newNMNode.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]
	// TODO: We can remove this check
	if oldSchedulerName == newSchedulerName && len(newSchedulerName) > 0 {
		nodeExist := maintainer.nodeExistsInGodelScheduler(newNMNode.Name, newSchedulerName)
		if !nodeExist {
			maintainer.addNodeToGodelScheduler(newSchedulerName, newNMNode.Name)
		}

		// schedulers name is not updated and node is already in godel schedulers partition, return directly
		return nil
	}

	if len(oldSchedulerName) > 0 {
		if maintainer.generalSchedulers[oldSchedulerName] != nil {
			maintainer.generalSchedulers[oldSchedulerName].RemoveNode(oldNMNode.Name)
		}
	}

	if len(newSchedulerName) > 0 {
		maintainer.addNodeToGodelScheduler(newSchedulerName, newNMNode.Name)
	}
	return nil
}

// DeleteNodeFromGodelScheduler deletes node from specific godel schedulers
func (maintainer *SchedulerMaintainer) DeleteNodeFromGodelScheduler(node *v1.Node) error {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	if len(node.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]) == 0 {
		// annotation is nil or does not contain GodelSchedulerNodeAnnotationKey,
		// this kind of node will be handled by a separate sync-up goroutine, so skip directly here
		return nil
	}

	schedulerName := node.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]
	if maintainer.generalSchedulers[schedulerName] != nil {
		maintainer.generalSchedulers[schedulerName].RemoveNode(node.Name)
	}

	return nil
}

// DeleteNMNodeFromGodelScheduler deletes nmnode from specific godel schedulers
func (maintainer *SchedulerMaintainer) DeleteNMNodeFromGodelScheduler(nmNode *nodev1alpha1.NMNode) error {
	maintainer.schedulerMux.Lock()
	defer maintainer.schedulerMux.Unlock()

	if len(nmNode.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]) == 0 {
		// annotation is nil or does not contain GodelSchedulerNodeAnnotationKey,
		// this kind of node will be handled by a separate sync-up goroutine, so skip directly here
		return nil
	}

	schedulerName := nmNode.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]
	if maintainer.generalSchedulers[schedulerName] != nil {
		maintainer.generalSchedulers[schedulerName].RemoveNode(nmNode.Name)
	}

	return nil
}
