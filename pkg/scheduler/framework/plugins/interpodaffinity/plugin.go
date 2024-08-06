/*
Copyright 2019 The Kubernetes Authors.

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

package interpodaffinity

import (
	"fmt"

	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/validation"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/handle"
	"k8s.io/apimachinery/pkg/runtime"
)

// Name is the name of the plugin used in the plugin registry and configurations.
const Name = "InterPodAffinity"

var _ framework.PreFilterPlugin = &InterPodAffinity{}
var _ framework.FilterPlugin = &InterPodAffinity{}
var _ framework.PreScorePlugin = &InterPodAffinity{}
var _ framework.ScorePlugin = &InterPodAffinity{}

// InterPodAffinity is a plugin that checks inter pod affinity
type InterPodAffinity struct {
	args         config.InterPodAffinityArgs
	sharedLister framework.SharedLister
}

// Name returns name of the plugin. It is used in logs, etc.
func (pl *InterPodAffinity) Name() string {
	return Name
}

// New initializes a new plugin and returns it.
func New(plArgs runtime.Object, h handle.PodFrameworkHandle) (framework.Plugin, error) {
	if h.SnapshotSharedLister() == nil {
		return nil, fmt.Errorf("SnapshotSharedlister is nil")
	}
	args, err := getArgs(plArgs)
	if err != nil {
		return nil, err
	}
	if err := validation.ValidateInterPodAffinityArgs(args); err != nil {
		return nil, err
	}
	return &InterPodAffinity{
		args:         args,
		sharedLister: h.SnapshotSharedLister(),
	}, nil
}

func getArgs(obj runtime.Object) (config.InterPodAffinityArgs, error) {
	if obj == nil {
		return config.InterPodAffinityArgs{}, nil
	}

	ptr, ok := obj.(*config.InterPodAffinityArgs)
	if !ok {
		return config.InterPodAffinityArgs{}, fmt.Errorf("want args to be of type InterPodAffinityArgs, got %T", obj)
	}
	return *ptr, nil
}
