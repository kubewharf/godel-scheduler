/*
Copyright The Godel Scheduler Authors.

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

// Code generated by informer-gen. DO NOT EDIT.

package v1alpha1

import (
	"context"
	time "time"

	schedulingv1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	versioned "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	internalinterfaces "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions/internalinterfaces"
	v1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
)

// SchedulerInformer provides access to a shared informer and lister for
// Schedulers.
type SchedulerInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1alpha1.SchedulerLister
}

type schedulerInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
}

// NewSchedulerInformer constructs a new informer for Scheduler type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewSchedulerInformer(client versioned.Interface, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredSchedulerInformer(client, resyncPeriod, indexers, nil)
}

// NewFilteredSchedulerInformer constructs a new informer for Scheduler type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredSchedulerInformer(client versioned.Interface, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.SchedulingV1alpha1().Schedulers().List(context.TODO(), options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.SchedulingV1alpha1().Schedulers().Watch(context.TODO(), options)
			},
		},
		&schedulingv1alpha1.Scheduler{},
		resyncPeriod,
		indexers,
	)
}

func (f *schedulerInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredSchedulerInformer(client, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *schedulerInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&schedulingv1alpha1.Scheduler{}, f.defaultInformer)
}

func (f *schedulerInformer) Lister() v1alpha1.SchedulerLister {
	return v1alpha1.NewSchedulerLister(f.Informer().GetIndexer())
}
