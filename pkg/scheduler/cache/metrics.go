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

package cache

import (
	v1 "k8s.io/api/core/v1"
	k8smetrics "k8s.io/component-base/metrics"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/util/generationstore"
	utilnode "github.com/kubewharf/godel-scheduler/pkg/util/node"
)

const (
	Guaranteed string = "guaranteed"
	BestEffort string = "besteffort"

	unhealthy string = "unhealthy"
	tainted   string = "tainted"

	// indicate empty sub cluster
	defaultSubCluster string = "-"
	subClusterLabel   string = config.DefaultSubClusterKey
)

// Collectable defines required node resources, currently only Guaranteed and BestEffort are supported.
// This is intended to be designed as a subset of framework.NodeInfo.
type Collectable interface {
	GetGuaranteedAllocatable() *api.Resource
	GetGuaranteedRequested() *api.Resource
	GetBestEffortAllocatable() *api.Resource
	GetBestEffortRequested() *api.Resource
	GetNode() *v1.Node
}

// NodeCollectable composites Collectable and generationstore.StoreObj
type NodeCollectable interface {
	Collectable
	generationstore.StoredObj
}

var (
	_ Collectable               = &nodeResource{}
	_ generationstore.StoredObj = &nodeResource{}
)

// nodeResource implements NodeCollectable, holds the real data
type nodeResource struct {
	guaranteedAllocatable *api.Resource
	guaranteedRequested   *api.Resource
	bestEffortAllocatable *api.Resource
	bestEffortRequested   *api.Resource

	node *v1.Node

	generation int64
}

func newNodeResource() *nodeResource {
	return &nodeResource{
		guaranteedAllocatable: &api.Resource{},
		guaranteedRequested:   &api.Resource{},
		bestEffortRequested:   &api.Resource{},
		bestEffortAllocatable: &api.Resource{},
	}
}

func Clone(c NodeCollectable) NodeCollectable {
	return &nodeResource{
		guaranteedAllocatable: c.GetGuaranteedAllocatable().Clone(),
		guaranteedRequested:   c.GetGuaranteedRequested().Clone(),
		bestEffortAllocatable: c.GetGuaranteedAllocatable().Clone(),
		bestEffortRequested:   c.GetBestEffortRequested().Clone(),
		generation:            c.GetGeneration(),
		node:                  c.GetNode().DeepCopy(),
	}
}

func (n *nodeResource) GetGuaranteedAllocatable() *api.Resource {
	return n.guaranteedAllocatable
}

func (n *nodeResource) GetGuaranteedRequested() *api.Resource {
	return n.guaranteedRequested
}

func (n *nodeResource) GetBestEffortAllocatable() *api.Resource {
	return n.bestEffortAllocatable
}

func (n *nodeResource) GetBestEffortRequested() *api.Resource {
	return n.bestEffortRequested
}

func (n *nodeResource) GetGeneration() int64 {
	return n.generation
}

func (n *nodeResource) SetGeneration(generation int64) {
	if n != nil {
		n.generation = generation
	}
}

func (n *nodeResource) GetNode() *v1.Node {
	return n.node
}

// ClusterCollectable aggregates all the resource metrics we collected and
// flush them to the Prometheus.
type ClusterCollectable struct {
	generationstore.RawStore
	owner          string
	unhealthyNodes int
	taintedNodes   int
}

func NewEmptyClusterCollectable(owner string) *ClusterCollectable {
	return &ClusterCollectable{
		RawStore: generationstore.NewRawStore(),
		owner:    owner,
	}
}

func (c ClusterCollectable) UpdateMetrics() {
	c.aggregateNodeCollectable()
}

func getSubCluster(labels map[string]string) string {
	if labels == nil || labels[subClusterLabel] == "" {
		return defaultSubCluster
	}
	return labels[subClusterLabel]
}

