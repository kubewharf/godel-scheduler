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

import (
	"sync"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

type StoreType int

const (
	Cache StoreType = iota
	Snapshot
)

type BaseStore interface {
	commoncache.ClusterCache
	AssumePod(*framework.CachePodInfo) error
	ForgetPod(*framework.CachePodInfo) error

	PeriodWorker(mu *sync.RWMutex)
}

type CommonStore interface {
	BaseStore

	Name() StoreName
	UpdateSnapshot(CommonStore) error
}
