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

package metrics

import (
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// SwitchTypeToQos converts switch type to qos.
// SwitchType is used for concurrent scheduling, but in metrics we need to
// use more consistent label values, since concurrent scheduling is only supported
// in scheduler, not in dispatcher/binder.
func SwitchTypeToQos(switchType framework.SwitchType) string {
	switch switchType {
	case switchType & framework.GTBitMask:
		return string(podutil.GuaranteedPod)
	case switchType & framework.BEBitMask:
		return string(podutil.BestEffortPod)
	default:
		return string(podutil.UndefinedPod)
	}
}
