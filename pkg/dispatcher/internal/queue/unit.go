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

package queue

import (
	"fmt"
	"sync"
	"time"

	"github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/dispatcher/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
)

type UnitInfos interface {
	AddPodGroup(pg *v1alpha1.PodGroup)
	UpdatePodGroup(oldPG, newPG *v1alpha1.PodGroup)
	DeletePodGroup(pg *v1alpha1.PodGroup)
	AddPod(unitKey string, podKey string)
	DeletePod(unitKey string, podKey string)
	AddUnSortedPodInfo(unitKey string, podInfo *QueuedPodInfo)
	DeleteUnSortedPodInfo(unitKey string, podInfo *QueuedPodInfo)
	Pop() (*QueuedPodInfo, error)
	Enqueue(podInfo *QueuedPodInfo)
	GetAssignedSchedulerForPodGroupUnit(pg *v1alpha1.PodGroup) string
	AssignSchedulerToPodGroupUnit(pg *v1alpha1.PodGroup, schedName string, forceUpdate bool) error
	Run(stop <-chan struct{})
}

type unitInfos struct {
	sync.Mutex

	units map[string]*unitInfo

	readyUnitPods *cache.FIFO

	recorder events.EventRecorder
}

var _ UnitInfos = &unitInfos{}

func NewUnitInfos(recorder events.EventRecorder) UnitInfos {
	return &unitInfos{
		units:         make(map[string]*unitInfo),
		readyUnitPods: cache.NewFIFO(simpleKeyFunc),
		recorder:      recorder,
	}
}

func simpleKeyFunc(obj interface{}) (string, error) {
	return obj.(*QueuedPodInfo).PodKey, nil
}

func (uis *unitInfos) Run(stop <-chan struct{}) {
	go wait.Until(uis.populate, 30*time.Second, stop)
}

func syncPendingMetricsFactory() func(ui *unitInfo) {
	// used to count unsorted pods
	type PodMetricsLabels struct {
		Qos        string
		SubCluster string
	}

	// used to count unsorted unit
	type UnitMetricsLabels struct {
		PodMetricsLabels
		unitType string
	}

	pendingPodsCounter := make(map[PodMetricsLabels]float64)
	pendingUnitsCounter := make(map[UnitMetricsLabels]float64)

	return func(ui *unitInfo) {
		unitProperty := ui.GetUnitProperty()
		if unitProperty == nil {
			return
		}

		uip := unitProperty.(*unitInfoProperty)
		podMetricsLabels := PodMetricsLabels{string(uip.Qos), uip.SubCluster}
		pendingPodsCounter[podMetricsLabels] += float64(len(ui.unSortedPods))
		metrics.PendingPodsSet(uip.GetPodProperty(), "waiting", pendingPodsCounter[podMetricsLabels])

		t := 0.0 // We add 0 to the metric when the unit has no pods pending here. Otherwise, the metric cannot decrease from 1 to 0.
		if len(ui.unSortedPods) > 0 {
			t = 1.0
		}

		unitMetricsLabels := UnitMetricsLabels{podMetricsLabels, uip.UnitType}
		pendingUnitsCounter[unitMetricsLabels] += t
		metrics.PendingUnitsSet(uip, "waiting", pendingUnitsCounter[unitMetricsLabels])
	}
}

func (uis *unitInfos) populate() {
	uis.Lock()
	defer uis.Unlock()

	// TODO: remove this func after refactor dispatcher Queue unit
	syncPendingMetrics := syncPendingMetricsFactory()
	for _, ui := range uis.units {
		message, isReady := ui.readyToBeDispatched()
		if isReady {
			uis.movePodsToReadyQueue(ui)
		} else {
			podGroup := ui.podGroup
			if podGroup != nil {
				uis.recorder.Eventf(podGroup, nil, v1.EventTypeNormal, "PeriodicallyCheckDispatchReadiness", "CheckDispatchReadiness", message)
			}
		}
		syncPendingMetrics(ui)
	}
}

// this is a private function, we assume the lock is acquired
func (uis *unitInfos) movePodsToReadyQueue(ui *unitInfo) {
	if len(ui.unSortedPods) == 0 {
		return
	}

	unitProperty := ui.GetUnitProperty()
	for key, podInfo := range ui.unSortedPods {
		klog.V(3).InfoS("DEBUG: added pod to ready queue", "pod", podInfo.PodKey)
		if err := uis.readyUnitPods.Add(podInfo); err == nil {
			delete(ui.unSortedPods, key)
		} else {
			klog.InfoS("DEBUG: error occurred when adding pod to ready queue", "pod", podInfo.PodKey, "err", err)
		}
	}

	metrics.UnitPendingDurationObserve(unitProperty, "waiting", helper.SinceInSeconds(ui.waitingTimestamp))
	ui.waitingTimestamp = time.Now()
}

