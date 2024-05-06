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
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
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
