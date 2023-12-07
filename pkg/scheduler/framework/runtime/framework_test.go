/*
Copyright 2014 The Kubernetes Authors.

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

package runtime

import (
	"context"
	"reflect"
	"strconv"
	"testing"

	v1 "k8s.io/api/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/util"
)

const (
	TestSchedulerName = "test-scheduler"
	// ErrReasonFake is a fake error message denotes the filter function errored.
	ErrReasonFake = "Nodes failed the fake plugin"
)

var (
	fakePlugins      = []string{"a", "b", "c", "d", "e"}
	fakeOrderPlugins = []string{"d", "c", "a", "b", "e"}

	_ framework.FilterPlugin    = &FakePlugin{}
	_ framework.PreFilterPlugin = &FakePlugin{}
	_ framework.ScorePlugin     = &FakePlugin{}
)

type FakePlugin struct {
	name string
}

// Name returns name of the plugin.
func (pl *FakePlugin) Name() string {
	return pl.name
}

// Filter invoked at the filter extension point.
func (pl *FakePlugin) Filter(_ context.Context, _ *framework.CycleState, _ *v1.Pod, _ framework.NodeInfo) *framework.Status {
	return framework.NewStatus(framework.Unschedulable, ErrReasonFake)
}

// PreFilter invoked at the PreFilter extension point.
func (pl *FakePlugin) PreFilter(_ context.Context, _ *framework.CycleState, _ *v1.Pod) *framework.Status {
	return nil
}

func (pl *FakePlugin) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

// Score invoked at the Score extension point.
func (pl *FakePlugin) Score(_ context.Context, _ *framework.CycleState, _ *v1.Pod, _ string) (int64, *framework.Status) {
	return 0, nil
}

// ScoreExtensions returns a ScoreExtensions interface if it implements one, or nil if does not.
func (pl *FakePlugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

func newTestSchedulerFramework(
	preFilterPlugins []framework.PreFilterPlugin,
	filterPlugins []framework.FilterPlugin,
	preScorePlugins []framework.PreScorePlugin,
	scorePlugins []framework.ScorePlugin,
	scoreWeightMap map[string]int64,
	crossNodesPlugins []framework.CrossNodesPlugin,
) *GodelSchedulerFramework {
	f := &GodelSchedulerFramework{
		preFilterPlugins:     preFilterPlugins,
		filterPlugins:        filterPlugins,
		preScorePlugins:      preScorePlugins,
		scorePlugins:         scorePlugins,
		scoreWeightMap:       scoreWeightMap,
		crossNodesPlugins:    crossNodesPlugins,
		preFilterPluginsSet:  make(map[string]int),
		filterPluginsSet:     make(map[string]int),
		preScorePluginsSet:   make(map[string]int),
		scorePluginsSet:      make(map[string]int),
		crossNodesPluginsSet: make(map[string]int),
	}

	for index, plugin := range preFilterPlugins {
		f.preFilterPluginsSet[plugin.Name()] = index
	}
	for index, plugin := range filterPlugins {
		f.filterPluginsSet[plugin.Name()] = index
	}
	for index, plugin := range preScorePlugins {
		f.preScorePluginsSet[plugin.Name()] = index
	}
	for index, plugin := range scorePlugins {
		f.scorePluginsSet[plugin.Name()] = index
	}
	return f
}

func initTestPluginRegistry() framework.PluginMap {
	testPluginRegistry := make(framework.PluginMap)
	for _, plugin := range fakePlugins {
		testPluginRegistry[plugin] = &FakePlugin{name: plugin}
	}
	return testPluginRegistry
}

func initTestPluginOrder(pluginList []string) framework.PluginOrder {
	orderedPlugin := &framework.OrderedPluginRegistry{
		Plugins: fakeOrderPlugins,
	}
	return util.GetListIndex(orderedPlugin)
}

func TestSchedulerFrameworkAddFilterPlugins(t *testing.T) {
	registry := initTestPluginRegistry()
	pluginOrder := initTestPluginOrder(fakeOrderPlugins)

	tests := []struct {
		name            string
		framework       *GodelSchedulerFramework
		toAddPlugins    []*framework.PluginSpec
		expectedPlugins string
	}{
		{
			name: "normal filter plugins",
			framework: newTestSchedulerFramework(
				[]framework.PreFilterPlugin{},
				[]framework.FilterPlugin{},
				[]framework.PreScorePlugin{},
				[]framework.ScorePlugin{}, map[string]int64{},
				[]framework.CrossNodesPlugin{}),
			toAddPlugins: []*framework.PluginSpec{
				framework.NewPluginSpec(fakePlugins[0]),
				framework.NewPluginSpec(fakePlugins[1]),
				framework.NewPluginSpec(fakePlugins[2]),
			},
			expectedPlugins: "c,a,b,",
		},
		{
			name: "duplicate filter plugins",
			framework: newTestSchedulerFramework(
				[]framework.PreFilterPlugin{},
				[]framework.FilterPlugin{},
				[]framework.PreScorePlugin{},
				[]framework.ScorePlugin{}, map[string]int64{},
				[]framework.CrossNodesPlugin{}),
			toAddPlugins: []*framework.PluginSpec{
				framework.NewPluginSpec(fakePlugins[0]),
				framework.NewPluginSpec(fakePlugins[1]),
				framework.NewPluginSpec(fakePlugins[0]),
			},
			expectedPlugins: "a,b,",
		},
		{
			name: "duplicate with existed filter plugins",
			framework: newTestSchedulerFramework(
				[]framework.PreFilterPlugin{},
				[]framework.FilterPlugin{},
				[]framework.PreScorePlugin{},
				[]framework.ScorePlugin{},
				map[string]int64{},
				[]framework.CrossNodesPlugin{}),
			toAddPlugins: []*framework.PluginSpec{
				framework.NewPluginSpec(fakePlugins[0]),
				framework.NewPluginSpec(fakePlugins[1]),
			},
			expectedPlugins: "a,b,",
		},
		{
			name: "duplicate with existed filter plugins",
			framework: newTestSchedulerFramework(
				[]framework.PreFilterPlugin{},
				[]framework.FilterPlugin{},
				[]framework.PreScorePlugin{},
				[]framework.ScorePlugin{}, map[string]int64{},
				[]framework.CrossNodesPlugin{}),
			toAddPlugins: []*framework.PluginSpec{
				framework.NewPluginSpec(fakePlugins[0]),
				framework.NewPluginSpec(fakePlugins[0]),
				framework.NewPluginSpec(fakePlugins[2]),
				framework.NewPluginSpec(fakePlugins[1]),
			},
			expectedPlugins: "c,a,b,",
		},
		{
			name: "ignore unsupported filter plugins",
			framework: newTestSchedulerFramework(
				[]framework.PreFilterPlugin{},
				[]framework.FilterPlugin{},
				[]framework.PreScorePlugin{},
				[]framework.ScorePlugin{},
				map[string]int64{},
				[]framework.CrossNodesPlugin{}),
			toAddPlugins: []*framework.PluginSpec{
				framework.NewPluginSpec("hello"),
			},
			expectedPlugins: "",
		},
	}

	for _, test := range tests {
		for _, pluginSpec := range test.toAddPlugins {
			test.framework.addFilterPlugin(pluginSpec, registry)
		}
		test.framework.orderFilterPlugins(pluginOrder)
		got := ""
		for _, plugin := range test.framework.filterPlugins {
			got += plugin.Name() + ","
		}
		if test.expectedPlugins != got {
			t.Errorf("expected: %#v, got: %#v", test.expectedPlugins, got)
		}
	}
}

func TestSchedulerFrameworkAddScorePlugins(t *testing.T) {
	registry := initTestPluginRegistry()

	tests := []struct {
		name            string
		framework       *GodelSchedulerFramework
		toAddPlugins    []*framework.PluginSpec
		expectedPlugins string
	}{
		{
			name: "normal score plugins",
			framework: newTestSchedulerFramework(
				[]framework.PreFilterPlugin{},
				[]framework.FilterPlugin{},
				[]framework.PreScorePlugin{},
				[]framework.ScorePlugin{},
				map[string]int64{},
				[]framework.CrossNodesPlugin{}),
			toAddPlugins: []*framework.PluginSpec{
				framework.NewPluginSpecWithWeight(fakePlugins[0], 2),
				framework.NewPluginSpecWithWeight(fakePlugins[1], 3),
				framework.NewPluginSpecWithWeight(fakePlugins[2], 4),
			},
			expectedPlugins: "a:2,b:3,c:4,",
		},
		{
			name: "duplicate score plugins and override",
			framework: newTestSchedulerFramework(
				[]framework.PreFilterPlugin{},
				[]framework.FilterPlugin{},
				[]framework.PreScorePlugin{},
				[]framework.ScorePlugin{},
				map[string]int64{},
				[]framework.CrossNodesPlugin{}),
			toAddPlugins: []*framework.PluginSpec{
				framework.NewPluginSpecWithWeight(fakePlugins[0], 2),
				framework.NewPluginSpecWithWeight(fakePlugins[1], 3),
				framework.NewPluginSpecWithWeight(fakePlugins[0], 4),
			},
			expectedPlugins: "a:4,b:3,",
		},
		{
			name: "duplicate with existed score plugins",
			framework: newTestSchedulerFramework([]framework.PreFilterPlugin{},
				[]framework.FilterPlugin{},
				[]framework.PreScorePlugin{},
				[]framework.ScorePlugin{
					&FakePlugin{name: fakePlugins[1]},
				},
				map[string]int64{fakePlugins[1]: 2},
				[]framework.CrossNodesPlugin{}),
			toAddPlugins: []*framework.PluginSpec{
				framework.NewPluginSpecWithWeight(fakePlugins[0], 3),
				framework.NewPluginSpecWithWeight(fakePlugins[1], 1),
			},
			expectedPlugins: "b:1,a:3,",
		},
		{
			name: "duplicate with existed score plugins",
			framework: newTestSchedulerFramework(
				[]framework.PreFilterPlugin{},
				[]framework.FilterPlugin{},
				[]framework.PreScorePlugin{},
				[]framework.ScorePlugin{
					&FakePlugin{name: fakePlugins[1]},
				},
				map[string]int64{fakePlugins[1]: 2},
				[]framework.CrossNodesPlugin{}),
			toAddPlugins: []*framework.PluginSpec{
				framework.NewPluginSpecWithWeight(fakePlugins[0], 10),
				framework.NewPluginSpecWithWeight(fakePlugins[0], 5),
				framework.NewPluginSpecWithWeight(fakePlugins[2], 3),
				framework.NewPluginSpecWithWeight(fakePlugins[1], 1),
			},
			expectedPlugins: "b:1,a:5,c:3,",
		},
		{
			name: "ignore unsupported score plugins",
			framework: newTestSchedulerFramework(
				[]framework.PreFilterPlugin{},
				[]framework.FilterPlugin{},
				[]framework.PreScorePlugin{},
				[]framework.ScorePlugin{
					&FakePlugin{name: fakePlugins[1]},
				},
				map[string]int64{},
				[]framework.CrossNodesPlugin{}),
			toAddPlugins: []*framework.PluginSpec{
				framework.NewPluginSpec("hello"),
			},
			expectedPlugins: "b:0,",
		},
	}

	for _, test := range tests {
		for _, pluginSpec := range test.toAddPlugins {
			test.framework.addScorePlugin(pluginSpec, registry)
		}
		got := ""
		for _, plugin := range test.framework.scorePlugins {
			got += plugin.Name() + ":" + strconv.FormatInt(test.framework.scoreWeightMap[plugin.Name()], 10) + ","
		}
		if test.expectedPlugins != got {
			t.Errorf("expected: %#v, got: %#v", test.expectedPlugins, got)
		}
	}
}

func TestGodelSchedulerFrameworkOrderFilterPlugins(t *testing.T) {
	tests := []struct {
		name            string
		framework       *GodelSchedulerFramework
		orderedPlugin   framework.PluginList
		expectedPlugins string
	}{
		// TODO: Add test cases.
		{
			name: "normal order",
			framework: newTestSchedulerFramework([]framework.PreFilterPlugin{},
				[]framework.FilterPlugin{
					&FakePlugin{name: fakePlugins[2]},
					&FakePlugin{name: fakePlugins[0]},
					&FakePlugin{name: fakePlugins[1]},
				},
				[]framework.PreScorePlugin{},
				[]framework.ScorePlugin{},
				map[string]int64{},
				[]framework.CrossNodesPlugin{}),
			orderedPlugin: &framework.OrderedPluginRegistry{
				Plugins: []string{fakePlugins[0], fakePlugins[1], fakePlugins[2]},
			},
			expectedPlugins: "a,b,c,",
		},
		{
			name: "duplicate filer plugins",
			framework: newTestSchedulerFramework([]framework.PreFilterPlugin{},
				[]framework.FilterPlugin{
					&FakePlugin{name: fakePlugins[2]},
					&FakePlugin{name: fakePlugins[0]},
					&FakePlugin{name: fakePlugins[0]},
				},
				[]framework.PreScorePlugin{},
				[]framework.ScorePlugin{},
				map[string]int64{},
				[]framework.CrossNodesPlugin{}),
			orderedPlugin: &framework.OrderedPluginRegistry{
				Plugins: []string{fakePlugins[0], fakePlugins[1], fakePlugins[2]},
			},
			expectedPlugins: "a,a,c,",
		},
		{
			name: "duplicate order",
			framework: newTestSchedulerFramework([]framework.PreFilterPlugin{},
				[]framework.FilterPlugin{
					&FakePlugin{name: fakePlugins[2]},
					&FakePlugin{name: fakePlugins[0]},
					&FakePlugin{name: fakePlugins[1]},
				},
				[]framework.PreScorePlugin{},
				[]framework.ScorePlugin{},
				map[string]int64{},
				[]framework.CrossNodesPlugin{}),
			orderedPlugin: &framework.OrderedPluginRegistry{
				Plugins: []string{fakePlugins[0], fakePlugins[1], fakePlugins[1], fakePlugins[2]},
			},
			expectedPlugins: "a,b,c,",
		},
		{
			name: "no exist plugin",
			framework: newTestSchedulerFramework([]framework.PreFilterPlugin{},
				[]framework.FilterPlugin{
					&FakePlugin{name: fakePlugins[2]},
					&FakePlugin{name: fakePlugins[0]},
					&FakePlugin{name: fakePlugins[3]},
					&FakePlugin{name: fakePlugins[1]},
				},
				[]framework.PreScorePlugin{},
				[]framework.ScorePlugin{},
				map[string]int64{},
				[]framework.CrossNodesPlugin{}),
			orderedPlugin: &framework.OrderedPluginRegistry{
				Plugins: []string{fakePlugins[0], fakePlugins[2], fakePlugins[3]},
			},
			expectedPlugins: "a,c,d,b,",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.framework
			orderedPlugin := util.GetListIndex(tt.orderedPlugin)
			f.orderFilterPlugins(orderedPlugin)
			got := ""
			for _, plugin := range f.filterPlugins {
				got += plugin.Name() + ","
			}
			if tt.expectedPlugins != got {
				t.Errorf("expected: %#v, got: %#v", tt.expectedPlugins, got)
			}
		})
	}
}

var (
	_ framework.FilterPlugin     = &FakePluginA{}
	_ framework.CrossNodesPlugin = &FakePluginA{}
)

type FakePluginA struct {
	name string
}

// Name returns name of the plugin.
func (pl *FakePluginA) Name() string {
	return pl.name
}

// Filter invoked at the filter extension point.
func (pl *FakePluginA) Filter(_ context.Context, _ *framework.CycleState, _ *v1.Pod, _ framework.NodeInfo) *framework.Status {
	return framework.NewStatus(framework.Unschedulable, ErrReasonFake)
}

func (pl *FakePluginA) HasCrossNodesConstraints(_ context.Context, _ *v1.Pod) bool {
	return false
}

func TestSchedulerFrameworkAddCrossNodesPlugins(t *testing.T) {
	fakePlugin := &FakePlugin{name: "fake"}
	fakePluginA := &FakePluginA{name: "fakeA"}
	testPluginRegistry := framework.PluginMap{
		fakePlugin.Name():  fakePlugin,
		fakePluginA.Name(): fakePluginA,
	}

	schedulerFramework := newTestSchedulerFramework(
		[]framework.PreFilterPlugin{},
		[]framework.FilterPlugin{},
		[]framework.PreScorePlugin{},
		[]framework.ScorePlugin{}, map[string]int64{},
		[]framework.CrossNodesPlugin{})

	schedulerFramework.addFilterPlugin(framework.NewPluginSpec("fake"), testPluginRegistry)
	schedulerFramework.addFilterPlugin(framework.NewPluginSpec("fakeA"), testPluginRegistry)

	var crossNodesPlugins []string
	for _, pl := range schedulerFramework.crossNodesPlugins {
		crossNodesPlugins = append(crossNodesPlugins, pl.Name())
	}
	expectedCrossNodesPlugins := []string{"fakeA"}
	if !reflect.DeepEqual(expectedCrossNodesPlugins, crossNodesPlugins) {
		t.Errorf("expected %v but got %v", expectedCrossNodesPlugins, crossNodesPlugins)
	}
}
