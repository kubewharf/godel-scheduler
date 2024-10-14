/*
Copyright 2024 The Godel Scheduler Authors.

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

package reservation

import (
	"context"

	"github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	reservationstore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/reservation_store"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
)

const (
	Name         = "Reservation"
	unitStateKey = "UnitState" + Name
)

type Reservation struct {
	handle       handle.UnitFrameworkHandle
	pluginHandle reservationstore.StoreHandle
}

var (
	_ framework.LocatingPlugin      = &Reservation{}
	_ framework.PreferNodeExtension = &Reservation{}
)

func New(_ runtime.Object, handle handle.UnitFrameworkHandle) (framework.Plugin, error) {
	var pluginHandle reservationstore.StoreHandle
	if ins := handle.FindStore(reservationstore.Name); ins != nil {
		pluginHandle = ins.(reservationstore.StoreHandle)
	}
	return &Reservation{handle: handle, pluginHandle: pluginHandle}, nil
}

func (i *Reservation) Name() string {
	return Name
}

func (i *Reservation) Locating(ctx context.Context, unit framework.ScheduleUnit, unitCycleState *framework.CycleState, nodeGroup framework.NodeGroup) (framework.NodeGroup, *framework.Status) {
	if !utilfeature.DefaultFeatureGate.Enabled(features.ResourceReservation) {
		return nodeGroup, nil
	}
	// TODO: More precise inspection rules.
	if unit.Type() == framework.PodGroupUnitType {
		// We don't support gang reservation for now.
		return nodeGroup, nil
	}

	placeholder := ""
	for _, podInfo := range unit.GetPods() {
		if podInfo == nil || podInfo.Pod == nil {
			continue
		}

		placeholder = podutil.GetReservationPlaceholder(podInfo.Pod)
		if len(placeholder) > 0 {
			break
		}
	}

	if len(placeholder) == 0 {
		return nodeGroup, nil
	}

	availablePlaceholders, err := i.pluginHandle.GetAvailableNodesAndPlaceholders(placeholder)
	if err != nil {
		// metrics.IncReservationAttempt(podProperty, metrics.FailureResult, "no_available_placeholders")
		klog.InfoS("Failed to locate reservation placeholder", "unit", unit.GetKey(), "placeholder", placeholder, "err", err)
		return nodeGroup, nil
	}

	klog.InfoS("Succeed to locate reservation placeholder", "unitKey", unit.GetKey(), "placeholder", placeholder)
	for nodeName := range availablePlaceholders {
		nodeInfo, err := nodeGroup.Get(nodeName)
		if err != nil || nodeInfo == nil {
			continue
		}

		nodeGroup.GetPreferredNodes().Add(nodeInfo, i)
	}

	// TODO: Distribute all placeholders to different nodegroups when podgroup supports reservation.
	unitCycleState.Write(unitStateKey,
		&unitState{
			index:                   availablePlaceholders,
			unavailablePlaceholders: make(map[string]*v1.Pod),
		})

	return nodeGroup, nil
}

func (i *Reservation) PreparePreferNode(ctx context.Context, unitCycleState, state *framework.CycleState, pod *v1.Pod) *framework.Status {
	return nil
}

func (i *Reservation) PrePreferNode(ctx context.Context, unitCycleState, podCycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo) (framework.NodeInfo, *framework.CycleState, *framework.Status) {
	unitState, err := getUnitState(unitCycleState)
	if err != nil || unitState == nil || unitState.index == nil {
		// do nothing.
		return nodeInfo, podCycleState, nil
	}

	// TODO: selected node of lpv
	selectedNode := podutil.GetSelectedNodeOfLpv(i.handle.PvcLister(), pod)
	if selectedNode != "" && selectedNode != nodeInfo.GetNodeName() {
		return nodeInfo, podCycleState, framework.NewStatus(framework.UnschedulableAndUnresolvable, "lpv node not match")
	}

	fakePods, ok := unitState.index[nodeInfo.GetNodeName()]
	if !ok || len(fakePods) == 0 {
		// do nothing.
		return nodeInfo, podCycleState, nil
	}

	// get a placeholder pod
	// TODO: sort reserved pod inorder to reduce failure rate
	var fakePod *v1.Pod = nil
	for _, p := range fakePods {
		if p != nil {
			fakePod = p
			// set matched placeholder pod
			pod.Annotations[podutil.MatchedReservationPlaceholderKey] = podutil.GetPodKey(p)
			delete(fakePods, podutil.GetPodKey(p)) // remove used fakePod
			break
		}
	}

	if fakePod == nil {
		return nodeInfo, podCycleState, nil
	}

	// deep copy & remove fake pod from nodeInfo.
	nodeCopy := nodeInfo.Clone()

	if err = nodeCopy.RemovePod(fakePod, false); err != nil {
		klog.ErrorS(err, "failed to remove fake pod", "placeholder", klog.KObj(fakePod), "node", nodeCopy.GetNodeName())
		delete(pod.Annotations, podutil.MatchedReservationPlaceholderKey)
		return nodeInfo, podCycleState, framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
	}

	podCycleStateCopy := podCycleState.Clone()
	// TODO: Shall we run PreFilterExtensions for podCycleStateCopy?
	// TODO(libing.binacs@): revisit this.

	return nodeCopy, podCycleStateCopy, nil
}

func (i *Reservation) PostPreferNode(ctx context.Context, unitCycleState, podCycleState *framework.CycleState, pod *v1.Pod, nodeInfo framework.NodeInfo, status *framework.Status) *framework.Status {
	if !status.IsSuccess() {
		if ph := podutil.GetReservationPlaceholder(pod); ph == "" {
			return nil
		}

		unitState, err := getUnitState(unitCycleState)
		if err != nil || unitState == nil || unitState.index == nil {
			// do nothing.
			return nil
		}

		key := podutil.GetMatchedReservationPlaceholderPod(pod)
		if key == "" {
			return nil
		}

		delete(pod.Annotations, podutil.MatchedReservationPlaceholderKey)
		unitState.removeUnavailableFakePods(nodeInfo.GetNodeName(), key)
		unitCycleState.Write(unitStateKey, unitState)
		// TODO: set reservation failure in pod cycle state, and move this placeholder to the end in reservation cache store.
	}

	return nil
}
