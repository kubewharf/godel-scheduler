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

package config

const (
	PodReservationRequestDefaultTimeOutSeconds = 60
	ReservationDefaultCheckPeriod              = 1
	DefaultMatchedRequestExtraTTL              = 60
)

var DefaultIgnoredNamespace = []string{"batch"}

func SetDefaultReservationController(obj *ReservationControllerConfiguration) {
	if obj.ReservationTTL == 0 {
		obj.ReservationTTL = PodReservationRequestDefaultTimeOutSeconds
	}

	if obj.ReservationCheckPeriod == 0 {
		obj.ReservationCheckPeriod = ReservationDefaultCheckPeriod
	}

	if obj.MatchedRequestExtraTTL == 0 {
		obj.MatchedRequestExtraTTL = DefaultMatchedRequestExtraTTL
	}

	if len(obj.IgnoredNamespace) == 0 {
		obj.IgnoredNamespace = DefaultIgnoredNamespace
	}
}
