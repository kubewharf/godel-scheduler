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

package handler

import (
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	coreinformers "k8s.io/client-go/informers/core/v1"
	corelister "k8s.io/client-go/listers/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

type (
	NodeHandler func(string) framework.NodeInfo
	PodHandler  func(string) (*framework.CachePodState, bool)

	PodOpFunc func(pod *v1.Pod, isAdd bool, skippStores sets.String) error
)

type CacheHandler interface {
	SchedulerName() string
	SchedulerType() string
	SubCluster() string
	SwitchType() framework.SwitchType

	// TODO: Revisit this and split the judgment section on whether storage needs to be enabled.
	IsStoreEnabled(string) bool

	TTL() time.Duration
	Period() time.Duration
	StopCh() <-chan struct{}

	PodLister() corelister.PodLister
	PodInformer() coreinformers.PodInformer

	// GetNodeInfo return the NodeInfo before NodeStore handle the event.
	GetNodeInfo(string) framework.NodeInfo
	// GetPodState return the PodState before PodStore handle the event.
	GetPodState(string) (*framework.CachePodState, bool)

	SetNodeHandler(NodeHandler)
	SetPodHandler(PodHandler)

	PodOp(pod *v1.Pod, isAdd bool, skippStores sets.String) error
	SetPodOpFunc(PodOpFunc)
}

type handler struct {
	schedulerName string
	schedulerType string
	subCluster    string
	switchType    framework.SwitchType // Only be used in Snapshot.

	enabledStores sets.String

	ttl    time.Duration
	period time.Duration
	stop   <-chan struct{}

	podLister   corelister.PodLister
	podInformer coreinformers.PodInformer

	nodeHandler NodeHandler
	podHandler  PodHandler

	// Trigger AddPod/RemovePod for other stores in PodStore.
	podOpFunc PodOpFunc
}

var _ CacheHandler = &handler{}

func (h *handler) SchedulerName() string                  { return h.schedulerName }
func (h *handler) SchedulerType() string                  { return h.schedulerType }
func (h *handler) SubCluster() string                     { return h.subCluster }
func (h *handler) SwitchType() framework.SwitchType       { return h.switchType }
func (h *handler) IsStoreEnabled(storeName string) bool   { return h.enabledStores.Has(storeName) }
func (h *handler) TTL() time.Duration                     { return h.ttl }
func (h *handler) Period() time.Duration                  { return h.period }
func (h *handler) StopCh() <-chan struct{}                { return h.stop }
func (h *handler) PodLister() corelister.PodLister        { return h.podLister }
func (h *handler) PodInformer() coreinformers.PodInformer { return h.podInformer }

func (h *handler) GetNodeInfo(nodeName string) framework.NodeInfo          { return h.nodeHandler(nodeName) }
func (h *handler) GetPodState(key string) (*framework.CachePodState, bool) { return h.podHandler(key) }
func (h *handler) SetNodeHandler(f NodeHandler)                            { h.nodeHandler = f }
func (h *handler) SetPodHandler(f PodHandler)                              { h.podHandler = f }

func (h *handler) PodOp(pod *v1.Pod, isAdd bool, skippStores sets.String) error {
	return h.podOpFunc(pod, isAdd, skippStores)
}
func (h *handler) SetPodOpFunc(f PodOpFunc) { h.podOpFunc = f }

// --------------------------------------------------------

type handlerWrapper struct{ obj *handler }

func MakeCacheHandlerWrapper() *handlerWrapper {
	return &handlerWrapper{&handler{enabledStores: sets.NewString()}}
}

func (w *handlerWrapper) Obj() CacheHandler {
	return w.obj
}

func (w *handlerWrapper) SchedulerName(schedulerName string) *handlerWrapper {
	w.obj.schedulerName = schedulerName
	return w
}

func (w *handlerWrapper) SchedulerType(schedulerType string) *handlerWrapper {
	w.obj.schedulerType = schedulerType
	return w
}

func (w *handlerWrapper) SubCluster(subCluster string) *handlerWrapper {
	w.obj.subCluster = subCluster
	return w
}

func (w *handlerWrapper) SwitchType(switchType framework.SwitchType) *handlerWrapper {
	w.obj.switchType = switchType
	return w
}

func (w *handlerWrapper) EnableStore(storeNames ...string) *handlerWrapper {
	w.obj.enabledStores.Insert(storeNames...)
	return w
}

func (w *handlerWrapper) TTL(ttl time.Duration) *handlerWrapper {
	w.obj.ttl = ttl
	return w
}

func (w *handlerWrapper) Period(period time.Duration) *handlerWrapper {
	w.obj.period = period
	return w
}

func (w *handlerWrapper) StopCh(stop <-chan struct{}) *handlerWrapper {
	w.obj.stop = stop
	return w
}

func (w *handlerWrapper) PodLister(podLister corelister.PodLister) *handlerWrapper {
	w.obj.podLister = podLister
	return w
}

func (w *handlerWrapper) PodInformer(podInformer coreinformers.PodInformer) *handlerWrapper {
	w.obj.podInformer = podInformer
	return w
}

func (w *handlerWrapper) NodeHandler(h NodeHandler) *handlerWrapper {
	w.obj.nodeHandler = h
	return w
}

func (w *handlerWrapper) PodHandler(h PodHandler) *handlerWrapper {
	w.obj.podHandler = h
	return w
}

func (w *handlerWrapper) PodOpFunc(f PodOpFunc) *handlerWrapper {
	w.obj.podOpFunc = f
	return w
}
