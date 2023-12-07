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

package isolatedcache

import (
	"reflect"
	"testing"
	"time"
)

var someTime = time.Now()

type fields struct {
	cachedNodesForPodOwner cachedNodes
}

// TODO: add cases for pod group checking logic
func Test_schedulerCache_CacheNodesForPodOwner(t *testing.T) {
	type args struct {
		podOwner string
		nodeName string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			name: "nil map",
			fields: fields{
				cachedNodesForPodOwner: nil,
			},
			args: args{
				podOwner: "foo",
				nodeName: "bar",
			},
			wantErr: false,
		},
		{
			name: "empty map",
			fields: fields{
				cachedNodesForPodOwner: cachedNodes{},
			},
			args: args{
				podOwner: "foo",
				nodeName: "bar",
			},
			wantErr: false,
		},
		{
			name: "non-empty map",
			fields: fields{
				cachedNodesForPodOwner: cachedNodes{
					"yoo": &podOwnerCachedNodes{
						nodeSlice: []string{"ahh"},
						nodeMap: map[string]*nodeTmpInfo{
							"ahh": {
								settingTime: someTime,
							},
						},
					},
				},
			},
			args: args{
				podOwner: "foo",
				nodeName: "bar",
			},
			wantErr: false,
		},
		{
			name: "duplicate data",
			fields: fields{
				cachedNodesForPodOwner: cachedNodes{
					"foo": &podOwnerCachedNodes{
						nodeSlice: []string{"bar"},
						nodeMap: map[string]*nodeTmpInfo{
							"bar": {
								settingTime: someTime,
							},
						},
					},
				},
			},
			args: args{
				podOwner: "foo",
				nodeName: "bar",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &isolatedCache{
				cachedNodesForPodOwner: tt.fields.cachedNodesForPodOwner,
			}
			if err := cache.CacheNodeForPodOwner(tt.args.podOwner, tt.args.nodeName, ""); (err != nil) != tt.wantErr {
				t.Errorf("schedulerCache.CacheNodesForPodOwner() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_schedulerCache_GetNodesForPodOwner(t *testing.T) {
	type args struct {
		podOwner string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []string
	}{
		{
			name: "nil map",
			fields: fields{
				cachedNodesForPodOwner: nil,
			},
			args: args{
				podOwner: "foo",
			},
			want: nil,
		},
		{
			name: "empty map",
			fields: fields{
				cachedNodesForPodOwner: cachedNodes{},
			},
			args: args{
				podOwner: "foo",
			},
			want: nil,
		},
		{
			name: "non-empty map",
			fields: fields{
				cachedNodesForPodOwner: cachedNodes{
					"foo": &podOwnerCachedNodes{
						nodeSlice: []string{"bar"},
						nodeMap: map[string]*nodeTmpInfo{
							"bar": {
								settingTime: someTime,
							},
						},
					},
				},
			},
			args: args{
				podOwner: "foo",
			},
			want: []string{"bar"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &isolatedCache{
				cachedNodesForPodOwner: tt.fields.cachedNodesForPodOwner,
			}
			if got := cache.GetOrderedNodesForPodOwner(tt.args.podOwner); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("schedulerCache.GetNodesForPodOwner() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_schedulerCache_DeleteNodeForPodOwner(t *testing.T) {
	type args struct {
		podOwner string
		nodeName string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			name: "nil map",
			fields: fields{
				cachedNodesForPodOwner: nil,
			},
			args: args{
				podOwner: "foo",
				nodeName: "bar",
			},
			wantErr: false,
		},
		{
			name: "empty map",
			fields: fields{
				cachedNodesForPodOwner: cachedNodes{},
			},
			args: args{
				podOwner: "foo",
				nodeName: "bar",
			},
			wantErr: false,
		},
		{
			name: "non-empty map",
			fields: fields{
				cachedNodesForPodOwner: cachedNodes{
					"yoo": &podOwnerCachedNodes{
						nodeSlice: []string{"ahh"},
						nodeMap: map[string]*nodeTmpInfo{
							"ahh": {
								settingTime: someTime,
							},
						},
					},
				},
			},
			args: args{
				podOwner: "foo",
				nodeName: "bar",
			},
			wantErr: false,
		},
		{
			name: "non-empty map with same args",
			fields: fields{
				cachedNodesForPodOwner: cachedNodes{
					"foo": &podOwnerCachedNodes{
						nodeSlice: []string{"bar"},
						nodeMap: map[string]*nodeTmpInfo{
							"bar": {
								settingTime: someTime,
							},
						},
					},
				},
			},
			args: args{
				podOwner: "foo",
				nodeName: "bar",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &isolatedCache{
				cachedNodesForPodOwner: tt.fields.cachedNodesForPodOwner,
			}
			if err := cache.DeleteNodeForPodOwner(tt.args.podOwner, tt.args.nodeName); (err != nil) != tt.wantErr {
				t.Errorf("schedulerCache.DeleteNodeForPodOwner() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_schedulerCache_CleanupCachedNodesForPodOwner(t *testing.T) {
	tests := []struct {
		name   string
		fields fields
		want   map[string]*podOwnerCachedNodes
	}{
		{
			name: "nil map",
			fields: fields{
				cachedNodesForPodOwner: nil,
			},
			want: nil,
		},
		{
			name: "empty map",
			fields: fields{
				cachedNodesForPodOwner: map[string]*podOwnerCachedNodes{},
			},
			want: map[string]*podOwnerCachedNodes{},
		},
		{
			name: "non-empty map that doesn't need to be cleaned up",
			fields: fields{
				cachedNodesForPodOwner: map[string]*podOwnerCachedNodes{
					"foo": {
						nodeSlice: []string{"bar"},
						nodeMap: map[string]*nodeTmpInfo{
							"bar": {
								settingTime: someTime.Add(defaultNodeCacheExpirationDuration * 2),
							},
						},
					},
				},
			},

			want: map[string]*podOwnerCachedNodes{
				"foo": {
					nodeSlice: []string{"bar"},
					nodeMap: map[string]*nodeTmpInfo{
						"bar": {
							settingTime: someTime.Add(defaultNodeCacheExpirationDuration * 2),
						},
					},
				},
			},
		},
		{
			name: "non-empty map that needs to be cleaned up",
			fields: fields{
				cachedNodesForPodOwner: map[string]*podOwnerCachedNodes{
					"foo": {
						nodeSlice: []string{"bar"},
						nodeMap: map[string]*nodeTmpInfo{
							"bar": {
								settingTime: someTime.Add(-defaultNodeCacheExpirationDuration * 2),
							},
						},
					},
				},
			},
			want: map[string]*podOwnerCachedNodes{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &isolatedCache{
				cachedNodesForPodOwner: tt.fields.cachedNodesForPodOwner,
			}
			cache.CleanupCachedNodesForPodOwner()
			if len(cache.cachedNodesForPodOwner) != len(tt.want) {
				t.Errorf("len is not equal, expect: %v, got: %v", len(tt.want), len(cache.cachedNodesForPodOwner))
			}

			for owner, nodes := range cache.cachedNodesForPodOwner {
				if tt.want[owner] == nil {
					t.Errorf("pod owner: %v shouldn't be cleaned up, but actually not", owner)
				}
				if len(tt.want[owner].nodeSlice) != len(nodes.nodeSlice) {
					t.Errorf("node slice length for owner is not equal, expect: %v, got: %v", len(tt.want[owner].nodeSlice), len(nodes.nodeSlice))
				}
				for name := range nodes.nodeMap {
					if tt.want[owner].nodeMap[name] == nil {
						t.Errorf("node: %v shouldn't be cleaned up, but actually not", name)
					}
				}
			}

			/*if !reflect.DeepEqual(cache.cachedNodesForPodOwner, tt.want) {
				t.Errorf("schedulerCache.CleanupCachedNodesForPodOwner() = %v, want %v", cache.cachedNodesForPodOwner, tt.want)
			}*/
		})
	}
}