// TODO: revisit this later
func generateUnitKeyFromPodGroup(pg *v1alpha1.PodGroup) string {
	return pg.Namespace + "/" + pg.Name
}

func (uis *unitInfos) AddPodGroup(pg *v1alpha1.PodGroup) {
	uis.Lock()
	defer uis.Unlock()

	uis.addPodGroup(pg)
}

func (uis *unitInfos) addPodGroup(pg *v1alpha1.PodGroup) {
	unitKey := generateUnitKeyFromPodGroup(pg)
	ui, ok := uis.units[unitKey]
	if !ok {
		ui = NewUnitInfo()
		uis.units[unitKey] = ui
	}
	ui.podGroup = pg

	message, isReady := uis.units[unitKey].readyToBeDispatched()
	if isReady {
		uis.movePodsToReadyQueue(uis.units[unitKey])
	} else {
		podGroup := uis.units[unitKey].podGroup
		if podGroup != nil {
			uis.recorder.Eventf(
				podGroup, nil, v1.EventTypeNormal, "AddOrUpdatePodGroup", "CheckDispatchReadiness", message)
		}
	}
}

func (uis *unitInfos) UpdatePodGroup(oldPG, newPG *v1alpha1.PodGroup) {
	uis.Lock()
	defer uis.Unlock()

	uis.addPodGroup(newPG)
}

func (uis *unitInfos) DeletePodGroup(pg *v1alpha1.PodGroup) {
	uis.Lock()
	defer uis.Unlock()

	unitKey := generateUnitKeyFromPodGroup(pg)
	ui, ok := uis.units[unitKey]
	if !ok {
		klog.InfoS("Failed to find podGroup in unit info", "podGroup", klog.KObj(pg))
		return
	}

	ui.podGroup = nil
	if len(ui.pods) == 0 {
		delete(uis.units, unitKey)
	}
}

func (uis *unitInfos) AddPod(unitKey string, podKey string) {
	uis.Lock()
	defer uis.Unlock()

	if len(unitKey) > 0 {
		if uis.units[unitKey] == nil {
			uis.units[unitKey] = NewUnitInfo()
		}
		uis.units[unitKey].pods[podKey] = struct{}{}

		message, isReady := uis.units[unitKey].readyToBeDispatched()
		if isReady {
			uis.movePodsToReadyQueue(uis.units[unitKey])
		} else {
			podGroup := uis.units[unitKey].podGroup
			if podGroup != nil {
				uis.recorder.Eventf(podGroup, nil, v1.EventTypeNormal, "AddPodKey",
					"CheckDispatchReadiness", fmt.Sprintf("message: %s, pod: %s", message, podKey))
			}
		}
	}
}

func (uis *unitInfos) AddUnSortedPodInfo(unitKey string, podInfo *QueuedPodInfo) {
	uis.Lock()
	defer uis.Unlock()

	if len(unitKey) > 0 {
		if uis.units[unitKey] == nil {
			uis.units[unitKey] = NewUnitInfo()
		}
		uis.units[unitKey].unSortedPods[podInfo.PodKey] = podInfo
		uis.units[unitKey].pods[podInfo.PodKey] = struct{}{}

		message, isReady := uis.units[unitKey].readyToBeDispatched()
		if isReady {
			klog.V(5).InfoS("DEBUG: scheduling unit is ready to be dispatched", "unitKey", unitKey)
			uis.movePodsToReadyQueue(uis.units[unitKey])
		} else {
			klog.V(5).InfoS("DEBUG: scheduling unit is not ready to be dispatched", "unitKey", unitKey)
			podGroup := uis.units[unitKey].podGroup
			if podGroup != nil {
				uis.recorder.Eventf(podGroup, nil, v1.EventTypeNormal, "AddPodInfo", "CheckDispatchReadiness",
					fmt.Sprintf("message: %s, pod: %s", message, podInfo.PodKey))
			}
		}
	}
}

func (uis *unitInfos) DeleteUnSortedPodInfo(unitKey string, podInfo *QueuedPodInfo) {
	uis.Lock()
	defer uis.Unlock()

	if len(unitKey) > 0 && uis.units[unitKey] != nil {
		delete(uis.units[unitKey].unSortedPods, podInfo.PodKey)
	}
}

func (uis *unitInfos) DeletePod(unitKey string, podKey string) {
	uis.Lock()
	defer uis.Unlock()

	if len(unitKey) > 0 && uis.units[unitKey] != nil {
		delete(uis.units[unitKey].pods, podKey)
		delete(uis.units[unitKey].unSortedPods, podKey)

		if len(uis.units[unitKey].pods) == 0 && uis.units[unitKey].podGroup == nil {
			delete(uis.units, unitKey)
		}
	}
}

func (uis *unitInfos) Pop() (*QueuedPodInfo, error) {
	result, err := uis.readyUnitPods.Pop(func(obj interface{}) error { return nil })
	if err == cache.ErrFIFOClosed {
		return nil, fmt.Errorf("ready unit pods FIFO queue closed")
	}

	return result.(*QueuedPodInfo), err
}

