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

package cache

import (
	deletedmarkerstore "github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores/deleted_marker_store"
	nodestore "github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores/node_store"
	pdbstore "github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores/pdb_store"
	podstore "github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores/pod_store"
	unitstatusstore "github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores/unit_status_store"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
)

// ATTENTION: The stores should be called in a certain order.
var orderedStoreNames = []commonstore.StoreName{
	// misc
	pdbstore.Name,

	// pod related
	unitstatusstore.Name,
	deletedmarkerstore.Name,

	nodestore.Name, // NodeStore be placed second to last.
	podstore.Name,  // PodStore must be placed at the end.
}
