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

package framework_helper

import (
	"time"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
)

type NodeInfoWrapper struct {
	framework.NodeInfo
}

func MakeNodeInfo() *NodeInfoWrapper {
	nodeInfo := framework.NewNodeInfo()
	node := v1.Node{}
	nodeInfo.SetNode(&node)
	cnr := katalystv1alpha1.CustomNodeResource{}
	nodeInfo.SetCNR(&cnr)
	return &NodeInfoWrapper{nodeInfo}
}

func (n *NodeInfoWrapper) Obj() framework.NodeInfo {
	return n.NodeInfo
}

func (n *NodeInfoWrapper) Name(s string) *NodeInfoWrapper {
	n.NodeInfo.GetNode().SetName(s)
	n.NodeInfo.GetCNR().SetName(s)
	return n
}

// Label applies a {k,v} label pair to the inner node.
func (n *NodeInfoWrapper) Label(k, v string) *NodeInfoWrapper {
	node := n.GetNode()
	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}
	node.Labels[k] = v
	return n
}

// Capacity sets the capacity and the allocatable resources of the inner node.
// Each entry in `resources` corresponds to a resource name and its quantity.
// By default, the capacity and allocatable number of pods are set to 32.
func (n *NodeInfoWrapper) Capacity(resources map[v1.ResourceName]string) *NodeInfoWrapper {
	res := v1.ResourceList{
		v1.ResourcePods: resource.MustParse("32"),
	}
	for name, value := range resources {
		res[name] = resource.MustParse(value)
	}

	node := n.GetNode()
	node.Status.Capacity, node.Status.Allocatable = res, res
	n.SetNode(node)
	return n
}

func (n *NodeInfoWrapper) CNRCapacity(resources map[v1.ResourceName]string) *NodeInfoWrapper {
	res := v1.ResourceList{
		v1.ResourcePods: resource.MustParse("32"),
	}
	for name, value := range resources {
		res[name] = resource.MustParse(value)
	}
	cnr := n.GetCNR()
	cnr.Status.Resources.Capacity, cnr.Status.Resources.Allocatable = &res, &res
	n.SetCNR(cnr)
	return n
}

func WithNode(node *v1.Node) framework.NodeInfo {
	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)
	return nodeInfo
}

func WithNMNode(nmNode *nodev1alpha1.NMNode) framework.NodeInfo {
	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNMNode(nmNode)
	return nodeInfo
}

func MakeSnapShot(existingPods []*v1.Pod, nodes []*v1.Node, nmNodes []*nodev1alpha1.NMNode) *godelcache.Snapshot {
	cache := godelcache.New(commoncache.MakeCacheHandlerWrapper().
		ComponentName("").SchedulerType("").SubCluster(framework.DefaultSubCluster).
		PodAssumedTTL(time.Second).Period(10 * time.Second).StopCh(make(<-chan struct{})).
		EnableStore("PreemptionStore").
		Obj())
	snapshot := godelcache.NewEmptySnapshot(commoncache.MakeCacheHandlerWrapper().
		SubCluster(framework.DefaultSubCluster).SwitchType(framework.DefaultSubClusterSwitchType).
		EnableStore("PreemptionStore").
		Obj())

	for _, pod := range existingPods {
		pod.UID = types.UID(pod.Name)
		cache.AddPod(pod)
	}
	for _, node := range nodes {
		// WithNode(node)
		cache.AddNode(node)
	}
	for _, nmNode := range nmNodes {
		// WithNMNode(nmNode)
		cache.AddNMNode(nmNode)
	}

	cache.UpdateSnapshot(snapshot)

	return snapshot
}
