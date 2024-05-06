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

package nodevolumelimits

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/informers"
	corelisters "k8s.io/client-go/listers/core/v1"
	storagelisters "k8s.io/client-go/listers/storage/v1"
	csitrans "k8s.io/csi-translation-lib"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	volumeutil "github.com/kubewharf/godel-scheduler/pkg/util/volume/util"
)

// InTreeToCSITranslator contains methods required to check migratable status
// and perform translations from InTree PV's to CSI
type InTreeToCSITranslator interface {
	IsPVMigratable(pv *v1.PersistentVolume) bool
	IsMigratableIntreePluginByName(inTreePluginName string) bool
	GetInTreePluginNameFromSpec(pv *v1.PersistentVolume, vol *v1.Volume) (string, error)
	GetCSINameFromInTreeName(pluginName string) (string, error)
	TranslateInTreePVToCSI(pv *v1.PersistentVolume) (*v1.PersistentVolume, error)
}

// CSILimits is a plugin that checks node volume limits.
type CSILimits struct {
	csiNodeLister storagelisters.CSINodeLister
	pvLister      corelisters.PersistentVolumeLister
	pvcLister     corelisters.PersistentVolumeClaimLister
	scLister      storagelisters.StorageClassLister

	randomVolumeIDPrefix string

	translator InTreeToCSITranslator
}

// FitsCSILimits invoked at the filter extension point.
func (pl *CSILimits) FitsCSILimits(ctx context.Context, s *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) *framework.Status {
	var nodeName string

	// If the new pod doesn't have any volume attached to it, the predicate will always be true
	if len(pod.Spec.Volumes) == 0 {
		return nil
	}

	if nodeName = nodeInfo.GetNodeName(); nodeName == "" {
		return framework.NewStatus(framework.Error, "could not found node and cnr")
	}

	// If CSINode doesn't exist, the predicate may read the limits from Node object
	csiNode, err := pl.csiNodeLister.Get(nodeName)
	if err != nil {
		// TODO: return the error once CSINode is created by default (2 releases)
		klog.V(5).InfoS("Failed to get a CSINode object for the node", "err", err)
	}

	newVolumes := make(map[string]string)
	if err := pl.filterAttachableVolumes(csiNode, pod.Spec.Volumes, pod.Namespace, newVolumes); err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	// If the pod doesn't have any new CSI volumes, the predicate will always be true
	if len(newVolumes) == 0 {
		return nil
	}

	// If the node doesn't have volume limits, the predicate will always be true
	nodeVolumeLimits := getVolumeLimits(nodeInfo, csiNode)
	if len(nodeVolumeLimits) == 0 {
		return nil
	}

	attachedVolumes := make(map[string]string)
	for _, existingPod := range nodeInfo.GetPods() {
		if err := pl.filterAttachableVolumes(csiNode, existingPod.Pod.Spec.Volumes, existingPod.Pod.Namespace, attachedVolumes); err != nil {
			return framework.NewStatus(framework.Error, err.Error())
		}
	}

	attachedVolumeCount := map[string]int{}
	for volumeUniqueName, volumeLimitKey := range attachedVolumes {
		if _, ok := newVolumes[volumeUniqueName]; ok {
			// Don't count single volume used in multiple pods more than once
			delete(newVolumes, volumeUniqueName)
		}
		attachedVolumeCount[volumeLimitKey]++
	}

	newVolumeCount := map[string]int{}
	for _, volumeLimitKey := range newVolumes {
		newVolumeCount[volumeLimitKey]++
	}

	for volumeLimitKey, count := range newVolumeCount {
		maxVolumeLimit, ok := nodeVolumeLimits[v1.ResourceName(volumeLimitKey)]
		if ok {
			currentVolumeCount := attachedVolumeCount[volumeLimitKey]
			if currentVolumeCount+count > int(maxVolumeLimit) {
				return framework.NewStatus(framework.Unschedulable, ErrReasonMaxVolumeCountExceeded)
			}
		}
	}

	return nil
}

func (pl *CSILimits) filterAttachableVolumes(
	csiNode *storagev1.CSINode, volumes []v1.Volume, namespace string, result map[string]string,
) error {
	for _, vol := range volumes {
		// CSI volumes can only be used as persistent volumes
		if vol.PersistentVolumeClaim == nil {
			continue
		}
		pvcName := vol.PersistentVolumeClaim.ClaimName

		if pvcName == "" {
			return fmt.Errorf("PersistentVolumeClaim had no name")
		}

		pvc, err := pl.pvcLister.PersistentVolumeClaims(namespace).Get(pvcName)
		if err != nil {
			klog.V(5).InfoS("Failed to look up PVC info", "namespace", namespace, "pvcName", pvcName)
			continue
		}

		driverName, volumeHandle := pl.getCSIDriverInfo(csiNode, pvc)
		if driverName == "" || volumeHandle == "" {
			klog.V(5).InfoS("Failed to find a CSI driver name or volume handle, not counting volume")
			continue
		}

		volumeUniqueName := fmt.Sprintf("%s/%s", driverName, volumeHandle)
		volumeLimitKey := volumeutil.GetCSIAttachLimitKey(driverName)
		result[volumeUniqueName] = volumeLimitKey
	}
	return nil
}

