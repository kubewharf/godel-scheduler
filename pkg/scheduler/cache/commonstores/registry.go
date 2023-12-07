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

package commonstores

import "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/handler"

type (
	StoreName string

	Registry map[StoreName]New

	New                func(handler.CacheHandler) CommonStore
	FeatureGateChecker func(handler.CacheHandler) bool
)

type registrys struct {
	CacheRegistry       map[StoreName]New
	SnapshotRegistry    map[StoreName]New
	FeatureGateCheckers map[StoreName]FeatureGateChecker
}

func (r *registrys) Register(name StoreName, checker FeatureGateChecker, newCache, newSnapshot New) {
	r.CacheRegistry[name] = newCache
	r.SnapshotRegistry[name] = newSnapshot
	r.FeatureGateCheckers[name] = checker
}

var GlobalRegistry = &registrys{
	CacheRegistry:       make(map[StoreName]New),
	SnapshotRegistry:    make(map[StoreName]New),
	FeatureGateCheckers: make(map[StoreName]FeatureGateChecker),
}
