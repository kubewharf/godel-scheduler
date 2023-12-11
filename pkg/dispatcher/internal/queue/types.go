/*
Copyright 2018 The Kubernetes Authors.

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

package queue

import (
	"errors"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
)

type KeyFunc func(obj interface{}) (string, error)

// KeyError will be returned any time a KeyFunc gives an error; it includes the object
// at fault.
type KeyError struct {
	Obj interface{}
	Err error
}

// Error gives a human-readable description of the error.
func (k KeyError) Error() string {
	return fmt.Sprintf("couldn't create key for object %+v: %v", k.Obj, k.Err)
}

// ErrFIFOClosed used when FIFO is closed
var ErrFIFOClosed = errors.New("DeltaFIFO: manipulating with closed queue")

// QueuedPodInfo is a Pod wrapper with additional information related to
// the pod's status in dispatcher queue, such as the timestamp when
// it's added to the queue.
type QueuedPodInfo struct {
	PodKey          string
	PodResourceType podutil.PodResourceType

	// The time pod added to the scheduling queue.
	Timestamp time.Time
	// The time when the pod is added to the queue for the first time. The pod may be added
	// back to the queue multiple times before it's successfully dispatched.
	// It shouldn't be updated once initialized. It's used to record the e2e scheduling
	// latency for a pod.
	InitialAddedTimestamp time.Time

	// Tracing context used in dispatcher, which should be passed between different queues
	SpanContext tracing.SpanContext

	// The property of the pod, which is used to describe the pod's attributes
	PodProperty *framework.PodProperty
}

func (qi *QueuedPodInfo) GetPodProperty() *framework.PodProperty {
	return qi.PodProperty
}

func NewQueuedPodInfo(pod *v1.Pod) (*QueuedPodInfo, error) {
	resourceType, err := podutil.GetPodResourceType(pod)
	if err != nil {
		return nil, err
	}
	return &QueuedPodInfo{
		PodKey:          podutil.GetPodKey(pod),
		PodResourceType: resourceType,
		SpanContext:     tracing.GetSpanContextFromPod(pod),
		PodProperty:     framework.ExtractPodProperty(pod),
	}, nil
}

func podInfoKeyFunc(obj interface{}) (string, error) {
	return obj.(*QueuedPodInfo).PodKey, nil
}
