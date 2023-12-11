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

package cmd

import (
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreinformers "k8s.io/client-go/informers/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	podAnnotations "github.com/kubewharf/godel-scheduler/pkg/util/pod"
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

const (
	PodGroupNameLabelIndex = "podGroupNameLabel"
	labelEmpty             = "emptylabel"
)

func LabelIndexFunc(obj interface{}) ([]string, error) {
	metadata, err := meta.Accessor(obj)
	if err != nil {
		return nil, nil
	}

	var indexKeys []string
	for key, value := range metadata.GetLabels() {
		if key == podAnnotations.PodGroupNameAnnotationKey {
			indexKeys = append(indexKeys, value)
		}
	}

	if len(indexKeys) == 0 {
		indexKeys = append(indexKeys, labelEmpty)
	}
	return indexKeys, nil
}

func NewLabelIndexedPodInformer(client clientset.Interface, resyncPeriod time.Duration) coreinformers.PodInformer {
	return newLabelIndexedPodInformer(client, resyncPeriod)
}

func newLabelIndexedPodInformer(client clientset.Interface, resyncPeriod time.Duration) coreinformers.PodInformer {
	index := cache.Indexers{
		cache.NamespaceIndex:   cache.MetaNamespaceIndexFunc,
		PodGroupNameLabelIndex: LabelIndexFunc,
	}

	informer := newFilteredPodInformer(client, metav1.NamespaceAll, resyncPeriod, index, nil)

	return &podInformer{informer: informer}
}
