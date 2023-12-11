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

package helper

import "time"

const TimestampLayout = time.RFC3339Nano

// SinceDuration gets the duration since the specified start.
func SinceDuration(start time.Time) string {
	return time.Since(start).String()
}

// SinceInSeconds gets the time since the specified start in seconds.
func SinceInSeconds(start time.Time) float64 {
	return time.Since(start).Seconds()
}

// BetweenInSeconds gets the time between the specified time scope in seconds.
func BetweenInSeconds(from, to time.Time) float64 {
	if from.After(to) {
		return from.Sub(to).Seconds()
	}
	return to.Sub(from).Seconds()
}
