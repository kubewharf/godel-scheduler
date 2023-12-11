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

package nodestore

import (
	v1 "k8s.io/api/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// -------------------------------------- topology --------------------------------------
func AssignMicroTopology(node framework.NodeInfo, pod *v1.Pod) error {
	if numaBinding, _ := util.NeedConsiderTopology(pod); !numaBinding {
		return nil
	}

	/*
		numaTopologyStatus := node.GetNumaTopologyStatus()

		podsLabels := map[string]map[string]string{}
		for _, pInfo := range node.GetPods() {
			p := pInfo.Pod
			if p == nil {
				continue
			}
			podsLabels[string(p.UID)] = p.Labels
		}

		podAllocation, err := agent.Allocate(pod, numaTopologyStatus, podsLabels)
		if err != nil {
			return err
		}
	*/
	var podAllocation map[int]*v1.ResourceList
	// TODO: add Allocate function
	if len(podAllocation) == 0 {
		return nil
	}
	microTopologyStr := util.MarshalMicroTopology(podAllocation)
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[podutil.MicroTopologyKey] = microTopologyStr
	return nil
}
