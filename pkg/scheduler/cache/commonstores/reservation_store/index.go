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

package reservationstore

import "k8s.io/apimachinery/pkg/util/sets"

// placeholder -> nodeName List
type indexTable struct {
	// key: placeholderKey, value: nodeNameList
	nodesOfPlaceholder map[string]sets.String
	// TODO: support node priority
}

func newPlaceholderTable() *indexTable {
	return &indexTable{
		nodesOfPlaceholder: make(map[string]sets.String),
	}
}

func (t *indexTable) add(placeholder, nodeName string) {
	if _, ok := t.nodesOfPlaceholder[placeholder]; !ok {
		t.nodesOfPlaceholder[placeholder] = sets.NewString()
	}
	t.nodesOfPlaceholder[placeholder].Insert(nodeName)
}

func (t *indexTable) delete(placeholder, nodeName string) {
	if _, ok := t.nodesOfPlaceholder[placeholder]; !ok {
		return
	}
	t.nodesOfPlaceholder[placeholder].Delete(nodeName)
	if t.nodesOfPlaceholder[placeholder].Len() == 0 {
		delete(t.nodesOfPlaceholder, placeholder)
	}
}

func (t *indexTable) getNodeList(placeholder string) []string {
	set := t.nodesOfPlaceholder[placeholder]
	if set == nil {
		return nil
	}
	return set.UnsortedList()
}

func (t *indexTable) equal(o *indexTable) bool {
	for placeholder, nodeSet := range t.nodesOfPlaceholder {
		nodeSet2 := o.nodesOfPlaceholder[placeholder]
		if !nodeSet.Equal(nodeSet2) {
			return false
		}
	}
	return true
}
