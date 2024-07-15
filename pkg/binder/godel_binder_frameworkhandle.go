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
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	"github.com/kubewharf/godel-scheduler/pkg/binder/apis"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/binder/cache"
	binderframework "github.com/kubewharf/godel-scheduler/pkg/binder/framework"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/runtime"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/volume/scheduling"
)

type frameworkHandleImpl struct {
	client             clientset.Interface
	crdClient          godelclient.Interface
	informerFactory    informers.SharedInformerFactory
	crdInformerFactory crdinformers.SharedInformerFactory
	binderCache        godelcache.BinderCache

	// VolumeBinder handles PVC/PV binding for the pod.
	volumeBinder scheduling.GodelVolumeBinder

	// basePlugins is the collection of all plugins supposed to run when a pod is scheduled
	basePlugins *apis.BinderPluginCollection
	// pluginRegistry is the collection of all enabled plugins
	pluginRegistry framework.PluginMap
	// preemptionPluginRegistry is the collection of all enabled preemption plugins
	preemptionPluginRegistry framework.PluginMap
}

func NewFrameworkHandle(
	client clientset.Interface,
	crdClient godelclient.Interface,
	informerFactory informers.SharedInformerFactory,
	crdInformerFactory crdinformers.SharedInformerFactory,
	options binderOptions,
	binderCache godelcache.BinderCache, volumeBindingTimeoutSeconds int64, // TODO: cleanup
) handle.BinderFrameworkHandle {
	h := &frameworkHandleImpl{
		client:             client,
		crdClient:          crdClient,
		informerFactory:    informerFactory,
		crdInformerFactory: crdInformerFactory,
		binderCache:        binderCache,

		volumeBinder: scheduling.NewVolumeBinder(
			client,
			informerFactory.Core().V1().Nodes(),
			informerFactory.Storage().V1().CSINodes(),
			informerFactory.Core().V1().PersistentVolumeClaims(),
			informerFactory.Core().V1().PersistentVolumes(),
			informerFactory.Storage().V1().StorageClasses(),
			time.Duration(volumeBindingTimeoutSeconds)*time.Second,
		),
	}

	pluginMaps, err := binderframework.NewPluginsRegistry(binderframework.NewInTreeRegistry(), options.pluginConfigs, h)
	if err != nil {
		klog.ErrorS(err, "Failed to initialize GodelBinder")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	if pluginMaps == nil {
		klog.ErrorS(nil, "Failed to initialize GodelBinder as plugins registry is not defined")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	preemptionPluginsMaps, err := binderframework.NewPluginsRegistry(binderframework.NewInTreePreemptionRegistry(), options.preemptionPluginConfigs, h)
	if err != nil {
		klog.ErrorS(err, "Failed to initialize GodelBinder")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	h.pluginRegistry = pluginMaps
	h.preemptionPluginRegistry = preemptionPluginsMaps
	h.basePlugins = NewBasePlugins(options.victimCheckingPluginSet)

	return h
}

func (h *frameworkHandleImpl) GetFrameworkForPod(pod *v1.Pod) (framework.BinderFramework, error) {
	// TODO: construct according to pod.Annotation ?
	f := runtime.New(h.pluginRegistry, h.preemptionPluginRegistry, h.basePlugins) //, binder.waitingTasksManager)
	return f, nil
}

func (h *frameworkHandleImpl) ClientSet() clientset.Interface {
	return h.client
}

func (h *frameworkHandleImpl) CRDClientSet() godelclient.Interface {
	return h.crdClient
}

func (h *frameworkHandleImpl) SharedInformerFactory() informers.SharedInformerFactory {
	return h.informerFactory
}

func (h *frameworkHandleImpl) CRDSharedInformerFactory() crdinformers.SharedInformerFactory {
	return h.crdInformerFactory
}

func (h *frameworkHandleImpl) VolumeBinder() scheduling.GodelVolumeBinder {
	return h.volumeBinder
}

func (h *frameworkHandleImpl) GetNodeInfo(nodename string) framework.NodeInfo {
	return h.binderCache.GetNodeInfo(nodename)
}

func (h *frameworkHandleImpl) FindStore(storeName commonstore.StoreName) commonstore.Store {
	return h.binderCache.FindStore(storeName)
}
