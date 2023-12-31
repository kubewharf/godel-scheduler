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

package scheduling

import (
	v1 "k8s.io/api/core/v1"
)

// FakeVolumeBinderConfig holds configurations for fake volume binder.
type FakeVolumeBinderConfig struct {
	AllBound    bool
	FindReasons ConflictReasons
	FindErr     error
	AssumeErr   error
	BindErr     error
}

// NewFakeVolumeBinder sets up all the caches needed for the scheduler to make
// topology-aware volume binding decisions.
func NewFakeVolumeBinder(config *FakeVolumeBinderConfig) *FakeVolumeBinder {
	return &FakeVolumeBinder{
		config: config,
	}
}

// FakeVolumeBinder represents a fake volume binder for testing.
type FakeVolumeBinder struct {
	config       *FakeVolumeBinderConfig
	AssumeCalled bool
	BindCalled   bool
}

// FindPodVolumes implements GodelVolumeBinder.FindPodVolumes.
func (b *FakeVolumeBinder) FindPodVolumes(pod *v1.Pod, nodeName string, nodeLabels map[string]string) (reasons ConflictReasons, err error) {
	return b.config.FindReasons, b.config.FindErr
}

// AssumePodVolumes implements GodelVolumeBinder.AssumePodVolumes.
func (b *FakeVolumeBinder) AssumePodVolumes(assumedPod *v1.Pod, nodeName string) (bool, error) {
	b.AssumeCalled = true
	return b.config.AllBound, b.config.AssumeErr
}

// BindPodVolumes implements GodelVolumeBinder.BindPodVolumes.
func (b *FakeVolumeBinder) BindPodVolumes(assumedPod *v1.Pod) error {
	b.BindCalled = true
	return b.config.BindErr
}

// GetBindingsCache implements GodelVolumeBinder.GetBindingsCache.
func (b *FakeVolumeBinder) GetBindingsCache() PodBindingCache {
	return nil
}

// DeletePodBindings implements GodelVolumeBinder.DeletePodBindings.
func (b *FakeVolumeBinder) DeletePodBindings(pod *v1.Pod) {}
