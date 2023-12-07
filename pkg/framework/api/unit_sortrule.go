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

type UnitAffinityTerm struct {
	TopologyKey string
}

// SortRule defines the rule for sorting items.
type SortRule struct {
	// Resource defines the resource name for sorting.
	Resource SortResource

	// Dimension may be Capacity or Available for the moment
	Dimension SortDimension

	// Order may be either Ascending or Descending
	Order SortOrder
}

// Valid returns the sort rule is valid or not
func (sr *SortRule) Valid() bool {
	switch sr.Resource {
	case GPUResource, CPUResource, MemoryResource:
	default:
		return false
	}

	switch sr.Dimension {
	case Capacity, Available:
	default:
		return false
	}

	switch sr.Order {
	case AscendingOrder, DescendingOrder:
	default:
		return false
	}
	return true
}

// SortResource is the resource that items are sorted by.
type SortResource string

const (
	// GPUResource means items are sorted by GPU resource.
	GPUResource SortResource = "GPU"

	// CPUResource means items are sorted by CPU resource.
	CPUResource SortResource = "CPU"

	// MemoryResource means items are sorted by Memory Resource.
	MemoryResource SortResource = "Memory"
)

type SortDimension string

const (
	Capacity SortDimension = "Capacity"

	Available SortDimension = "Available"
)

// SortOrder is the order of items.
type SortOrder string

const (
	// AscendingOrder is the order which items are increasing in a dimension.
	AscendingOrder SortOrder = "Ascending"

	// DescendingOrder is the order which items are decreasing in a dimension.
	DescendingOrder SortOrder = "Descending"
)
