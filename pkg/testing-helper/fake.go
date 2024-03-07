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

package testing_helper

import (
	"fmt"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	schedulingv1a1listers "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	appv1listers "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	policylisters "k8s.io/client-go/listers/policy/v1"
	storagelisters "k8s.io/client-go/listers/storage/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

var _ framework.SharedLister = &fakeSharedLister{}

type fakeSharedLister struct {
	nodeInfos                        []framework.NodeInfo
	nodeInfoMap                      map[string]framework.NodeInfo
	havePodsWithAffinityNodeInfoList []framework.NodeInfo
}

func NewFakeSharedLister(pods []*v1.Pod, nodes []*v1.Node) *fakeSharedLister {
	nodeInfoMap := createNodeInfoMap(pods, nodes)
	nodeInfos := make([]framework.NodeInfo, 0, len(nodeInfoMap))
	havePodsWithAffinityNodeInfoList := make([]framework.NodeInfo, 0, len(nodeInfoMap))
	for _, v := range nodeInfoMap {
		nodeInfos = append(nodeInfos, v)
		if len(v.GetPodsWithAffinity()) > 0 {
			havePodsWithAffinityNodeInfoList = append(havePodsWithAffinityNodeInfoList, v)
		}
	}
	return &fakeSharedLister{
		nodeInfos:                        nodeInfos,
		nodeInfoMap:                      nodeInfoMap,
		havePodsWithAffinityNodeInfoList: havePodsWithAffinityNodeInfoList,
	}
}

func createNodeInfoMap(pods []*v1.Pod, nodes []*v1.Node) map[string]framework.NodeInfo {
	nodeNameToInfo := make(map[string]framework.NodeInfo)
	for _, pod := range pods {
		nodeName := pod.Spec.NodeName
		if _, ok := nodeNameToInfo[nodeName]; !ok {
			nodeNameToInfo[nodeName] = framework.NewNodeInfo()
		}
		nodeNameToInfo[nodeName].AddPod(pod)
	}

	for _, node := range nodes {
		if _, ok := nodeNameToInfo[node.Name]; !ok {
			nodeNameToInfo[node.Name] = framework.NewNodeInfo()
		}
		nodeInfo := nodeNameToInfo[node.Name]
		nodeInfo.SetNode(node)
	}
	return nodeNameToInfo
}

func (f *fakeSharedLister) NodeInfos() framework.NodeInfoLister {
	return f
}

func (f *fakeSharedLister) List() []framework.NodeInfo {
	return f.nodeInfos
}

func (f *fakeSharedLister) InPartitionList() []framework.NodeInfo {
	return f.nodeInfos
}

func (f *fakeSharedLister) OutOfPartitionList() []framework.NodeInfo {
	return f.nodeInfos
}

func (f *fakeSharedLister) HavePodsWithAffinityList() []framework.NodeInfo {
	return f.havePodsWithAffinityNodeInfoList
}

func (s *fakeSharedLister) HavePodsWithRequiredAntiAffinityList() []framework.NodeInfo {
	return nil
}

func (f *fakeSharedLister) Get(nodeName string) (framework.NodeInfo, error) {
	return f.nodeInfoMap[nodeName], nil
}

func (f *fakeSharedLister) GetPreemptorsByVictim(node, victim string) []string {
	return nil
}

type fakeDeploymentNamespaceLister struct {
	dpMap map[string]*appsv1.Deployment
}

func NewFakeDeploymentNamespaceLister(deploys []*appsv1.Deployment) appv1listers.DeploymentNamespaceLister {
	if len(deploys) == 0 {
		return &fakeDeploymentNamespaceLister{}
	} else {
		dpMap := make(map[string]*appsv1.Deployment)
		for _, dp := range deploys {
			dpMap[dp.Name] = dp
		}
		return &fakeDeploymentNamespaceLister{dpMap: dpMap}
	}
}

func (fdnl *fakeDeploymentNamespaceLister) Get(name string) (*appsv1.Deployment, error) {
	if dp, ok := fdnl.dpMap[name]; ok {
		return dp, nil
	} else {
		return nil, fmt.Errorf("dp %s not found.", name)
	}
}

func (fdnl *fakeDeploymentNamespaceLister) List(selector labels.Selector) (ret []*appsv1.Deployment, err error) {
	for _, deploy := range fdnl.dpMap {
		ret = append(ret, deploy)
	}
	return ret, err
}

type fakeDeploymentLister struct {
	dpNsLister appv1listers.DeploymentNamespaceLister
}

func (fdl *fakeDeploymentLister) List(selector labels.Selector) (ret []*appsv1.Deployment, err error) {
	return fdl.dpNsLister.List(selector)
}

// Deployments returns an object that can list and get Deployments.
func (fdl *fakeDeploymentLister) Deployments(namespace string) appv1listers.DeploymentNamespaceLister {
	return fdl.dpNsLister
}

func NewFakeDeploymentLister(deploys []*appsv1.Deployment) appv1listers.DeploymentLister {
	return &fakeDeploymentLister{dpNsLister: NewFakeDeploymentNamespaceLister(deploys)}
}

type fakePodGroupNamespaceLister struct {
	pgMap map[string]*schedulingv1a1.PodGroup
}

func NewFakePodGroupNamespaceLister(podGroups []*schedulingv1a1.PodGroup) schedulingv1a1listers.PodGroupNamespaceLister {
	if len(podGroups) == 0 {
		return &fakePodGroupNamespaceLister{}
	} else {
		pgMap := make(map[string]*schedulingv1a1.PodGroup)
		for _, pg := range podGroups {
			pgMap[pg.Name] = pg
		}
		return &fakePodGroupNamespaceLister{pgMap: pgMap}
	}
}

func (f fakePodGroupNamespaceLister) List(selector labels.Selector) (ret []*schedulingv1a1.PodGroup, err error) {
	for _, pg := range f.pgMap {
		ret = append(ret, pg)
	}
	return ret, err
}

func (f fakePodGroupNamespaceLister) Get(name string) (*schedulingv1a1.PodGroup, error) {
	if pg, ok := f.pgMap[name]; ok {
		return pg, nil
	} else {
		return nil, apierrors.NewNotFound(schedulingv1a1.Resource("podgroup"), name)
	}
}

type fakePodGroupLister struct {
	pgNsLister schedulingv1a1listers.PodGroupNamespaceLister
}

func (f *fakePodGroupLister) List(selector labels.Selector) (ret []*schedulingv1a1.PodGroup, err error) {
	return f.pgNsLister.List(selector)
}

func (f *fakePodGroupLister) PodGroups(namespace string) schedulingv1a1listers.PodGroupNamespaceLister {
	var res []*schedulingv1a1.PodGroup
	pgs, _ := f.pgNsLister.List(labels.Everything())
	for _, pg := range pgs {
		if pg.GetNamespace() == namespace {
			res = append(res, pg)
		}
	}
	return NewFakePodGroupNamespaceLister(res)
}

func NewFakePodGroupLister(podGroups []*schedulingv1a1.PodGroup) schedulingv1a1listers.PodGroupLister {
	return &fakePodGroupLister{pgNsLister: NewFakePodGroupNamespaceLister(podGroups)}
}

type fakePriorityClassLister struct {
	pcMap map[string]*schedulingv1.PriorityClass
}

func NewFakePriorityClassLister(pcs []*schedulingv1.PriorityClass) fakePriorityClassLister {
	lister := fakePriorityClassLister{make(map[string]*schedulingv1.PriorityClass)}
	if len(pcs) == 0 {
		return lister
	}
	for _, pc := range pcs {
		if pc != nil && len(pc.Name) != 0 {
			lister.pcMap[pc.Name] = pc
		}
	}
	return lister
}

func (f fakePriorityClassLister) List(selector labels.Selector) (ret []*schedulingv1.PriorityClass, err error) {
	for _, pc := range f.pcMap {
		ret = append(ret, pc)
	}
	return ret, err
}

func (f fakePriorityClassLister) Get(name string) (*schedulingv1.PriorityClass, error) {
	if len(name) == 0 {
		return nil, fmt.Errorf("empty name.")
	} else if pc, ok := f.pcMap[name]; ok {
		return pc, nil
	} else {
		return nil, fmt.Errorf("can not get PriorityClass :%s", name)
	}
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
	return nil, fmt.Errorf("unable to find storage class: %s", name)
}

// List lists all StorageClass in the indexer.
func (classes StorageClassLister) List(selector labels.Selector) ([]*storagev1.StorageClass, error) {
	return nil, fmt.Errorf("not implemented")
}

func NewFakePodLister(pods []*v1.Pod) corelisters.PodLister {
	objs := make([]runtime.Object, len(pods))
	for i := range pods {
		objs[i] = pods[i]
	}
	cs := fake.NewSimpleClientset(objs...)
	informer := informers.NewSharedInformerFactory(cs, 0)
	for _, pod := range pods {
		informer.Core().V1().Pods().Informer().GetIndexer().Add(pod)
	}
	return informer.Core().V1().Pods().Lister()
}

type fakePodNamespaceLister struct {
	podMap map[string]*v1.Pod
}

func NewFakePodNamespaceLister(pods []*v1.Pod) corelisters.PodNamespaceLister {
	if len(pods) == 0 {
		return &fakePodNamespaceLister{}
	} else {
		podMap := make(map[string]*v1.Pod)
		for _, pod := range pods {
			podMap[pod.Name] = pod
		}
		return &fakePodNamespaceLister{podMap: podMap}
	}
}

func (fpnl *fakePodNamespaceLister) Get(name string) (*v1.Pod, error) {
	if pod, ok := fpnl.podMap[name]; ok {
		return pod, nil
	} else {
		return nil, fmt.Errorf("pod %s not found", name)
	}
}

func (fpnl *fakePodNamespaceLister) List(selector labels.Selector) (ret []*v1.Pod, err error) {
	for _, pod := range fpnl.podMap {
		ret = append(ret, pod)
	}
	return ret, err
}

type fakePodDisruptionBudgetNamespaceLister struct {
	pdbMap map[string]*policy.PodDisruptionBudget
}

func NewFakePodDisruptionBudgetNamespaceLister(pdbs []*policy.PodDisruptionBudget) policylisters.PodDisruptionBudgetNamespaceLister {
	if len(pdbs) == 0 {
		return &fakePodDisruptionBudgetNamespaceLister{}
	} else {
		pdbMap := make(map[string]*policy.PodDisruptionBudget)
		for _, pdb := range pdbs {
			pdbMap[pdb.Name] = pdb
		}
		return &fakePodDisruptionBudgetNamespaceLister{pdbMap: pdbMap}
	}
}

func (fpdbnl *fakePodDisruptionBudgetNamespaceLister) Get(name string) (*policy.PodDisruptionBudget, error) {
	if pdb, ok := fpdbnl.pdbMap[name]; ok {
		return pdb, nil
	} else {
		return nil, fmt.Errorf("pdb %s not found", name)
	}
}

func (fpdbnl *fakePodDisruptionBudgetNamespaceLister) List(selector labels.Selector) (ret []*policy.PodDisruptionBudget, err error) {
	for _, pdb := range fpdbnl.pdbMap {
		ret = append(ret, pdb)
	}
	return ret, err
}

type fakePodDisruptionBudgetLister struct {
	pdbNsLister policylisters.PodDisruptionBudgetNamespaceLister
}

func (fpdbl *fakePodDisruptionBudgetLister) List(selector labels.Selector) (ret []*policy.PodDisruptionBudget, err error) {
	return fpdbl.pdbNsLister.List(selector)
}

func (fpdbl *fakePodDisruptionBudgetLister) PodDisruptionBudgets(namespace string) policylisters.PodDisruptionBudgetNamespaceLister {
	var res []*policy.PodDisruptionBudget
	pdbs, _ := fpdbl.pdbNsLister.List(labels.Everything())
	for _, pdb := range pdbs {
		if pdb.GetNamespace() != namespace {
			continue
		}
		res = append(res, pdb)
	}
	return NewFakePodDisruptionBudgetNamespaceLister(res)
}

func (fpdbl *fakePodDisruptionBudgetLister) GetPodPodDisruptionBudgets(pod *v1.Pod) ([]*policy.PodDisruptionBudget, error) {
	return nil, nil
}

func NewFakePodDisruptionBudgetLister(pdbs []*policy.PodDisruptionBudget) policylisters.PodDisruptionBudgetLister {
	return &fakePodDisruptionBudgetLister{pdbNsLister: NewFakePodDisruptionBudgetNamespaceLister(pdbs)}
}
