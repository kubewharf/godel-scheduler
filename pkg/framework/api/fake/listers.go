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

package fake

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	storagelisters "k8s.io/client-go/listers/storage/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

var _ corelisters.ServiceLister = &ServiceLister{}

// ServiceLister implements ServiceLister on []v1.Service for test purposes.
type ServiceLister []*v1.Service

// Services returns nil.
func (f ServiceLister) Services(namespace string) corelisters.ServiceNamespaceLister {
	var services []*v1.Service
	for i := range f {
		if f[i].Namespace == namespace {
			services = append(services, f[i])
		}
	}
	return &serviceNamespaceLister{
		services:  services,
		namespace: namespace,
	}
}

// List returns v1.ServiceList, the list of all services.
func (f ServiceLister) List(labels.Selector) ([]*v1.Service, error) {
	return f, nil
}

// serviceNamespaceLister is implementation of ServiceNamespaceLister returned by Services() above.
type serviceNamespaceLister struct {
	services  []*v1.Service
	namespace string
}

func (f *serviceNamespaceLister) Get(name string) (*v1.Service, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *serviceNamespaceLister) List(selector labels.Selector) ([]*v1.Service, error) {
	return f.services, nil
}

var _ appslisters.StatefulSetLister = &StatefulSetLister{}

// StatefulSetLister implements ControllerLister on []appsv1.StatefulSet for testing purposes.
type StatefulSetLister []*appsv1.StatefulSet

// List returns stateful sets.
func (f StatefulSetLister) List(labels.Selector) ([]*appsv1.StatefulSet, error) {
	return f, nil
}

// GetPodStatefulSets gets the StatefulSets that have the selector that match the labels on the given pod.
func (f StatefulSetLister) GetPodStatefulSets(pod *v1.Pod) (sss []*appsv1.StatefulSet, err error) {
	var selector labels.Selector

	for _, ss := range f {
		if ss.Namespace != pod.Namespace {
			continue
		}
		selector, err = metav1.LabelSelectorAsSelector(ss.Spec.Selector)
		if err != nil {
			return
		}
		if selector.Matches(labels.Set(pod.Labels)) {
			sss = append(sss, ss)
		}
	}
	if len(sss) == 0 {
		err = fmt.Errorf("could not find StatefulSet for pod %s in namespace %s with labels: %v", pod.Name, pod.Namespace, pod.Labels)
	}
	return
}

// StatefulSets returns nil
func (f StatefulSetLister) StatefulSets(namespace string) appslisters.StatefulSetNamespaceLister {
	return nil
}

// persistentVolumeClaimNamespaceLister is implementation of PersistentVolumeClaimNamespaceLister returned by List() above.
type persistentVolumeClaimNamespaceLister struct {
	pvcs      []*v1.PersistentVolumeClaim
	namespace string
}

func (f *persistentVolumeClaimNamespaceLister) Get(name string) (*v1.PersistentVolumeClaim, error) {
	for _, pvc := range f.pvcs {
		if pvc.Name == name && pvc.Namespace == f.namespace {
			return pvc, nil
		}
	}
	return nil, fmt.Errorf("persistentvolumeclaim %q not found", name)
}

func (f persistentVolumeClaimNamespaceLister) List(selector labels.Selector) (ret []*v1.PersistentVolumeClaim, err error) {
	return nil, fmt.Errorf("not implemented")
}

// PersistentVolumeClaimLister declares a []v1.PersistentVolumeClaim type for testing.
type PersistentVolumeClaimLister []v1.PersistentVolumeClaim

var _ corelisters.PersistentVolumeClaimLister = PersistentVolumeClaimLister{}

// List gets PVC matching the namespace and PVC ID.
func (pvcs PersistentVolumeClaimLister) List(selector labels.Selector) (ret []*v1.PersistentVolumeClaim, err error) {
	return nil, fmt.Errorf("not implemented")
}

// PersistentVolumeClaims returns a fake PersistentVolumeClaimLister object.
func (pvcs PersistentVolumeClaimLister) PersistentVolumeClaims(namespace string) corelisters.PersistentVolumeClaimNamespaceLister {
	ps := make([]*v1.PersistentVolumeClaim, len(pvcs))
	for i := range pvcs {
		ps[i] = &pvcs[i]
	}
	return &persistentVolumeClaimNamespaceLister{
		pvcs:      ps,
		namespace: namespace,
	}
}

