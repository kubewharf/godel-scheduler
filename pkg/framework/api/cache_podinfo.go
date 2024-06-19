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

package api

import v1 "k8s.io/api/core/v1"

type CachePodInfo struct {
	Pod        *v1.Pod
	CycleState *CycleState
	Victims    *Victims
}

type CachePodInfoWrapper struct{ obj *CachePodInfo }

func MakeCachePodInfoWrapper() *CachePodInfoWrapper {
	return &CachePodInfoWrapper{&CachePodInfo{}}
}

func (w *CachePodInfoWrapper) Obj() *CachePodInfo {
	return w.obj
}

func (w *CachePodInfoWrapper) Pod(p *v1.Pod) *CachePodInfoWrapper {
	w.obj.Pod = p
	return w
}

func (w *CachePodInfoWrapper) CycleState(s *CycleState) *CachePodInfoWrapper {
	w.obj.CycleState = s
	return w
}

func (w *CachePodInfoWrapper) Victims(victims *Victims) *CachePodInfoWrapper {
	w.obj.Victims = victims
	return w
}