// getCSIDriverInfo returns the CSI driver name and volume ID of a given PVC.
// If the PVC is from a migrated in-tree plugin, this function will return
// the information of the CSI driver that the plugin has been migrated to.
func (pl *CSILimits) getCSIDriverInfo(csiNode *storagev1.CSINode, pvc *v1.PersistentVolumeClaim) (string, string) {
	pvName := pvc.Spec.VolumeName

	if pvName == "" {
		klog.V(5).InfoS("Persistent volume had no name for claim", "PVC", klog.KObj(pvc))
		return pl.getCSIDriverInfoFromSC(csiNode, pvc)
	}

	pv, err := pl.pvLister.Get(pvName)
	if err != nil {
		klog.V(5).InfoS("Failed to look up PV info for PVC and PV", "PVC", klog.KObj(pvc), "pvName", pvName)
		// If we can't fetch PV associated with PVC, may be it got deleted
		// or PVC was prebound to a PVC that hasn't been created yet.
		// fallback to using StorageClass for volume counting
		return pl.getCSIDriverInfoFromSC(csiNode, pvc)
	}

	csiSource := pv.Spec.PersistentVolumeSource.CSI
	if csiSource == nil {
		// We make a fast path for non-CSI volumes that aren't migratable
		if !pl.translator.IsPVMigratable(pv) {
			return "", ""
		}

		pluginName, err := pl.translator.GetInTreePluginNameFromSpec(pv, nil)
		if err != nil {
			klog.V(5).InfoS("Failed to look up plugin name from PV spec", "err", err)
			return "", ""
		}

		if !isCSIMigrationOn(csiNode, pluginName) {
			klog.V(5).InfoS("CSI Migration of plugin was not enabled", "plugin", pluginName)
			return "", ""
		}

		csiPV, err := pl.translator.TranslateInTreePVToCSI(pv)
		if err != nil {
			klog.V(5).InfoS("Failed to translate in-tree volume to CSI", "err", err)
			return "", ""
		}

		if csiPV.Spec.PersistentVolumeSource.CSI == nil {
			klog.V(5).InfoS("Failed to get a valid volume source for translated PV", "PV", klog.KObj(pv))
			return "", ""
		}

		csiSource = csiPV.Spec.PersistentVolumeSource.CSI
	}

	return csiSource.Driver, csiSource.VolumeHandle
}

// getCSIDriverInfoFromSC returns the CSI driver name and a random volume ID of a given PVC's StorageClass.
func (pl *CSILimits) getCSIDriverInfoFromSC(csiNode *storagev1.CSINode, pvc *v1.PersistentVolumeClaim) (string, string) {
	namespace := pvc.Namespace
	pvcName := pvc.Name
	scName := helper.GetPersistentVolumeClaimClass(pvc)

	// If StorageClass is not set or not found, then PVC must be using immediate binding mode
	// and hence it must be bound before scheduling. So it is safe to not count it.
	if scName == "" {
		klog.V(5).InfoS("PVC had no StorageClass", "PVC", klog.KObj(pvc))
		return "", ""
	}

	storageClass, err := pl.scLister.Get(scName)
	if err != nil {
		klog.V(5).InfoS("Failed to get StorageClass for PVC", "PVC", klog.KObj(pvc), "scName", scName, "err", err)
		return "", ""
	}

	// We use random prefix to avoid conflict with volume IDs. If PVC is bound during the execution of the
	// predicate and there is another pod on the same node that uses same volume, then we will overcount
	// the volume and consider both volumes as different.
	volumeHandle := fmt.Sprintf("%s-%s/%s", pl.randomVolumeIDPrefix, namespace, pvcName)

	provisioner := storageClass.Provisioner
	if pl.translator.IsMigratableIntreePluginByName(provisioner) {
		if !isCSIMigrationOn(csiNode, provisioner) {
			klog.V(5).InfoS("CSI Migration of plugin was not enabled", "plugin", provisioner)
			return "", ""
		}

		driverName, err := pl.translator.GetCSINameFromInTreeName(provisioner)
		if err != nil {
			klog.V(5).InfoS("Failed to look up driver name from plugin name", "err", err)
			return "", ""
		}
		return driverName, volumeHandle
	}

	return provisioner, volumeHandle
}

// NewCSILimits initializes a new plugin and returns it.
func NewCSILimits(informerFactory informers.SharedInformerFactory) *CSILimits {
	csiNodeLister := getCSINodeListerIfEnabled(informerFactory)
	pvLister := informerFactory.Core().V1().PersistentVolumes().Lister()
	pvcLister := informerFactory.Core().V1().PersistentVolumeClaims().Lister()
	scLister := informerFactory.Storage().V1().StorageClasses().Lister()
	return &CSILimits{
		csiNodeLister:        csiNodeLister,
		pvLister:             pvLister,
		pvcLister:            pvcLister,
		scLister:             scLister,
		randomVolumeIDPrefix: rand.String(32),
		translator:           csitrans.New(),
	}
}

func getVolumeLimits(nodeInfo framework.NodeInfo, csiNode *storagev1.CSINode) map[v1.ResourceName]int64 {
	// TODO: stop getting values from Node object in v1.18
	volumeLimits := nodeInfo.VolumeLimits()
	if csiNode != nil {
		for i := range csiNode.Spec.Drivers {
			d := csiNode.Spec.Drivers[i]
			if d.Allocatable != nil && d.Allocatable.Count != nil {
				// TODO: drop GetCSIAttachLimitKey once we don't get values from Node object (v1.18)
				k := v1.ResourceName(volumeutil.GetCSIAttachLimitKey(d.Name))
				volumeLimits[k] = int64(*d.Allocatable.Count)
			}
		}
	}
	return volumeLimits
}
