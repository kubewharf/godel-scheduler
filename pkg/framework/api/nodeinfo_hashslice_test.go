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
	"strconv"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func makeNode(name string) NodeInfo {
	nodeInfo := NewNodeInfo()
	nodeInfo.SetNode(&v1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}})
	return nodeInfo
}

func TestNodeHashSlice(t *testing.T) {
	type args struct {
		op []string
		in []NodeInfo
	}
	tests := []struct {
		name string
		args args
		want []bool
	}{
		{
			name: "normal",
			args: args{
				op: []string{"add", "del", "add", "del", "add"},
				in: []NodeInfo{
					makeNode("1"),
					makeNode("2"),
					makeNode("2"),
					makeNode("1"),
					makeNode("2"),
				},
			},
			want: []bool{true, false, true, true, false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hs := NewNodeHashSlice()
			var ret bool
			for i := range tt.args.op {
				switch tt.args.op[i] {
				case "add":
					ret = hs.Add(tt.args.in[i])
				case "del":
					ret = hs.Del(tt.args.in[i])
				default:
					t.Errorf("Invalid operation: %v\n", tt.args.op[i])
				}

				if ret != tt.want[i] {
					t.Errorf("Index %v get %v, want %v\n", i, ret, tt.want[i])
				}
			}
		},
		)
	}
}

func Benchmark_NodeHashSlice(b *testing.B) {
	for i := 0; i < b.N; i++ {
		nodeSlice := NewNodeHashSlice()
		for j := 0; j < 1e3; j++ {
			nodeSlice.Add(makeNode(strconv.Itoa(j)))
		}

		for j := 0; j < 1e3; j++ {
			if (j & 1) != 0 {
				nodeSlice.Del(makeNode(strconv.Itoa(j)))
			}
		}
	}
}

func Benchmark_Traditional(b *testing.B) {
	for i := 0; i < b.N; i++ {
		nodes := make([]NodeInfo, 0)
		for j := 0; j < 1e3; j++ {
			nodes = append(nodes, makeNode(strconv.Itoa(j)))
		}

		for j := 0; j < 1e3; j++ {
			if (j & 1) != 0 {
				newNodes := make([]NodeInfo, 0)
				for k := 0; k < len(nodes); k++ {
					if nodes[k].GetNodeName() != strconv.Itoa(j) {
						newNodes = append(newNodes, nodes[k])
					}
				}
				nodes = newNodes
			}
		}
	}
}
