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

package testing

import (
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"

	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	"github.com/kubewharf/godel-scheduler/pkg/binder/apis"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/binder/cache"
	"github.com/kubewharf/godel-scheduler/pkg/binder/framework/handle"
	binderruntime "github.com/kubewharf/godel-scheduler/pkg/binder/framework/runtime"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/volume/scheduling"
)

const (
	volumeBindingTimeoutSeconds int64 = 200
)

type MockBinderFrameworkHandle struct {
	clientSet          clientset.Interface
	crdClient          godelclient.Interface
	informerFactory    informers.SharedInformerFactory
	crdInformerFactory crdinformers.SharedInformerFactory
	cache              godelcache.BinderCache
	volumeBinder       scheduling.GodelVolumeBinder
}

func (mfh *MockBinderFrameworkHandle) ClientSet() clientset.Interface {
	return mfh.clientSet
}

func (mfh *MockBinderFrameworkHandle) CRDClientSet() godelclient.Interface {
	return mfh.crdClient
}

func (mfh *MockBinderFrameworkHandle) SharedInformerFactory() informers.SharedInformerFactory {
	return mfh.informerFactory
}

func (mfh *MockBinderFrameworkHandle) CRDSharedInformerFactory() crdinformers.SharedInformerFactory {
	return mfh.crdInformerFactory
}

func (mfh *MockBinderFrameworkHandle) GetFrameworkForPod(pod *v1.Pod) (f framework.BinderFramework, err error) {
	f = NewBinderFramework(framework.PluginMap{}, framework.PluginMap{}, nil)
	return
}

func (mfh *MockBinderFrameworkHandle) VolumeBinder() scheduling.GodelVolumeBinder {
	return mfh.volumeBinder
}

func (mfh *MockBinderFrameworkHandle) FindStore(storeName commonstore.StoreName) commonstore.Store {
	return mfh.cache.FindStore(storeName)
}

func (mfh *MockBinderFrameworkHandle) GetNodeInfo(nodeName string) framework.NodeInfo {
	return mfh.cache.GetNodeInfo(nodeName)
}

func NewBinderFramework(pluginRegistry, preemptionPluginRegistry framework.PluginMap, basePlugins *apis.BinderPluginCollection) framework.BinderFramework {
	return binderruntime.New(pluginRegistry, preemptionPluginRegistry, basePlugins)
}

func NewBinderFrameworkHandle(
	client clientset.Interface,
	crdClient godelclient.Interface,
	informerFactory informers.SharedInformerFactory,
	crdInformerFactory crdinformers.SharedInformerFactory,
	cache godelcache.BinderCache,
) (handle.BinderFrameworkHandle, error) {
	return &MockBinderFrameworkHandle{
		clientSet:          client,
		crdClient:          crdClient,
		informerFactory:    informerFactory,
		crdInformerFactory: crdInformerFactory,
		cache:              cache,
		volumeBinder: scheduling.NewVolumeBinder(
			client,
			informerFactory.Core().V1().Nodes(),
			informerFactory.Storage().V1().CSINodes(),
			informerFactory.Core().V1().PersistentVolumeClaims(),
			informerFactory.Core().V1().PersistentVolumes(),
			informerFactory.Storage().V1().StorageClasses(),
			time.Duration(volumeBindingTimeoutSeconds)*time.Second,
		),
	}, nil
}
