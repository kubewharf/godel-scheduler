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

package cache

// TODO: Implement a `MessageQueue` similar to the event mechanism, and collect messages from the Cache
// in specific situations and provide feedback to the SchedulingQueue or other components.
//
// Case 1:
// 	When the AssumedPod expires and is removed, the flush queue logic should be triggered to allow
//	previously unschedulable pods to attempt scheduling again.
//
// Considering the existence of periodic flush logic for unschedulable pods, it would be better to use
// `MessageQueue` to trigger precise flush logic.
//
// We will provide such triggering behavior in the Cache after implementing the `MessageQueue`.
