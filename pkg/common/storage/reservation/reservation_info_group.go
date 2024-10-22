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

package reservation

// ReservationInfoGroup is a collection of ReservationInfo belong to the same placeholder
type ReservationInfoGroup struct {
	// key is placeholderPodKey, value is ReservationInfo
	reservations map[string]*ReservationInfo
	generation   int64
}

func (r *ReservationInfoGroup) GetGeneration() int64 {
	return r.generation
}

func (r *ReservationInfoGroup) SetGeneration(generation int64) {
	r.generation = generation
}

func NewReservationInfoGroup() *ReservationInfoGroup {
	return &ReservationInfoGroup{
		reservations: map[string]*ReservationInfo{},
	}
}

func (r *ReservationInfoGroup) addReservationInfo(reservationInfo *ReservationInfo) {
	r.reservations[reservationInfo.placeholderPodKey] = reservationInfo
}

func (r *ReservationInfoGroup) getReservationInfo(id *ReservationIdentifier) *ReservationInfo {
	return r.reservations[id.placeholderPodKey]
}

func (r *ReservationInfoGroup) deleteReservationInfo(id *ReservationIdentifier) {
	delete(r.reservations, id.placeholderPodKey)
}

func (r *ReservationInfoGroup) list() []*ReservationInfo {
	reservationInfos := make([]*ReservationInfo, 0, len(r.reservations))
	for _, reservationInfo := range r.reservations {
		reservationInfos = append(reservationInfos, reservationInfo)
	}
	return reservationInfos
}

func (r *ReservationInfoGroup) len() int {
	return len(r.reservations)
}

func (r *ReservationInfoGroup) Clone() *ReservationInfoGroup {
	reservations := make(map[string]*ReservationInfo, len(r.reservations))
	for placeholderPodKey, reservationInfo := range r.reservations {
		reservations[placeholderPodKey] = reservationInfo.clone()
	}
	return &ReservationInfoGroup{
		reservations: reservations,
	}
}
