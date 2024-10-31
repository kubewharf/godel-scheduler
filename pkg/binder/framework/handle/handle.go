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

package handle

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"

	crdclientset "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/volume/scheduling"
)

// TODO: PodFrameworkHandle & UnitFrameworkHandle

// BinderFrameworkHandle provides data and some tools that plugins can use. It is
// passed to the plugin factories at the time of plugin initialization. Plugins
// must store and use this handle to call framework functions.
type BinderFrameworkHandle interface {
	// ClientSet returns a kubernetes clientSet.
	ClientSet() clientset.Interface
	CRDClientSet() crdclientset.Interface
	SharedInformerFactory() informers.SharedInformerFactory
	CRDSharedInformerFactory() crdinformers.SharedInformerFactory
	GetFrameworkForPod(*v1.Pod) (framework.BinderFramework, error)
	VolumeBinder() scheduling.GodelVolumeBinder

	FindStore(storeName commonstore.StoreName) commonstore.Store

	GetNodeInfo(string) framework.NodeInfo

	ListNodeInfos() []framework.NodeInfo
}
