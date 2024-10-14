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

package config

type ReservationControllerConfiguration struct {
	ReservationTTL         int64
	MatchedRequestExtraTTL int64
	ReservationCheckPeriod int64
	// IgnoredNamespace is the list of namespace to be ignored when
	// setup informer.
	IgnoredNamespace []string
}

func NewReservationControllerConfiguration() *ReservationControllerConfiguration {
	return &ReservationControllerConfiguration{}
}

func (c *ReservationControllerConfiguration) DeepCopyInto(in *ReservationControllerConfiguration) {
	c.ReservationTTL = in.ReservationTTL
	c.MatchedRequestExtraTTL = in.MatchedRequestExtraTTL
	c.ReservationCheckPeriod = in.ReservationCheckPeriod

	if in.IgnoredNamespace != nil {
		c.IgnoredNamespace = make([]string, len(in.IgnoredNamespace))
		copy(c.IgnoredNamespace, in.IgnoredNamespace)
	}
}

func (c *ReservationControllerConfiguration) DeepCopy() (out *ReservationControllerConfiguration) {
	out = new(ReservationControllerConfiguration)
	out.ReservationTTL = c.ReservationTTL
	out.MatchedRequestExtraTTL = c.MatchedRequestExtraTTL
	out.ReservationCheckPeriod = c.ReservationCheckPeriod
	if c.IgnoredNamespace != nil {
		out.IgnoredNamespace = make([]string, len(c.IgnoredNamespace))
		copy(out.IgnoredNamespace, c.IgnoredNamespace)
	}
	return out
}