func (uis *unitInfos) Enqueue(podInfo *QueuedPodInfo) {
	uis.readyUnitPods.Add(podInfo)
}

func (uis *unitInfos) GetAssignedSchedulerForPodGroupUnit(pg *v1alpha1.PodGroup) string {
	uis.Lock()
	defer uis.Unlock()
	unitKey := generateUnitKeyFromPodGroup(pg)
	if ui, exist := uis.units[unitKey]; exist {
		return ui.scheduler
	}
	return ""
}

func (uis *unitInfos) AssignSchedulerToPodGroupUnit(pg *v1alpha1.PodGroup, schedName string, forceUpdate bool) error {
	uis.Lock()
	defer uis.Unlock()
	unitKey := generateUnitKeyFromPodGroup(pg)
	ui := uis.units[unitKey]
	if ui == nil {
		uis.units[unitKey] = NewUnitInfo()
		ui = uis.units[unitKey]
	}
	if forceUpdate {
		ui.scheduler = schedName
		return nil
	}
	if ui.scheduler != "" && ui.scheduler != schedName {
		return fmt.Errorf("scheduler: %v is ever assigned to unit: %v, so can not set the newly selected scheduler: %v to that unit", ui.scheduler, unitKey, schedName)
	}
	ui.scheduler = schedName

	return nil
}

type unitInfo struct {
	podGroup     *v1alpha1.PodGroup
	pods         map[string]struct{}
	unSortedPods map[string]*QueuedPodInfo
	scheduler    string
	unitProperty api.UnitProperty

	// begin time wait for unit be ready
	waitingTimestamp time.Time
}

var _ api.ObservableUnit = &unitInfo{}

func NewUnitInfo() *unitInfo {
	return &unitInfo{
		pods:             make(map[string]struct{}),
		unSortedPods:     make(map[string]*QueuedPodInfo),
		waitingTimestamp: time.Now(),
	}
}

const (
	MsgNilPodGroup                     string = "DEBUG: pod group is nil"
	MsgPodGroupBeingDeleted            string = "DEBUG: pod group is being deleted"
	MsgPodGroupInPendingOrUnknownPhase string = "DEBUG: pod group is in either pending or unknown phase"
	MsgPodGroupLessThanMinMember       string = "DEBUG: pod group has not yet met the MinMember requirement with numReadyToBeDispatched=%d and minMember=%d"
)

func (ui *unitInfo) readyToBeDispatched() (string, bool) {
	if nil == ui.podGroup {
		klog.V(5).InfoS(MsgNilPodGroup)
		return MsgNilPodGroup, false
	}

	// if pod group is being deleted, do not try to dispatch pods belonging to it.
	if ui.podGroup.DeletionTimestamp != nil {
		klog.V(5).InfoS(MsgPodGroupBeingDeleted, "podGroup", klog.KObj(ui.podGroup))
		return MsgPodGroupBeingDeleted, false
	}

	if ui.podGroup.Status.Phase != v1alpha1.PodGroupPending && ui.podGroup.Status.Phase != v1alpha1.PodGroupUnknown {
		return "", true
	}
	klog.V(5).InfoS(MsgPodGroupInPendingOrUnknownPhase, "podGroup", klog.KObj(ui.podGroup))

	numReadyMembers := len(ui.pods)
	minMember := int(ui.podGroup.Spec.MinMember)
	if numReadyMembers < minMember {
		formattedMsg := fmt.Sprintf(MsgPodGroupLessThanMinMember, numReadyMembers, minMember)
		klog.V(5).InfoS(formattedMsg, "podGroup", klog.KObj(ui.podGroup))
		return formattedMsg, false
	}

	return "", true
}

type unitInfoProperty struct {
	*api.ScheduleUnitProperty
}

func newUnitInfoProperty(ui *unitInfo) *unitInfoProperty {
	if ui == nil || len(ui.unSortedPods) == 0 {
		return nil
	}

	var podProperty *api.PodProperty
	for _, podInfo := range ui.unSortedPods {
		if p := podInfo.GetPodProperty(); p != nil {
			podProperty = p
			break
		}
	}
	if podProperty == nil {
		return nil
	}

	minMember := 0
	if ui.podGroup != nil {
		minMember = int(ui.podGroup.Spec.MinMember)
	}

	return &unitInfoProperty{&api.ScheduleUnitProperty{
		PodProperty: podProperty,
		MinMember:   minMember,
		UnitType:    string(api.PodGroupUnitType),
	}}
}

func (ui *unitInfo) GetUnitProperty() api.UnitProperty {
	if ui.unitProperty != nil {
		return ui.unitProperty
	}

	p := newUnitInfoProperty(ui)
	if p == nil {
		return nil
	}
	ui.unitProperty = p
	return ui.unitProperty
}
