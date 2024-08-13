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

package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/robfig/cron/v3"
)

// GetEarliestPodStartTime returns the earliest start time of all pods that
// have the highest priority among all victims.
func GetEarliestPodStartTime(victims *framework.Victims) *metav1.Time {
	if len(victims.Pods) == 0 {
		// should not reach here.
		klog.InfoS("WARN: The parameter victims.Pods was empty. Should not reach here")
		return nil
	}

	earliestPodStartTime := util.GetPodStartTime(victims.Pods[0])
	maxPriority := podutil.GetPodPriority(victims.Pods[0])

	for _, pod := range victims.Pods {
		if podutil.GetPodPriority(pod) == maxPriority {
			if util.GetPodStartTime(pod).Before(earliestPodStartTime) {
				earliestPodStartTime = util.GetPodStartTime(pod)
			}
		} else if podutil.GetPodPriority(pod) > maxPriority {
			maxPriority = podutil.GetPodPriority(pod)
			earliestPodStartTime = util.GetPodStartTime(pod)
		}
	}

	return earliestPodStartTime
}

// GetPotentialVictims return potential victims of the given pod, from annotation potentialVictims
func GetPotentialVictims(pod *v1.Pod) (*framework.PotentialVictims, error) {
	if data, ok := pod.Annotations[podutil.PotentialVictimsAnnotationKey]; ok && data != "" {
		var potentialVictims framework.PotentialVictims
		if err := json.Unmarshal([]byte(data), &potentialVictims); err != nil {
			return nil, fmt.Errorf("illegal potential victims %v", data)
		}
		return &potentialVictims, nil
	}
	return nil, nil
}

// SetPodNominatedNode nominatedNode is supposed to be checked before call SetPodNominatedNode
func SetPodNominatedNode(pod *v1.Pod, nominatedNode *framework.NominatedNode) error {
	data, err := json.Marshal(*nominatedNode)
	if err != nil {
		return err
	}
	pod.Annotations[podutil.NominatedNodeAnnotationKey] = string(data)
	return nil
}

func GetPodNominatedNode(pod *v1.Pod) (*framework.NominatedNode, error) {
	if pod == nil {
		return nil, errors.New("pod is nil")
	}
	var nominatedNode framework.NominatedNode
	nominatedNodeData, ok := pod.Annotations[podutil.NominatedNodeAnnotationKey]
	if !ok {
		// concatenate strings instead of using fmt.Errorf to reduce possible performance overhead
		errMsg := "pod " + pod.Name + " does not have " + podutil.NominatedNodeAnnotationKey + " annotation"
		return nil, errors.New(errMsg)
	}

	if err := json.Unmarshal([]byte(nominatedNodeData), &nominatedNode); err != nil {
		// concatenate strings instead of using fmt.Errorf to reduce possible performance overhead
		errMsg := "failed to parse " + podutil.NominatedNodeAnnotationKey + " annotation for pod " + pod.Name + ", error is " + err.Error()
		return nil, errors.New(errMsg)
	}

	return &nominatedNode, nil
}

func ConstructNominatedNode(nominatedNodeName string, victims *framework.Victims) *framework.NominatedNode {
	var victimPods []framework.VictimPod
	if victims != nil && len(victims.Pods) > 0 {
		victimPods = make([]framework.VictimPod, len(victims.Pods))
		for index, victim := range victims.Pods {
			victimPods[index] = framework.VictimPod{
				Name:      victim.Name,
				Namespace: victim.Namespace,
				UID:       string(victim.UID),
			}
		}
	}

	return &framework.NominatedNode{
		NodeName:   nominatedNodeName,
		VictimPods: victimPods,
	}
}

// GetNodeNameFromPod returns the name of node where pod is assumed to be placed on, based on following rules:
// 1. NodeName should be Pod.Spec.NodeName if Pod.Spec.NodeName is set
// 2. NodeName should be Pod.Annotations[AssumedNodeAnnotationKey] if Pod.Annotations[AssumedNodeAnnotationKey] is set
// 3. NodeName should be Pod.Annotations[NominatedNodeAnnotationKey] if Pod.Annotations[NominatedNodeAnnotationKey] is set
// 4. NodeName should be ""
func GetNodeNameFromPod(pod *v1.Pod) string {
	nodeName := ""
	if pod.Spec.NodeName != "" {
		nodeName = pod.Spec.NodeName
	} else if pod.Annotations[podutil.AssumedNodeAnnotationKey] != "" {
		nodeName = pod.Annotations[podutil.AssumedNodeAnnotationKey]
	} else if pod.Annotations[podutil.NominatedNodeAnnotationKey] != "" {
		if nominatedNode, err := GetPodNominatedNode(pod); err == nil && nominatedNode != nil {
			nodeName = nominatedNode.NodeName
		}
	}

	return nodeName
}

// SchedSleep help to reduce the failures caused by golang scheduler which cannot schedule goroutines fast enough
// during unittests when coverage is enabled.
func SchedSleep(duration time.Duration) {
	runtime.Gosched()
	time.Sleep(duration)
	runtime.Gosched()
}

// StartMinutelyScheduledCron starts a cron that runs every minute on the minute.
func StartMinutelyScheduledCron(fun func()) error {
	c := cron.New()
	_, err := c.AddFunc("* * * * *", fun)
	if err != nil {
		return err
	}
	c.Start()
	return nil
}

func GetTaskInfoFromPod(pod *v1.Pod) *schedulingv1a1.TaskInfo {
	return &schedulingv1a1.TaskInfo{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		UID:       pod.UID,
		Node:      GetNodeNameFromPod(pod),
	}
}
