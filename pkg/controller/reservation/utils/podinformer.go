/*
Copyright 2024 The Godel Scheduler Authors.

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

package utils

import (
	"fmt"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	informer "k8s.io/client-go/informers/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

type podInformer struct {
	informer cache.SharedIndexInformer
}

func (i *podInformer) Informer() cache.SharedIndexInformer {
	return i.informer
}

func (i *podInformer) Lister() corelisters.PodLister {
	return corelisters.NewPodLister(i.informer.GetIndexer())
}

func NewFilteredPodInformer(cs clientset.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions func(options *metav1.ListOptions)) informer.PodInformer {
	i := informer.NewFilteredPodInformer(cs, v1.NamespaceAll, resyncPeriod,
		indexers, tweakListOptions)
	return &podInformer{informer: i}
}

func WithIgnoredNamespaceList(nsList []string) func(opt *metav1.ListOptions) {
	if len(nsList) == 0 {
		return func(opt *metav1.ListOptions) {}
	}

	namespaceList := make([]string, 0, len(nsList))
	for _, ns := range nsList {
		namespaceList = append(namespaceList, fmt.Sprintf("metadata.namespace!=%v", ns))
	}

	return func(opt *metav1.ListOptions) {
		opt.FieldSelector = strings.Join(namespaceList, ",")
	}
}
