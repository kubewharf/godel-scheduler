/*
Copyright 2019 The Kubernetes Authors.

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
	corev1 "k8s.io/api/core/v1"
	eventsv1beta1 "k8s.io/api/events/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	v1 "k8s.io/client-go/kubernetes/typed/events/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/tools/record"
)

// EventRecorder knows how to record events on behalf of an EventSource.
type EventRecorder interface {
	// Eventf constructs an event from the given information and puts it in the queue for sending.
	// 'regarding' is the object this event is about. Event will make a reference-- or you may also
	// pass a reference to the object directly.
	// 'related' is the secondary object for more complex actions. E.g. when regarding object triggers
	// a creation or deletion of related object.
	// 'type' of this event, and can be one of Normal, Warning. New types could be added in future
	// 'reason' is the reason this event is generated. 'reason' should be short and unique; it
	// should be in UpperCamelCase format (starting with a capital letter). "reason" will be used
	// to automate handling of events, so imagine people writing switch statements to handle them.
	// You want to make that easy.
	// 'action' explains what happened with regarding/what action did the ReportingController
	// (ReportingController is a type of a Controller reporting an Event, e.g. k8s.io/node-controller, k8s.io/kubelet.)
	// take in regarding's name; it should be in UpperCamelCase format (starting with a capital letter).
	// 'note' is intended to be human readable.
	Eventf(regarding runtime.Object, related runtime.Object, eventtype, reason, action, note string, args ...interface{})
}

type EventBroadcasterAdapter interface {
	// StartRecordingToSink starts sending events received from the specified eventBroadcaster.
	StartRecordingToSink(stopCh <-chan struct{})

	// NewRecorder creates a new Event Recorder with specified name.
	NewRecorder(name string) EventRecorder

	// DeprecatedNewLegacyRecorder creates a legacy Event Recorder with specific name.
	DeprecatedNewLegacyRecorder(name string) record.EventRecorder

	// Shutdown shuts down the broadcaster.
	Shutdown()
}

type eventBroadcasterAdapterImpl struct {
	// CoreEventClient is used for events API in core API group, backward compatibility with core.v1 Event type.
	// If connected API server does not support events API, CoreEventClient is used to emit events.
	coreEventClient v1core.EventsGetter
	coreBroadcaster record.EventBroadcaster
	// EventClient is used for events API in events API group, as the new Event API, to improve performance for events API in API server.
	eventClient v1.EventsGetter
	broadcaster events.EventBroadcaster
}

// NewEventBroadcasterAdapter creates a wrapper around new and legacy broadcasters to simplify
// migration of individual components to the new Event API.
func NewEventBroadcasterAdapter(client clientset.Interface) EventBroadcasterAdapter {
	eventClient := &eventBroadcasterAdapterImpl{}
	if _, err := client.Discovery().ServerResourcesForGroupVersion(eventsv1beta1.SchemeGroupVersion.String()); err == nil {
		eventClient.eventClient = client.EventsV1()
		eventClient.broadcaster = events.NewBroadcaster(&events.EventSinkImpl{Interface: client.EventsV1()})
	}
	// Even though there can soon exist cases when coreBroadcaster won't really be needed,
	// we create it unconditionally because its overhead is minor and will simplify using usage
	// patterns of this library in all components.
	eventClient.coreEventClient = client.CoreV1()
	eventClient.coreBroadcaster = record.NewBroadcaster()
	return eventClient
}

// StartRecordingToSink starts sending events received from the specified eventBroadcaster to the given sink.
func (e *eventBroadcasterAdapterImpl) StartRecordingToSink(stopCh <-chan struct{}) {
	if e.broadcaster != nil && e.eventClient != nil {
		e.broadcaster.StartRecordingToSink(stopCh)
	}
	if e.coreBroadcaster != nil && e.coreEventClient != nil {
		e.coreBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: e.coreEventClient.Events("")})
	}
}

func (e *eventBroadcasterAdapterImpl) NewRecorder(name string) EventRecorder {
	if e.broadcaster != nil && e.eventClient != nil {
		return e.broadcaster.NewRecorder(scheme.Scheme, name)
	}
	return record.NewEventRecorderAdapter(e.DeprecatedNewLegacyRecorder(name))
}

func (e *eventBroadcasterAdapterImpl) DeprecatedNewLegacyRecorder(name string) record.EventRecorder {
	return e.coreBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: name})
}

func (e *eventBroadcasterAdapterImpl) Shutdown() {
	if e.coreBroadcaster != nil {
		e.coreBroadcaster.Shutdown()
	}
	if e.broadcaster != nil {
		e.broadcaster.Shutdown()
	}
}
