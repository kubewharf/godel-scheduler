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

type Candidate struct {
	Victims *Victims
	Name    string
}

type CachedNominatedNodes struct {
	podCount                int
	hasCrossNodeConstraints *bool
	unusedNominatedNodes    []*Candidate
	usedNominatedNode       *Candidate
}

func NewCachedNominatedNodes() *CachedNominatedNodes {
	return &CachedNominatedNodes{
		podCount: 1,
	}
}

func (cnn *CachedNominatedNodes) HasCrossNodeConstraints() *bool {
	if cnn == nil {
		return nil
	}
	return cnn.hasCrossNodeConstraints
}

func (cnn *CachedNominatedNodes) SetHasCrossNodesConstraints(crossNodes bool) {
	if cnn == nil {
		return
	}
	cnn.hasCrossNodeConstraints = &crossNodes
}

func (cnn *CachedNominatedNodes) SetPodCount(count int) {
	if cnn == nil {
		return
	}
	cnn.podCount = count
}

func (cnn *CachedNominatedNodes) RemoveOnePod() {
	if cnn == nil {
		return
	}
	if cnn.podCount > 0 {
		cnn.podCount--
	}
}

func (cnn *CachedNominatedNodes) GetPodCount() int {
	if cnn == nil {
		return 1
	}
	return cnn.podCount
}

func (cnn *CachedNominatedNodes) GetUsedNominatedNode() *Candidate {
	if cnn == nil {
		return nil
	}
	return cnn.usedNominatedNode
}

func (cnn *CachedNominatedNodes) GetUnusedNominatedNodes() []*Candidate {
	if cnn == nil {
		return nil
	}
	return cnn.unusedNominatedNodes
}

func (cnn *CachedNominatedNodes) IsEmpty() bool {
	return cnn == nil || len(cnn.unusedNominatedNodes) == 0
}

func (cnn *CachedNominatedNodes) SetUnusedNominatedNodes(candidates []*Candidate) {
	if cnn == nil {
		return
	}
	cnn.unusedNominatedNodes = candidates
}

func (cnn *CachedNominatedNodes) SetUsedNominatedNode(candidate *Candidate) {
	if cnn == nil {
		return
	}
	cnn.usedNominatedNode = candidate
}
