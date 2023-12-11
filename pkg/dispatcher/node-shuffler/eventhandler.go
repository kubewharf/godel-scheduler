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
	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"

	nodeutil "github.com/kubewharf/godel-scheduler/pkg/util/node"
)

func (ns *NodeShuffler) addNodeToProcessingQueueIfNecessary(nodeName string, annotations map[string]string) {
	if len(annotations[nodeutil.GodelSchedulerNodeAnnotationKey]) == 0 {
		ns.nodeProcessingQueue.Add(&NodeToBeProcessed{
			nodeName: nodeName,
			reason:   NoSchedulerName,
		})
	} else if !ns.schedulerMaintainer.SchedulerExist(annotations[nodeutil.GodelSchedulerNodeAnnotationKey]) || ns.schedulerMaintainer.IsSchedulerInInactiveQueue(annotations[nodeutil.GodelSchedulerNodeAnnotationKey]) {
		ns.nodeProcessingQueue.Add(&NodeToBeProcessed{
			nodeName: nodeName,
			reason:   InactiveScheduler,
		})
	}
}

func (ns *NodeShuffler) AddNode(node *v1.Node) error {
	ns.addNodeToProcessingQueueIfNecessary(node.Name, node.Annotations)
	return nil
}

func (ns *NodeShuffler) AddNMNode(nmNode *nodev1alpha1.NMNode) error {
	ns.addNodeToProcessingQueueIfNecessary(nmNode.Name, nmNode.Annotations)
	return nil
}

func (ns *NodeShuffler) UpdateNode(oldNode *v1.Node, newNode *v1.Node) error {
	ns.addNodeToProcessingQueueIfNecessary(newNode.Name, newNode.Annotations)
	return nil
}

func (ns *NodeShuffler) UpdateNMNode(oldNMNode *nodev1alpha1.NMNode, newNMNode *nodev1alpha1.NMNode) error {
	ns.addNodeToProcessingQueueIfNecessary(newNMNode.Name, newNMNode.Annotations)
	return nil
}
