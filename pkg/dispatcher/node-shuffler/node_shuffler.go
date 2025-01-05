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
	"context"
	"sync"
	"time"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	crdclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	nodelister "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/node/v1alpha1"
	schedulerlister "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corelister "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/dispatcher/metrics"
	schemaintainer "github.com/kubewharf/godel-scheduler/pkg/dispatcher/scheduler-maintainer"
	nodeutil "github.com/kubewharf/godel-scheduler/pkg/util/node"
)

// NodeShuffler stores all the necessary info to shuffle nodes
// TODO: support different policies later
type NodeShuffler struct {
	schedulerMaintainer *schemaintainer.SchedulerMaintainer

	k8sClient    kubernetes.Interface
	crdClient    crdclient.Interface
	nodeLister   corelister.NodeLister
	nmNodeLister nodelister.NMNodeLister

	schedulerLister schedulerlister.SchedulerLister

	// TODO: remove nodes from this queue if nodes are not necessary to be processed again ?
	// TODO: change data structure to improve performance if we want to delete node from it ?
	nodeProcessingQueue *nodeQueue
}

type nodeQueue struct {
	sync.Mutex
	nodeProcessingQueue *workqueue.Type
	nodeCache           map[string]bool
}

// NewNodeQueue creates a new nodeQueue struct object
func NewNodeQueue() *nodeQueue {
	return &nodeQueue{
		nodeProcessingQueue: workqueue.NewNamed("node-shuffler"),
		nodeCache:           make(map[string]bool),
	}
}

// Add adds an item to the queue
func (nq *nodeQueue) Add(ntbp *NodeToBeProcessed) {
	nq.Lock()
	defer nq.Unlock()

	if !nq.nodeCache[ntbp.nodeName] {
		nq.nodeProcessingQueue.Add(ntbp)
		nq.nodeCache[ntbp.nodeName] = true
	}
}

// Done marks item as done processing
func (nq *nodeQueue) Done(item interface{}) {
	nq.Lock()
	defer nq.Unlock()

	if item != nil {
		nq.nodeProcessingQueue.Done(item)
	}
}

// Get pops an item from the queue
func (nq *nodeQueue) Get() (*NodeToBeProcessed, bool) {
	item, quit := nq.nodeProcessingQueue.Get()
	if quit {
		return nil, quit
	}

	nodeInfo, ok := item.(*NodeToBeProcessed)
	if !ok {
		klog.InfoS("Failed to convert item to NodeToBeProcessed", "item", item)
		// don't add it back to the queue again
		nq.nodeProcessingQueue.Done(item)
		return nil, false
	}
	nq.Lock()
	delete(nq.nodeCache, nodeInfo.nodeName)
	nq.Unlock()
	return nodeInfo, quit
}

// NodeToBeProcessed stores the node info which may need to be reassigned to another scheduler
type NodeToBeProcessed struct {
	nodeName string
	reason   EnqueueReason
}

type EnqueueReason string

const (
	// no scheduler name in node annotations
	NoSchedulerName EnqueueReason = "NoSchedulerName"
	// node in inactive scheduler's partition
	InactiveScheduler EnqueueReason = "InactiveScheduler"
	// too many nodes in this scheduler's partition
	TooManyNodesInThisPartition EnqueueReason = "TooManyNodesInThisPartition"
)

// NewNodeShuffler creates a new NodeShuffler struct
func NewNodeShuffler(k8sClient kubernetes.Interface, crdClient crdclient.Interface,
	nodeLister corelister.NodeLister, nmNodeLister nodelister.NMNodeLister, schedulerLister schedulerlister.SchedulerLister,
	maintainer *schemaintainer.SchedulerMaintainer,
) *NodeShuffler {
	return &NodeShuffler{
		k8sClient:           k8sClient,
		crdClient:           crdClient,
		nodeLister:          nodeLister,
		nmNodeLister:        nmNodeLister,
		schedulerLister:     schedulerLister,
		schedulerMaintainer: maintainer,
		nodeProcessingQueue: NewNodeQueue(),
	}
}

