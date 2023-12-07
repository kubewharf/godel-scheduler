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
	"reflect"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/plugins/unitqueuesort"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

// newQueuedPodInfo builds a QueuedPodInfo object.
func newQueuedPodInfo(pod *v1.Pod, clock util.Clock) *framework.QueuedPodInfo {
	now := clock.Now()
	ownerKey := util.GetOwnerReferenceKey(pod)
	if len(ownerKey) == 0 {
		ownerKey = podutil.GeneratePodKey(pod)
	}

	return &framework.QueuedPodInfo{
		Pod:                     pod,
		Timestamp:               now,
		InitialAttemptTimestamp: now,
		QueueSpan:               tracing.NewSpanInfo(framework.ExtractPodProperty(pod).ConvertToTracingTags()),
		OwnerReferenceKey:       ownerKey,
	}
}

// newQueuedPodInfoForLookup builds a QueuedPodInfo object without timestamp.
func newQueuedPodInfoForLookup(pod *v1.Pod) *framework.QueuedPodInfo {
	return &framework.QueuedPodInfo{
		Pod: pod,
	}
}

func updatePodInfo(oldPodInfo interface{}, newPod *v1.Pod) *framework.QueuedPodInfo {
	podInfo := oldPodInfo.(*framework.QueuedPodInfo)
	podInfo.Pod = newPod
	return podInfo
}

// isPodUpdated checks if the pod is updated in a way that it may have become
// schedulable. It drops status of the pod and compares it with old version.
func isPodUpdated(oldPod, newPod *v1.Pod) bool {
	if oldPod == nil || newPod == nil {
		return oldPod != nil || newPod != nil
	}
	strip := func(pod *v1.Pod) *v1.Pod {
		p := pod.DeepCopy()
		p.ResourceVersion = ""
		p.Generation = 0
		p.Status = v1.PodStatus{}
		return p
	}
	return !reflect.DeepEqual(strip(oldPod), strip(newPod))
}

func unitInfoKeyFunc(obj interface{}) (string, error) {
	unitInfo := obj.(*framework.QueuedUnitInfo)
	return unitInfo.UnitKey, nil
}

func MakeNextUnitFunc(queue SchedulingQueue) func() *framework.QueuedUnitInfo {
	return func() *framework.QueuedUnitInfo {
		unitInfo, err := queue.Pop()
		if err == nil {
			klog.V(4).InfoS("Ready to try and schedule the next unit", "numberOfPods", unitInfo.NumPods(), "unitKey", unitInfo.UnitKey)

			// finish queue span
			for _, info := range unitInfo.GetPods() {
				info.UpdateQueueStage("-")
				if info.QueueSpan != nil {
					// finish queue span
					info.QueueSpan.FinishSpan(tracing.SchedulerPendingInQueueSpan, tracing.GetSpanContextFromPod(info.Pod), info.Timestamp)
				}
			}
			return unitInfo
		}
		klog.InfoS("Failed to retrieve the next unit for scheduling", "err", err)
		return nil
	}
}

func alwaysFalse(_, _ interface{}) bool {
	return false
}

func InitUnitQueueSortPlugin(spec *framework.PluginSpec, pluginArgs map[string]*config.PluginConfig) (framework.UnitQueueSortPlugin, error) {
	if spec == nil {
		return nil, fmt.Errorf("queue unit sort plugin not specified")
	}
	plName := spec.GetName()
	factory, ok := UnitSortPluginRegistry[plName]
	if !ok {
		return nil, fmt.Errorf("unregiestered queue unit sort plugin: %v", plName)
	}
	if pluginArgs[plName] != nil {
		return factory(pluginArgs[plName].Args.Object)
	}
	return factory(nil)
}

type SortPluginFactory = func(runtime.Object) (framework.UnitQueueSortPlugin, error)

var UnitSortPluginRegistry = map[string]SortPluginFactory{
	unitqueuesort.FCFSName: unitqueuesort.NewFCFS,
	unitqueuesort.Name:     unitqueuesort.New,
}
