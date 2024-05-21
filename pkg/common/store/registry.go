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

package store

import commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"

type (
	StoreName string

	Registry map[StoreName]New

	New                func(commoncache.CacheHandler) Store
	FeatureGateChecker func(commoncache.CacheHandler) bool
)

type Registries interface {
	Register(name StoreName, checker FeatureGateChecker, newCache, newSnapshot New)

	CacheRegistry() Registry
	SnapshotRegistry() Registry
	FeatureGateCheckers() map[StoreName]FeatureGateChecker
}

type registriesImpl struct {
	cacheRegistry       map[StoreName]New
	snapshotRegistry    map[StoreName]New
	featureGateCheckers map[StoreName]FeatureGateChecker
}

func NewRegistries() Registries {
	return &registriesImpl{
		cacheRegistry:       make(map[StoreName]New),
		snapshotRegistry:    make(map[StoreName]New),
		featureGateCheckers: make(map[StoreName]FeatureGateChecker),
	}
}

func (r *registriesImpl) Register(name StoreName, checker FeatureGateChecker, newCache, newSnapshot New) {
	r.cacheRegistry[name] = newCache
	r.snapshotRegistry[name] = newSnapshot
	r.featureGateCheckers[name] = checker
}

func (r *registriesImpl) CacheRegistry() Registry {
	return r.cacheRegistry
}

func (r *registriesImpl) SnapshotRegistry() Registry {
	return r.snapshotRegistry
}

func (r *registriesImpl) FeatureGateCheckers() map[StoreName]FeatureGateChecker {
	return r.featureGateCheckers
}