// NodeInfoLister declares a []framework.NodeInfo type for testing.
type NodeInfoLister []framework.NodeInfo

// Get returns a fake node object in the fake nodes.
func (nodes NodeInfoLister) Get(nodeName string) (framework.NodeInfo, error) {
	for _, node := range nodes {
		if node != nil && node.GetNodeName() == nodeName {
			return node, nil
		}
	}
	return nil, fmt.Errorf("unable to find node: %s", nodeName)
}

// List lists all nodes.
func (nodes NodeInfoLister) List() []framework.NodeInfo {
	return nodes
}

// Returns the list of NodeInfos in the partition of the scheduler
func (nodes NodeInfoLister) InPartitionList() []framework.NodeInfo {
	return nodes
}

// Returns the list of NodeInfos out of the partition of the scheduler
func (nodes NodeInfoLister) OutOfPartitionList() []framework.NodeInfo {
	return nodes
}

// HavePodsWithAffinityList is supposed to list nodes with at least one pod with affinity. For the fake lister
// we just return everything.
func (nodes NodeInfoLister) HavePodsWithAffinityList() []framework.NodeInfo {
	return nodes
}

// HavePodsWithRequiredAntiAffinityList is supposed to list nodes with at least one pod with
// required anti-affinity. For the fake lister we just return everything.
func (nodes NodeInfoLister) HavePodsWithRequiredAntiAffinityList() []framework.NodeInfo {
	return nodes
}

func (nodes NodeInfoLister) Len() int {
	return len(nodes)
}

// NewNodeInfoLister create a new fake NodeInfoLister from a slice of v1.Nodes.
func NewNodeInfoLister(nodes []*v1.Node) framework.NodeInfoLister {
	nodeInfoList := make([]framework.NodeInfo, 0, len(nodes))
	for _, node := range nodes {
		nodeInfo := framework.NewNodeInfo()
		nodeInfo.SetNode(node)
		nodeInfoList = append(nodeInfoList, nodeInfo)
	}

	return NodeInfoLister(nodeInfoList)
}

var _ storagelisters.CSINodeLister = CSINodeLister{}

// CSINodeLister declares a storagev1.CSINode type for testing.
type CSINodeLister storagev1.CSINode

// Get returns a fake CSINode object.
func (n CSINodeLister) Get(name string) (*storagev1.CSINode, error) {
	csiNode := storagev1.CSINode(n)
	return &csiNode, nil
}

// List lists all CSINodes in the indexer.
func (n CSINodeLister) List(selector labels.Selector) (ret []*storagev1.CSINode, err error) {
	return nil, fmt.Errorf("not implemented")
}

// PersistentVolumeLister declares a []v1.PersistentVolume type for testing.
type PersistentVolumeLister []v1.PersistentVolume

var _ corelisters.PersistentVolumeLister = PersistentVolumeLister{}

// Get returns a fake PV object in the fake PVs by PV ID.
func (pvs PersistentVolumeLister) Get(pvID string) (*v1.PersistentVolume, error) {
	for _, pv := range pvs {
		if pv.Name == pvID {
			return &pv, nil
		}
	}
	return nil, fmt.Errorf("Unable to find persistent volume: %s", pvID)
}

// List lists all PersistentVolumes in the indexer.
func (pvs PersistentVolumeLister) List(selector labels.Selector) ([]*v1.PersistentVolume, error) {
	return nil, fmt.Errorf("not implemented")
}

// StorageClassLister declares a []storagev1.StorageClass type for testing.
type StorageClassLister []storagev1.StorageClass

var _ storagelisters.StorageClassLister = StorageClassLister{}

// Get returns a fake storage class object in the fake storage classes by name.
func (classes StorageClassLister) Get(name string) (*storagev1.StorageClass, error) {
	for _, sc := range classes {
		if sc.Name == name {
			return &sc, nil
		}
	}
	return nil, fmt.Errorf("Unable to find storage class: %s", name)
}

// List lists all StorageClass in the indexer.
func (classes StorageClassLister) List(selector labels.Selector) ([]*storagev1.StorageClass, error) {
	return nil, fmt.Errorf("not implemented")
}
