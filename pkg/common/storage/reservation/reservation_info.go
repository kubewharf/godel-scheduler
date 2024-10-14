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

import (
	"fmt"
	"time"

	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	v1 "k8s.io/api/core/v1"
)

type ReservationIdentifier struct {
	placeholder       string
	placeholderPodKey string
	nodeName          string
}

func (id *ReservationIdentifier) Clone() *ReservationIdentifier {
	ret := *id
	return &ret
}

func (id *ReservationIdentifier) String() string {
	return fmt.Sprintf("%s/%s/%s", id.placeholder, id.placeholderPodKey, id.nodeName)
}

func GetReservationIdentifier(p *v1.Pod) (*ReservationIdentifier, error) {
	placeholder := podutil.GetReservationPlaceholder(p)
	if len(placeholder) == 0 {
		return nil, fmt.Errorf("empty placeholder")
	}

	placeholderPodKey := podutil.GetPodKey(p)
	if !podutil.IsPlaceholderPod(p) {
		placeholderPodKey = podutil.GetMatchedReservationPlaceholderPod(p)
	}

	if len(placeholderPodKey) == 0 {
		return nil, fmt.Errorf("empty placeholder podKey")
	}

	nodeName := utils.GetNodeNameFromPod(p)
	if len(nodeName) == 0 {
		return nil, fmt.Errorf("empty node name")
	}

	return &ReservationIdentifier{
		placeholder:       placeholder,
		placeholderPodKey: placeholderPodKey,
		nodeName:          nodeName,
	}, nil
}

// ReservationInfo holds the information of reservation
type ReservationInfo struct {
	*ReservationIdentifier
	// original podKey.
	PlaceholderPod *v1.Pod

	// mark the timestamp, if timeout, reservation will be stopped
	// and pod resources will be released.
	CreateTime time.Time

	// TODO: revisit this filed, can we remove this field and use Status instead
	MatchedPod *v1.Pod
}

func (ri *ReservationInfo) IsAvailable() bool {
	if ri == nil {
		return false
	}
	return ri.MatchedPod == nil
}

func NewReservationInfo(placeholderPod, matchedPod *v1.Pod) (*ReservationInfo, error) {
	id, err := GetReservationIdentifier(placeholderPod)
	if err != nil {
		return nil, err
	}

	return &ReservationInfo{
		ReservationIdentifier: id,
		PlaceholderPod:        placeholderPod,
		CreateTime:            time.Now(),
		MatchedPod:            matchedPod,
	}, nil
}

func (ru *ReservationInfo) clone() *ReservationInfo {
	return &ReservationInfo{
		ReservationIdentifier: ru.ReservationIdentifier.Clone(),
		PlaceholderPod:        ru.PlaceholderPod,
		CreateTime:            ru.CreateTime,
		MatchedPod:            ru.MatchedPod,
	}
}
