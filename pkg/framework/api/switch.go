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

import (
	"sync"
	"time"

	"github.com/kubewharf/godel-scheduler/pkg/util/bitplace"
)

type SwitchType int64

const (
	MaxSwitchNum int        = 31 // 0 as default, with 30 additional subclusters
	GTBitMask    SwitchType = 1<<MaxSwitchNum - 1
	BEBitMask    SwitchType = GTBitMask << MaxSwitchNum

	DisableScheduleSwitch SwitchType = 0
	SwitchTypeAll         SwitchType = GTBitMask | BEBitMask

	RecycleExpiration = 30 * 24 * time.Hour

	DisableScheduleSwitchStr = "DisableScheduleSwitch"
	GTScheduleStr            = "GTSchedule"
	BEScheduleStr            = "BESchedule"
	InvalidScheduleStr       = "InvalidSchedule"

	DefaultSubClusterIndex      = 0
	DefaultSubClusterSwitchType = (SwitchType(1) << DefaultSubClusterIndex) | (SwitchType(1) << DefaultSubClusterIndex << MaxSwitchNum)
	DefaultSubCluster           = ""
)

func (st SwitchType) String() string {
	switch st {
	case DisableScheduleSwitch:
		return DisableScheduleSwitchStr
	case st & GTBitMask:
		return GTScheduleStr
	case st & BEBitMask:
		return BEScheduleStr
	}
	return InvalidScheduleStr
}

func ClusterIndexToSwitchType(x int) (SwitchType, SwitchType) {
	return SwitchType(1) << x, SwitchType(1) << x << MaxSwitchNum
}

func ClusterIndexToGTSwitchType(x int) SwitchType {
	return SwitchType(1) << x
}

func ClusterIndexToBESwitchType(x int) SwitchType {
	return SwitchType(1) << x << MaxSwitchNum
}

var (
	globalClusterIndexMaintainer *clusterIndexMaintainer
	globalClusterIndexLock       sync.RWMutex
)

func init() {
	globalClusterIndexMaintainer = &clusterIndexMaintainer{
		bitPlace: bitplace.New(MaxSwitchNum),
		hash:     make(map[string]int),
	}
}

type clusterIndexMaintainer struct {
	bitPlace bitplace.BitPlace
	hash     map[string]int // key: subCluster, value: index
}

func GetClusterIndex(subCluster string) int {
	globalClusterIndexLock.RLock()
	defer globalClusterIndexLock.RUnlock()
	if idx, ok := globalClusterIndexMaintainer.hash[subCluster]; ok {
		return idx
	}
	return -1
}

func GetOrCreateClusterIndex(subCluster string) (int, bool) {
	globalClusterIndexLock.RLock()
	if idx, ok := globalClusterIndexMaintainer.hash[subCluster]; ok {
		globalClusterIndexLock.RUnlock()
		return idx, true
	}
	globalClusterIndexLock.RUnlock()

	globalClusterIndexLock.Lock()
	defer globalClusterIndexLock.Unlock()
	idx := globalClusterIndexMaintainer.bitPlace.Alloc()
	if idx == -1 {
		panic("Couldn't handle more switch type")
		// TODO: revisit this. Turn to default subcluster when too many indices are requested?
		// idx = DefaultSubClusterIndex
	}
	globalClusterIndexMaintainer.hash[subCluster] = idx
	return idx, false
}

func AllocClusterIndex(subCluster string) int {
	globalClusterIndexLock.Lock()
	defer globalClusterIndexLock.Unlock()
	idx := globalClusterIndexMaintainer.bitPlace.Alloc()
	if idx == -1 {
		panic("Couldn't handle more switch type")
		// TODO: revisit this. Turn to default subcluster when too many indices are requested?
		// idx = DefaultSubClusterIndex
	}
	globalClusterIndexMaintainer.hash[subCluster] = idx
	return idx
}

func FreeClusterIndex(subCluster string) int {
	globalClusterIndexLock.Lock()
	defer globalClusterIndexLock.Unlock()
	idx, ok := globalClusterIndexMaintainer.hash[subCluster]
	if !ok {
		return -1
	}
	// TODO: revisit this. When the subcluster is turned into the default subcluster, do not release index.
	// if idx != DefaultSubClusterIndex || subCluster == DefaultSubCluster {
	globalClusterIndexMaintainer.bitPlace.Free(idx)
	// }
	delete(globalClusterIndexMaintainer.hash, subCluster)
	return idx
}

func CleanClusterIndex() {
	globalClusterIndexLock.Lock()
	defer globalClusterIndexLock.Unlock()
	globalClusterIndexMaintainer.bitPlace.Clean()
	globalClusterIndexMaintainer.hash = make(map[string]int)
}

func ParseSwitchTypeFromSubCluster(subCluster string) SwitchType {
	if idx := GetClusterIndex(subCluster); idx != -1 {
		gt, be := ClusterIndexToSwitchType(idx)
		return gt | be
	}
	return 0
}

var globalSubClusterKey string

func GetGlobalSubClusterKey() string {
	return globalSubClusterKey
}

// SetGlobalSubClusterKey will be called only when init scheduler.
func SetGlobalSubClusterKey(key string) {
	globalSubClusterKey = key
}
