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
	"errors"
	"sync"

	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	// NotFound is the not found error message.
	NotFound = "not found"
)

// globalRegistry stores the StateKeys that will only be read by plugins.
var globalRegistry sets.String

func init() {
	globalRegistry = sets.NewString(
		NodePartitionTypeStateKey,
		PodResourceTypeStateKey,
		PodLauncherStateKey,
		NodeGroupStateKey,
		PodPropertyKey,
	)
}

// StateData is a generic type for arbitrary data stored in CycleState.
type StateData interface {
	// Clone is an interface to make a copy of StateData. For performance reasons,
	// clone should make shallow copies for members (e.g., slices or maps) that are not
	// impacted by PreFilter's optional AddPod/RemovePod methods.
	Clone() StateData
}

// StateKey is the type of keys stored in CycleState.
type StateKey string

// CycleState provides a mechanism for plugins to store and retrieve arbitrary data.
// StateData stored by one plugin can be read, altered, or deleted by another plugin.
// For data that will be read and written by plugins, we use sync.Map to avoid race condition.
// As for data that will only be read by plugins, we use map directly.
type CycleState struct {
	storage         sync.Map
	readOnlyStorage map[string]interface{}
	// if recordPluginMetrics is true, PluginExecutionDuration will be recorded for this cycle.
	recordPluginMetrics bool
}

// NewCycleState initializes a new CycleState and returns its pointer.
func NewCycleState() *CycleState {
	return &CycleState{
		readOnlyStorage: map[string]interface{}{},
	}
}

// ShouldRecordPluginMetrics returns whether PluginExecutionDuration metrics should be recorded.
func (c *CycleState) ShouldRecordPluginMetrics() bool {
	if c == nil {
		return false
	}
	return c.recordPluginMetrics
}

// SetRecordPluginMetrics sets recordPluginMetrics to the given value.
func (c *CycleState) SetRecordPluginMetrics(flag bool) {
	if c == nil {
		return
	}
	c.recordPluginMetrics = flag
}

// Clone creates a copy of CycleState and returns its pointer. Clone returns
// nil if the context being cloned is nil.
func (c *CycleState) Clone() *CycleState {
	if c == nil {
		return nil
	}
	copy := NewCycleState()
	c.storage.Range(func(k, v interface{}) bool {
		copy.storage.Store(k, v.(StateData).Clone())
		return true
	})
	copy.readOnlyStorage = c.readOnlyStorage

	return copy
}

// Read retrieves data with the given "key" from CycleState. If the key is not
// present an error is returned.
// This function is not thread safe. In multithreading code, lock should be
// acquired first.
func (c *CycleState) Read(key StateKey) (StateData, error) {
	var v interface{}
	if globalRegistry.Has(string(key)) {
		v = c.readOnlyStorage[string(key)]
	} else {
		v, _ = c.storage.Load(key)
	}
	if v == nil {
		return nil, errors.New(NotFound)
	}
	return v.(StateData), nil
}

// Write stores the given "val" in CycleState with the given "key".
// This function is not thread safe. In multithreading code, lock should be
// acquired first.
func (c *CycleState) Write(key StateKey, val StateData) {
	if c == nil {
		return
	}

	if globalRegistry.Has(string(key)) {
		c.readOnlyStorage[string(key)] = val
	} else {
		c.storage.Store(key, val)
	}
}

// Delete deletes data with the given key from CycleState.
// This function is not thread safe. In multithreading code, lock should be
// acquired first.
func (c *CycleState) Delete(key StateKey) {
	c.storage.Delete(key)
}