// aggregateNodeCollectable sums all the NodeCollectables
func (c ClusterCollectable) aggregateNodeCollectable() {
	subClusterMetrics := make(map[string]*nodeResource, 0)
	c.unhealthyNodes = 0
	c.taintedNodes = 0

	for _, key := range c.Keys() {
		store := c.Get(key)
		if store == nil {
			continue
		}

		if collectable, ok := store.(Collectable); ok {
			node := collectable.GetNode()

			// if the node is unschedulable, we will not record the metrics
			if !utilnode.Scheduable(node) {
				c.unhealthyNodes++
				continue
			}

			if utilnode.Tainted(node) {
				c.taintedNodes++
			}

			subCluster := getSubCluster(node.Labels)
			clusterMetrics, ok := subClusterMetrics[subCluster]
			if !ok {
				clusterMetrics = newNodeResource()
				subClusterMetrics[subCluster] = clusterMetrics
			}

			clusterMetrics.guaranteedRequested.AddResource(collectable.GetGuaranteedRequested())
			clusterMetrics.guaranteedAllocatable.AddResource(collectable.GetGuaranteedAllocatable())
			clusterMetrics.bestEffortAllocatable.AddResource(collectable.GetBestEffortAllocatable())
			clusterMetrics.bestEffortRequested.AddResource(collectable.GetBestEffortRequested())
		}
	}

	for subCluster, clusterMetrics := range subClusterMetrics {
		c.recordMetrics(metrics.ClusterAllocatable, subCluster, Guaranteed, clusterMetrics.GetGuaranteedAllocatable())
		c.recordMetrics(metrics.ClusterAllocatable, subCluster, BestEffort, clusterMetrics.GetBestEffortAllocatable())
		c.recordMetrics(metrics.ClusterPodRequested, subCluster, Guaranteed, clusterMetrics.GetGuaranteedRequested())
		c.recordMetrics(metrics.ClusterPodRequested, subCluster, BestEffort, clusterMetrics.GetBestEffortRequested())
	}

	metrics.NodeCounter.WithLabelValues(unhealthy, c.owner).Set(float64(c.unhealthyNodes))
	metrics.NodeCounter.WithLabelValues(tainted, c.owner).Set(float64(c.taintedNodes))
}

// recordMetrics flush all the metrics to the Prometheus
func (c ClusterCollectable) recordMetrics(metric *k8smetrics.GaugeVec, subCluster string, qos string, resource *api.Resource) {
	metric.WithLabelValues(subCluster, qos, "cpu", c.owner).Set(float64(resource.MilliCPU))
	metric.WithLabelValues(subCluster, qos, "memory", c.owner).Set(float64(resource.Memory))
	metric.WithLabelValues(subCluster, qos, "ephemeral-storage", c.owner).Set(float64(resource.EphemeralStorage))
	for rName, rQuant := range resource.ScalarResources {
		metric.WithLabelValues(subCluster, qos, string(rName), c.owner).Set(float64(rQuant))
	}
}

// --------------------- node metrics ---------------------

type cacheMetrics struct {
	// number of nodes in partition
	nodeInPartitionCount int64
	// number of nodes in total
	nodeTotalCount int64
	// number of nmnodes in partition
	nmnodeInPartitionCount int64
	// number of nmnodes in total
	nmnodeTotalCount int64
	// number of hosts having in partition node and in partition nmnode
	hybridHostInPartitionCount int64
	// number of hosts having node and nmnode
	hybridHostTotalCount int64
}

func newCacheMetrics() *cacheMetrics {
	return &cacheMetrics{}
}

func (m *cacheMetrics) update(n framework.NodeInfo, delta int64) {
	if n == nil {
		return
	}
	if n.GetNode() == nil && n.GetNMNode() == nil {
		klog.InfoS("WARN: Unexpected NodeInfo without Node and NMNode")
		return
	}
	if n.GetNMNode() == nil {
		m.nodeTotalCount += delta
		if n.GetNodeInSchedulerPartition() {
			m.nodeInPartitionCount += delta
		}
	} else if n.GetNode() == nil {
		m.nmnodeTotalCount += delta
		if n.GetNMNodeInSchedulerPartition() {
			m.nmnodeInPartitionCount += delta
		}
	} else {
		m.hybridHostTotalCount += delta
		if n.GetNodeInSchedulerPartition() || n.GetNMNodeInSchedulerPartition() {
			m.hybridHostInPartitionCount += delta
		}
	}
}
