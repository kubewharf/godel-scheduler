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

package binder

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/binder/apis"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/binder/cache"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/defaultbinder"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/nodeports"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/noderesources"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/nodevolumelimits"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/nonnativeresource"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/plugins/volumebinding"
	"github.com/kubewharf/godel-scheduler/pkg/binder/queue"
	"github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	godelunitqueuesort "github.com/kubewharf/godel-scheduler/pkg/plugins/unitqueuesort"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

// ErrNoNodesAvailable is used to describe the error that no nodes available to schedule pods.
var ErrNoNodesAvailable = fmt.Errorf("no nodes available to schedule pods")

// DefaultQueueSortFunc returns the function to sort pods in scheduling queue
func DefaultQueueSortFunc() framework.LessFunc {
	// If frameworkImpl is nil, simply keep their order unchanged.
	// NOTE: this is primarily for tests.
	// TODO this is fake, to be fixed
	return func(_, _ *framework.QueuedPodInfo) bool { return false }
}

func DefaultUnitQueueSortFunc() framework.UnitLessFunc {
	// TODO: discuss the AttemptImpactFactorOnPriority in binder, use 1e9 for now.
	godelUnitSort, err := godelunitqueuesort.New(nil)
	if err != nil {
		klog.ErrorS(err, "Failed to create default queue sorting function")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	return godelUnitSort.(framework.UnitQueueSortPlugin).Less
}

func NewBasePlugins(victimsCheckingPlugins []*framework.VictimCheckingPluginCollectionSpec) *apis.BinderPluginCollection {
	// TODO add some default plugins later
	basicPlugins := apis.BinderPluginCollection{
		CheckTopology: []string{},
		CheckConflicts: []string{
			noderesources.ConflictCheckName,
			nodevolumelimits.CSIName,
			volumebinding.Name,
			nodeports.Name,
		},
		Permits: []string{},
		Binds: []string{
			defaultbinder.Name,
		},
		VictimCheckings: victimsCheckingPlugins,
	}
	if utilfeature.DefaultFeatureGate.Enabled(features.NonNativeResourceSchedulingSupport) {
		basicPlugins.CheckConflicts = append(basicPlugins.CheckConflicts, nonnativeresource.Name)
	}

	return &basicPlugins
}

// MakeDefaultErrorFunc construct a function to handle pod scheduler error
func MakeDefaultErrorFunc(client clientset.Interface, podLister corelisters.PodLister, podQueue queue.BinderQueue, binderCache godelcache.BinderCache) func(*framework.QueuedPodInfo, error) {
	return func(podInfo *framework.QueuedPodInfo, err error) {
		pod := podInfo.Pod
		if err == ErrNoNodesAvailable {
			klog.V(4).InfoS("Failed to resolve pod conflict; no nodes are registered to the cluster; waiting", "pod", podutil.GetPodKey(pod), "err", err)
		} else if _, ok := err.(*framework.FitError); ok {
			klog.V(4).InfoS("Failed to resolve pod conflict as there is no fit; waiting", "pod", podutil.GetPodKey(pod), "err", err)
		} else if apierrors.IsNotFound(err) {
			klog.V(4).InfoS("Failed to resolve conflict for pod, possibly due to node not being found; waiting", "pod", klog.KObj(pod), "err", err)
			if errStatus, ok := err.(apierrors.APIStatus); ok && errStatus.Status().Details.Kind == "node" {
				nodeName := errStatus.Status().Details.Name
				// when node is not found, We do not remove the node right away. Trying again to get
				// the node and if the node is still not found, then remove it from the scheduler cache.
				_, err := client.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
				if err != nil && apierrors.IsNotFound(err) {
					node := v1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}
					if err := binderCache.DeleteNode(&node); err != nil {
						klog.V(4).InfoS("Failed to remove node from cache as it is not found", "node", klog.KObj(&node), "err", err)
					}
				}
			}
		} else {
			klog.ErrorS(err, "Got an error when resolving conflict pod", "pod", podutil.GetPodKey(pod))
		}
	}
}