// Run runs different tasks of node shuffler
func (ns *NodeShuffler) Run(stopCh <-chan struct{}) {
	// run node processing worker every one second
	go wait.Until(ns.nodeProcessingWorker, time.Second, stopCh) // 更新节点状态，比如有节点 add，为 node 分配 scheudler，并将信息更新到 node annotation
	// run re-balancing goroutine every one minute
	go wait.Until(ns.ReBalanceSchedulerNodes, time.Minute, stopCh) // 重新平衡调度器的节点数量
	// TODO: sync up node and cnr scheduler name annotation

	<-stopCh
}

// nodeProcessingWorker keeps updating scheduler name annotation for node
// nodeProcessingWorker 不断更新节点的调度器名称注释
func (ns *NodeShuffler) nodeProcessingWorker() {
	workFunc := func() bool {
		nodeInfo, quit := ns.nodeProcessingQueue.Get()
		if quit {
			return true
		}
		defer ns.nodeProcessingQueue.Done(nodeInfo)
		if nodeInfo == nil {
			// can not convert the popped item to NodeToBeProcessed, return directly
			return false
		}
		klog.V(5).InfoS("Started to process node", "nodeInfo", nodeInfo)

		// nodeLister 获取 node
		node, nodeErr := ns.nodeLister.Get(nodeInfo.nodeName)
		if nodeErr != nil && !errors.IsNotFound(nodeErr) {
			klog.InfoS("Failed to get the node from informer", "node", klog.KRef("", nodeInfo.nodeName), "err", nodeErr)
			//  don't add it back to the queue directly, wait for another node update event
			// ns.nodeProcessingQueue.Add(nodeInfo)
			return false
		}

		// nmNodeLister 获取 nmnode
		nmNode, nmNodeErr := ns.nmNodeLister.Get(nodeInfo.nodeName)
		if nmNodeErr != nil && !errors.IsNotFound(nmNodeErr) {
			klog.InfoS("Failed to get the nmnode from informer", "node", klog.KObj(node), "err", nmNodeErr)
			//  don't add it back to the queue directly, wait for another node update event
			// ns.nodeProcessingQueue.Add(nodeInfo)
			return false
		}

		if errors.IsNotFound(nodeErr) && errors.IsNotFound(nmNodeErr) {
			// The node is not found in both node and nmNode informer, it should have been deleted. return directly
			return false
		}

		// 更新节点 annotation 中 scheudler 的名称
		if err := ns.updateSchedulerNameForNode(node, nmNode); err != nil {
			klog.InfoS("Failed to update the scheduler name for the node", "node", klog.KObj(node), "err", err)
			// don't add it back to the queue directly, wait for another node update event
			// ns.nodeProcessingQueue.Add(nodeInfo)
			return false
		}

		return false
	}
	for {
		if quit := workFunc(); quit {
			klog.InfoS("Shut down the node processing worker queue")
			return
		}
	}
}

// updateSchedulerNameForNode selects one scheduler and updates node annotation
func (ns *NodeShuffler) updateSchedulerNameForNode(node *v1.Node, nmNode *nodev1alpha1.NMNode /*nodeInfo *NodeToBeProcessed*/) error {
	// choose the scheduler with lease number of nodes in its partition directly for the first phase
	selectedSchedulerName := ""
	// 从 schedulers 中获取节点数最少的 scheduler
	if schedulerName, err := ns.chooseOneSchedulerForThisNode(); err != nil {
		return err
	} else {
		selectedSchedulerName = schedulerName
	}
	// 更新节点，将 scheudlerName 信息写入 node annotation
	return ns.updateNodeSchedulerNameAnnotation(node, nmNode, selectedSchedulerName)

	// TODO: add more fine-grained reactions based on node enqueue reasons
	// how to select scheduler, update which one (node or cnr or both)...
	// and maybe we can take the previous scheduler name into account too.
}

