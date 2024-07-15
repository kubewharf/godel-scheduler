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

package defaultbinder

import (
	"context"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/binder/metrics"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

// Name of the plugin used in the plugin registry and configurations.
const Name = "DefaultBinder"

// DefaultBinder binds pods to nodes using a k8s client.
type DefaultBinder struct {
	handle handle.BinderFrameworkHandle
}

var _ framework.BindPlugin = &DefaultBinder{}

// New creates a DefaultBinder.
func New(_ runtime.Object, handle handle.BinderFrameworkHandle) (framework.Plugin, error) {
	return &DefaultBinder{handle: handle}, nil
}

// Name returns the name of the plugin.
func (b DefaultBinder) Name() string {
	return Name
}

// Bind binds pods to nodes using the k8s client.
func (b DefaultBinder) Bind(ctx context.Context, state *framework.CycleState, p *v1.Pod, nodeName string) *framework.Status {
	klog.V(3).InfoS("Started to bind pod to node", "pod", klog.KObj(p), "nodeName", nodeName)
	binding := &v1.Binding{
		ObjectMeta: metav1.ObjectMeta{Namespace: p.Namespace, Name: p.Name, UID: p.UID},
		Target:     v1.ObjectReference{Kind: "Node", Name: nodeName},
	}

	startTime := time.Now()
	if err := b.handle.ClientSet().CoreV1().Pods(binding.Namespace).Bind(ctx, binding, metav1.CreateOptions{}); err != nil {
		metrics.PodOperatingLatencyObserve(framework.ExtractPodProperty(p), metrics.FailureResult, metrics.BindPod, metrics.SinceInSeconds(startTime))
		return framework.NewStatus(framework.Error, err.Error())
	}
	metrics.PodOperatingLatencyObserve(framework.ExtractPodProperty(p), metrics.SuccessResult, metrics.BindPod, metrics.SinceInSeconds(startTime))
	return nil
}
