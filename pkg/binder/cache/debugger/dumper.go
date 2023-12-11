/*
Copyright 2019 The Kubernetes Authors.

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

package debugger

import (
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"

	godelcache "github.com/kubewharf/godel-scheduler/pkg/binder/cache"
	godelqueue "github.com/kubewharf/godel-scheduler/pkg/binder/queue"
	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
	schedutil "github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/features"
)

type CacheDumper struct {
	cache    godelcache.BinderCache
	podQueue godelqueue.BinderQueue
}

// DumpAll writes cached nodes and scheduling queue information to the scheduler logs.
func (d *CacheDumper) DumpAll() {
	d.dumpNodes()
	d.dumpSchedulingQueue()
}

// dumpNodes writes NodeInfo to the scheduler logs.
func (d *CacheDumper) dumpNodes() {
	dump := d.cache.Dump()
	klog.InfoS("Dump of cached NodeInfo")
	klog.InfoS("The number of nodes which were cached in scheduler cache", "numberOfNodes", len(dump.Nodes))
	for name, nodeInfo := range dump.Nodes {
		klog.InfoS(d.printNodeInfo(name, nodeInfo))
	}
}

// dumpSchedulingQueue writes pods in the scheduling queue to the scheduler logs.
func (d *CacheDumper) dumpSchedulingQueue() {
	var podData strings.Builder
	pendingPods := d.podQueue.PendingPods()
	klog.InfoS("Dump of scheduling queue")
	klog.InfoS("The number of  pods which were cached in the scheduling queue", "numberOfPods", len(pendingPods))
	for _, p := range pendingPods {
		podData.WriteString(printPod(p))
	}
	klog.InfoS(podData.String())
}

// printNodeInfo writes parts of NodeInfo to a string.
func (d *CacheDumper) printNodeInfo(name string, n api.NodeInfo) string {
	// the output would be like
	// "[<INFO header>] Node name: xxx
	//  Deleted: false
	//  Guaranteed Requested Resources:xxx
	//  Guaranteed Allocatable Resources: xxx
	//  Best-effort Requested Resources: xxx
	//  Best-effort Allocatable Resources: xxx
	//  Scheduled Pods(number: %v): xxx"
	//  follow by some numa information.
	var nodeData strings.Builder
	nodeData.WriteString(fmt.Sprintf("\nNode name: %s\nDeleted: %t\n "+
		"Gauranteed Requested Resources: %+v\nGauranteed Allocatable Resources:%+v\n"+
		"Best-effort Requested Resources: %+v\nBest-effort Allocatable Resources:%+v\n"+
		"Scheduled Pods(number: %v):\n",
		name, n.GetNode() == nil,
		n.GetGuaranteedRequested(), n.GetGuaranteedAllocatable(),
		n.GetBestEffortRequested(), n.GetBestEffortAllocatable(), n.NumPods()))

	// Dump node's numa topology information
	numaTopologyStatus := n.GetNumaTopologyStatus()
	if numaTopologyStatus != nil {
		sharedCoresRequest := n.GetResourcesRequestsOfSharedCoresPods()
		nodeData.WriteString(fmt.Sprintf("Shared Cores Request: %+v\n", sharedCoresRequest))
		sharedCoresAvailable := n.GetResourcesAvailableForSharedCoresPods(nil)
		nodeData.WriteString(fmt.Sprintf("Shared Cores Available: %+v\n", sharedCoresAvailable))
		numaStatus := numaTopologyStatus.GetNumaTopology()
		for numa := range numaStatus {
			stat := numaStatus[numa]
			nodeData.WriteString(fmt.Sprintf("Numa Id: %v\n", numa))
			nodeData.WriteString(stat.GetNumaInfo())
		}
	}

	// Dumping Pod Info
	for _, p := range n.GetPods() {
		nodeData.WriteString(printPod(p.Pod))
	}

	return nodeData.String()
}

// printPod writes parts of a Pod object to a string.
func printPod(p *v1.Pod) string {
	var podInfoString string
	podInfoString = fmt.Sprintf("name: %v, namespace: %v, uid: %v, phase: %v, nominated node: %v\n", p.Name, p.Namespace, p.UID, p.Status.Phase, p.Status.NominatedNodeName)
	request := resourceToValueMap{
		v1.ResourceCPU:              0,
		v1.ResourceMemory:           0,
		v1.ResourceStorage:          0,
		v1.ResourceEphemeralStorage: 0,
	}
	podInfoString += "ResourceRequest:\n"
	for resource := range request {
		request[resource] = calculatePodResourceRequest(p, resource)
		podInfoString += fmt.Sprintf("%v:%v\n", resource, request[resource])
	}
	return podInfoString
}

type resourceToValueMap map[v1.ResourceName]int64

// calculatePodResourceRequest returns the total non-zero requests. If Overhead is defined for the pod and the
// PodOverhead feature is enabled, the Overhead is added to the result.
// podResourceRequest = max(sum(podSpec.Containers), podSpec.InitContainers) + overHead
func calculatePodResourceRequest(pod *v1.Pod, resource v1.ResourceName) int64 {
	var podRequest int64
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		value := schedutil.GetNonzeroRequestForResource(resource, &container.Resources.Requests)
		podRequest += value
	}
	for i := range pod.Spec.InitContainers {
		initContainer := &pod.Spec.InitContainers[i]
		value := schedutil.GetNonzeroRequestForResource(resource, &initContainer.Resources.Requests)
		if podRequest < value {
			podRequest = value
		}
	}
	// If Overhead is being utilized, add to the total requests for the pod
	if pod.Spec.Overhead != nil && utilfeature.DefaultFeatureGate.Enabled(features.PodOverhead) {
		if quantity, found := pod.Spec.Overhead[resource]; found {
			podRequest += quantity.Value()
		}
	}
	return podRequest
}