// updateNodeSchedulerNameAnnotation updates node annotation
func (ns *NodeShuffler) updateNodeSchedulerNameAnnotation(node *v1.Node, nmNode *nodev1alpha1.NMNode, schedulerName string) error {
	if node != nil && node.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey] != schedulerName {
		nodeClone := node.DeepCopy()
		if nodeClone.Annotations == nil {
			nodeClone.Annotations = make(map[string]string)
		}
		previousScheduler := node.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]
		nodeClone.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey] = schedulerName
		// update node
		if _, err := ns.k8sClient.CoreV1().Nodes().Update(context.TODO(), nodeClone, metav1.UpdateOptions{}); err != nil {
			return err
		}
		if previousScheduler != "" {
			metrics.NodeInPartitionSizeDec(previousScheduler, "node")
		}
		metrics.NodeInPartitionSizeInc(schedulerName, "node")
	}

	if nmNode != nil && nmNode.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey] != schedulerName {
		nmNodeClone := nmNode.DeepCopy()
		if nmNodeClone.Annotations == nil {
			nmNodeClone.Annotations = make(map[string]string)
		}
		previousScheduler := nmNodeClone.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]
		nmNodeClone.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey] = schedulerName
		// update nmNode
		if _, err := ns.crdClient.NodeV1alpha1().NMNodes().Update(context.TODO(), nmNodeClone, metav1.UpdateOptions{}); err != nil {
			return err
		}
		if previousScheduler != "" {
			metrics.NodeInPartitionSizeDec(previousScheduler, "nmnode")
			if node != nil {
				metrics.NodeInPartitionSizeDec(previousScheduler, "hybrid")
			}
		}
		metrics.NodeInPartitionSizeInc(schedulerName, "nmnode")
		if node != nil {
			metrics.NodeInPartitionSizeInc(schedulerName, "hybrid")
		}
	}

	return nil
}

// ReBalanceSchedulerNodes re-balances the number of nodes among all active schedulers if necessary
func (ns *NodeShuffler) ReBalanceSchedulerNodes() {
	metrics.PodShufflingCountInc()
	// go through all the active schedulers
	// shuffle nodes if the loads are not balanced
	// TODO: revisit this and replace it with more accurate and complex logic
	schedulerInfo := ns.schedulerMaintainer.GetSchedulersWithMostAndLeastNumberOfNodes() // 获取节点数最少和最多的 scheduler
	// 如果节点数最多的 scheduler 的节点数大于节点数最少的 scheduler 的节点数的 2 倍，且节点数最多的 scheudler 的节点数大于1，则将节点数最少的 scheduler 的节点数减半
	if schedulerInfo != nil && schedulerInfo.MostNumberOfNodes > schedulerInfo.LeastNumberOfNodes*2 && schedulerInfo.MostNumberOfNodes > 1 {
		// 例1:（5 + 2）/2 - 2 = 1
		// 例2:（13 + 2）/2 - 2 = 5
		numberOfNodesNeedToBeMoved := (schedulerInfo.MostNumberOfNodes+schedulerInfo.LeastNumberOfNodes)/2 - schedulerInfo.LeastNumberOfNodes
		// 从节点数最多的 scheduler 中获取 numberOfNodesNeedToBeMoved 个节点，加入 nodeProcessingQueue，由 nodeProcessingWorker 处理
		nodeNames, err := ns.schedulerMaintainer.GetSomeNodeNamesFromGeneralActiveScheduler(numberOfNodesNeedToBeMoved, schedulerInfo.MostNumberOfNodesSchedulerName)
		if err != nil {
			klog.InfoS("Failed to get nodes from scheduler", "schedulerName", schedulerInfo.MostNumberOfNodesSchedulerName, "err", err)
		} else {
			for _, nodeName := range nodeNames {
				// 将节点从节点数最多的 scheduler 移到节点数最少的 scheduler，后续由 nodeProcessingWorker 处理
				ns.nodeProcessingQueue.Add(&NodeToBeProcessed{
					nodeName: nodeName,
					reason:   TooManyNodesInThisPartition,
				})
			}
		}
	}
}

// SyncUpNodeAndCNR makes sure that node and cnr with same name share the same scheduler name annotation
// TODO: sync up scheduler name in node and cnr
func (ns *NodeShuffler) SyncUpNodeAndCNR() {}
