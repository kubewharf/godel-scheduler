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

package unitqueuesort

import (
	"strconv"
)

const (
	LESS = iota
	EQUAL
	GREATER

	PriorityForDebugAnnotation = "godel.bytedance.com/customized-priority-for-debug"
)

func GetPriorityForDebug(annotations map[string]string) int {
	if len(annotations) == 0 {
		return 0
	}
	priorityForDebug := annotations[PriorityForDebugAnnotation]
	if len(priorityForDebug) == 0 {
		return 0
	}
	if priorityVal, err := strconv.Atoi(priorityForDebug); err != nil {
		return 0
	} else {
		return priorityVal
	}
}

func ComparePriorityForDebug(annotations1, annotations2 map[string]string) int {
	premium1, premium2 := GetPriorityForDebug(annotations1), GetPriorityForDebug(annotations2)
	if premium1 == premium2 {
		return EQUAL
	} else if premium1 < premium2 {
		return LESS
	} else {
		return GREATER
	}
}
