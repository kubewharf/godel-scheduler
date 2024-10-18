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

package reservationstore

import (
	"fmt"
	"sync"
	"time"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	"github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	"github.com/kubewharf/godel-scheduler/pkg/common/storage/reservation"
	commonstore "github.com/kubewharf/godel-scheduler/pkg/common/store"
	"github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/utils"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	deployutil "github.com/kubewharf/godel-scheduler/pkg/util/deployment"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
)

const Name commonstore.StoreName = "ReservationStore"

func (c *ReservationStore) Name() commonstore.StoreName {
	return Name
}

func init() {
	commonstores.GlobalRegistries.Register(
		Name,
		func(h commoncache.CacheHandler) bool {
			return utilfeature.DefaultFeatureGate.Enabled(features.ResourceReservation)
		},
		NewCache,
		NewSnapshot)
}

// -------------------------------------- ReservationStore --------------------------------------

type ReservationStore struct {
	commonstore.BaseStore
	storeType commonstore.StoreType
	handler   commoncache.CacheHandler

	reservations *reservation.NodeReservationStore

	deployWithReservationRequirement map[string]bool
	assumedPlaceholderPods           map[string]*time.Time
	clr                              *collector
}

var _ commonstore.Store = &ReservationStore{}

func NewCache(handler commoncache.CacheHandler) commonstore.Store {
	return &ReservationStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Cache,
		handler:   handler,

		reservations:                     reservation.NewCacheNodeReservationStore(),
		deployWithReservationRequirement: make(map[string]bool),
		assumedPlaceholderPods:           make(map[string]*time.Time),
		clr:                              newCollector(),
	}
}

func NewSnapshot(handler commoncache.CacheHandler) commonstore.Store {
	return &ReservationStore{
		BaseStore: commonstore.NewBaseStore(),
		storeType: commonstore.Snapshot,
		handler:   handler,

		reservations:                     reservation.NewSnapshotNodeReservationStore(),
		deployWithReservationRequirement: make(map[string]bool),
		assumedPlaceholderPods:           make(map[string]*time.Time),
	}
}

func (s *ReservationStore) addPlaceholderPod(fakePod *v1.Pod) error {
	info, err := reservation.NewReservationInfo(fakePod, nil)
	if err != nil {
		return fmt.Errorf("failed to new reservation info, %v", err)
	}

	s.reservations.AddReservationInfo(info)
	klog.V(4).InfoS("succeed to add pod reservation", "identifier", info.ReservationIdentifier)
	if s.clr != nil {
		s.clr.add(fakePod, availableStatus)
	}
	return nil
}

func (s *ReservationStore) getReservationInfoByPod(fakePod *v1.Pod) (*reservation.ReservationInfo, error) {
	id, err := reservation.GetReservationIdentifier(fakePod)
	if err != nil {
		return nil, fmt.Errorf("failed to get reservationInfo identifier, %v", err)
	}

	info := s.reservations.GetReservationInfo(id)
	if info == nil {
		return nil, fmt.Errorf("reservation info not found")
	}
	return info, nil
}

func (s *ReservationStore) updatePlaceholderPod(oldFakePod, newFakePod *v1.Pod) error {
	ph := podutil.GetReservationPlaceholder(newFakePod)
	if ph == "" {
		return fmt.Errorf("failed to get placeholder : %s/%s", newFakePod.Namespace, newFakePod.Name)
	}

	err := s.deletePlaceholderPod(oldFakePod)
	if err != nil {
		return err
	}

	err = s.addPlaceholderPod(newFakePod)
	if err != nil {
		return err
	}

	return nil
}

func (s *ReservationStore) deletePlaceholderPod(fakePod *v1.Pod) error {
	id, err := reservation.GetReservationIdentifier(fakePod)
	if err != nil {
		return fmt.Errorf("failed to get reservationInfo identifier, %v", err)
	}

	s.reservations.DeleteReservationInfo(id)
	klog.V(4).InfoS("succeed to remove reservation info", "identifier", id)
	if s.clr != nil {
		s.clr.delete(fakePod)
	}
	return nil
}

func (s *ReservationStore) resetMatchedPod(fakePod *v1.Pod) (err error) {
	id, err := reservation.GetReservationIdentifier(fakePod)
	if err != nil {
		return fmt.Errorf("failed to get reservationInfo identifier, %v", err)
	}

	if err := s.reservations.SetMatchedPodForReservation(id, nil); err != nil {
		return err
	}

	if s.clr != nil {
		s.clr.add(fakePod, availableStatus)
	}
	return nil
}

func (s *ReservationStore) setMatchedPod(fakePod, matchedPod *v1.Pod) (err error) {
	id, err := reservation.GetReservationIdentifier(fakePod)
	if err != nil {
		return fmt.Errorf("failed to get reservationInfo identifier, %v", err)
	}

	if err := s.reservations.SetMatchedPodForReservation(id, matchedPod); err != nil {
		return err
	}

	if s.clr != nil {
		s.clr.add(fakePod, matchedStatus)
	}
	return nil
}

// --------------------------------------Event Handler-----------------------------------------

func (s *ReservationStore) shouldReserveResources(pod *v1.Pod) bool {
	if pod == nil || !podutil.BoundPod(pod) {
		return false
	}
	if podutil.HasReservationRequirement(pod) {
		return true
	}
	deployName := util.GetDeployNameFromPod(pod)
	key := pod.Namespace + "/" + deployName
	_, ok := s.deployWithReservationRequirement[key]
	return ok
}

func (s *ReservationStore) UpdatePod(oldPod, newPod *v1.Pod) error {
	placeholder := podutil.GetReservationPlaceholder(newPod)
	if len(placeholder) == 0 {
		return nil
	}

	if !podutil.BoundPod(oldPod) && podutil.BoundPod(newPod) {
		// pod was assumed by scheduler
		placeholderKey := podutil.GetMatchedReservationPlaceholderPod(newPod)
		if len(placeholderKey) == 0 {
			return nil
		}

		unit, err := s.getReservationInfoByPod(newPod)
		if err != nil {
			return fmt.Errorf("failed to get reservationInfo, %v", err)
		}

		if err := s.consumeReservedResources(newPod, unit.PlaceholderPod); err != nil {
			return fmt.Errorf("failed to consume reserved resource, %v", err)
		}

		klog.V(4).InfoS("succeed to consume reserved resource", "pod", klog.KObj(newPod), "placeholder", placeholder, "placeholderKey", placeholderKey)
	}
	return nil
}

func (s *ReservationStore) DeletePod(pod *v1.Pod) error {
	placeholder := podutil.GetReservationPlaceholder(pod)
	if len(placeholder) == 0 {
		return nil
	}

	uid, err := podutil.GetPodUID(pod)
	if err != nil {
		return err
	}

	state, isAssumed := s.handler.GetPodState(uid)

	// bound pod was deleted by controller
	if (state != nil && !isAssumed) && s.shouldReserveResources(pod) {
		// if resources the pod owns need to be reserved, we should stop deleting from cache
		// and start a timer, then reserve the resources for a period of time.
		fakePod := podutil.CreateReservationFakePodForAssume(pod)

		// assume reservation fake pod and wait for add event of pod reservation request.
		if err := s.assumePodReservation(fakePod); err != nil {
			return fmt.Errorf("failed to reserve resources for scheduled pod %s, %s", klog.KObj(pod), err)
		}

		klog.V(4).InfoS("succeed to assume pod resource for removed pod", "pod", klog.KObj(pod), "placeholder", placeholder)
		return nil
	}

	// assumed pod was expired, we should reserve resource again
	if isAssumed {
		matchedPod := podutil.GetMatchedReservationPlaceholderPod(pod)
		if len(matchedPod) != 0 {
			placeholderKey := podutil.GetMatchedReservationPlaceholderPod(pod)
			if len(placeholderKey) == 0 {
				return nil
			}

			unit, err := s.getReservationInfoByPod(pod)
			if err != nil {
				return fmt.Errorf("failed to get reservationInfo, %v", err)
			}

			if err := s.reservePodResources(unit.PlaceholderPod); err != nil {
				return fmt.Errorf("failed to reserve resource, %v", err)
			}

			klog.V(4).InfoS("succeed to reserve pod resource for expired pod", "pod", klog.KObj(pod), "placeholder", placeholder, "placeholderKey", placeholder)
		}
	}

	return nil
}

func (s *ReservationStore) AssumePod(podInfo *framework.CachePodInfo) error {
	placeholder := podutil.GetReservationPlaceholder(podInfo.Pod)
	if placeholder != "" {
		placeholderKey := podutil.GetMatchedReservationPlaceholderPod(podInfo.Pod)
		if len(placeholderKey) == 0 {
			return nil
		}

		unit, err := s.getReservationInfoByPod(podInfo.Pod)
		if err != nil {
			return fmt.Errorf("failed to get reservationInfo when assume pod, %v", err)
		}

		if err := s.consumeReservedResources(podInfo.Pod, unit.PlaceholderPod); err != nil {
			return fmt.Errorf("failed to consume reserved resource, %v", err)
		}
		klog.V(4).InfoS("succeed to consume reserved resource for assumed pod", "pod", klog.KObj(podInfo.Pod), "placeholder", placeholder, "placeholderKey", placeholderKey)
	}
	return nil
}

func (s *ReservationStore) ForgetPod(podInfo *framework.CachePodInfo) error {
	placeholder := podutil.GetReservationPlaceholder(podInfo.Pod)
	if placeholder != "" {
		placeholderKey := podutil.GetMatchedReservationPlaceholderPod(podInfo.Pod)
		if len(placeholderKey) == 0 {
			return nil
		}

		unit, err := s.getReservationInfoByPod(podInfo.Pod)
		if err != nil {
			return fmt.Errorf("failed to get reservationInfo when assume pod, %v", err)
		}

		if err := s.reservePodResources(unit.PlaceholderPod); err != nil {
			return fmt.Errorf("failed to reserve resource, %v", err)
		}

		klog.V(4).InfoS("succeed to consume reserved resource for forgot pod", "pod", klog.KObj(podInfo.Pod), "placeholder", placeholder, "placeholderKey", placeholderKey)
	}
	return nil
}

func (r *ReservationStore) AddDeployment(deploy *appsv1.Deployment) error {
	if !deployutil.DeployHasReservationRequirement(deploy) {
		return nil
	}
	key := deployutil.GetDeployKey(deploy)
	r.deployWithReservationRequirement[key] = true
	return nil
}

func (r *ReservationStore) UpdateDeployment(oldDeploy, newDeploy *appsv1.Deployment) error {
	if !deployutil.DeployHasReservationRequirement(oldDeploy) && deployutil.DeployHasReservationRequirement(newDeploy) {
		key := deployutil.GetDeployKey(newDeploy)
		r.deployWithReservationRequirement[key] = true
	} else if deployutil.DeployHasReservationRequirement(oldDeploy) && !deployutil.DeployHasReservationRequirement(newDeploy) {
		key := deployutil.GetDeployKey(oldDeploy)
		delete(r.deployWithReservationRequirement, key)
	}
	return nil
}

func (r *ReservationStore) DeleteDeployment(deploy *appsv1.Deployment) error {
	if !deployutil.DeployHasReservationRequirement(deploy) {
		return nil
	}
	key := deployutil.GetDeployKey(deploy)
	delete(r.deployWithReservationRequirement, key)
	return nil
}

func (s *ReservationStore) AddReservation(res *schedulingv1a1.Reservation) error {
	if podutil.ShouldOccupyResources(res) {
		pod := podutil.ConvertReservationToPod(res)
		key, _ := podutil.GetPodUID(pod)

		assumedPlaceholderPods := s.assumedPlaceholderPods
		if _, exist := assumedPlaceholderPods[key]; exist {
			// already assumed
			delete(assumedPlaceholderPods, key)
			return nil
		}

		if err := s.addFakePod(pod); err != nil {
			return fmt.Errorf("failed to add fake pod, %v", err)
		}

		if err := s.addPlaceholderPod(pod); err != nil {
			return fmt.Errorf("failed to add reservationInfo, %v", err)
		}

		klog.V(4).InfoS("succeed to add reservation", "reservation", klog.KObj(res))
	}

	return nil
}

func (s *ReservationStore) UpdateReservation(oldRes, newRes *schedulingv1a1.Reservation) error {
	switch true {
	case !podutil.ShouldOccupyResources(oldRes) && podutil.ShouldOccupyResources(newRes):
		fakePod := podutil.ConvertReservationToPod(newRes)
		if err := s.reservePodResources(fakePod); err != nil {
			return fmt.Errorf("failed to reserve resource, %v", err)
		}

		klog.V(4).InfoS("succeed to to reserve pod resource", "reservation", klog.KObj(newRes))
	case podutil.ShouldOccupyResources(oldRes) && !podutil.ShouldOccupyResources(newRes):
		fakePod := podutil.ConvertReservationToPod(oldRes)
		ps, _ := s.handler.GetPodState(string(newRes.Status.CurrentOwners.UID))
		// owner pod must already in cache
		if ps == nil || ps.Pod == nil {
			klog.V(4).InfoS("owner pod not found in cache, skip consume reserved resource",
				"reservation", klog.KObj(newRes), "owner", newRes.Status.CurrentOwners)
			return nil
		}

		if err := s.consumeReservedResources(ps.Pod, fakePod); err != nil {
			return fmt.Errorf("failed to consume reserved resource, %v", err)
		}

		klog.V(4).InfoS("succeed to consume reserved resource", "reservation", klog.KObj(newRes))

	case podutil.ShouldOccupyResources(oldRes) && podutil.ShouldOccupyResources(newRes):
		// TODO: figure out which situation will update reservation, this way will override existed unit
		oldPod := podutil.ConvertReservationToPod(oldRes)
		newPod := podutil.ConvertReservationToPod(newRes)
		return s.updateReservedResources(oldPod, newPod)
	default:
		klog.V(4).InfoS("skip updating reservation", "old Reservation", podutil.GetReservationKey(oldRes), "new Reservation", podutil.GetReservationKey(newRes))
	}
	return nil
}

func (s *ReservationStore) DeleteReservation(res *schedulingv1a1.Reservation) error {
	fakePod := podutil.ConvertReservationToPod(res)
	if err := s.removeFakePod(fakePod); err != nil {
		return fmt.Errorf("failed to remove fake pod, %v", err)
	}

	if err := s.deletePlaceholderPod(podutil.ConvertReservationToPod(res)); err != nil {
		return fmt.Errorf("failed to remove reservationInfo, %v", err)
	}

	klog.V(4).InfoS("succeed to delete reservation", "reservation", klog.KObj(res))
	return nil
}

func (s *ReservationStore) PeriodWorker(mu *sync.RWMutex) {
	go wait.Until(func() {
		mu.Lock()
		defer mu.Unlock()

		s.CleanupExpiredAssumedPodReservation(time.Now())
		if s.clr != nil {
			s.clr.emit()
		}
	}, s.handler.Period(), s.handler.StopCh())
}

func (s *ReservationStore) CleanupExpiredAssumedPodReservation(now time.Time) {
	// The size of assumedPods should be small
	// TODO: sorting by expired time, so wo can accelerate the cleanup process
	keys := make([]string, 0)
	for key, dl := range s.assumedPlaceholderPods {
		if now.After(*dl) {
			ps, _ := s.handler.GetPodState(key)
			if ps != nil && ps.Pod != nil {
				// Use the pod stored in Cache instead of oldPod.
				if err := s.removeFakePod(ps.Pod); err != nil {
					klog.V(4).ErrorS(err, "failed to remove fake pod", "pod", klog.KObj(ps.Pod))
				}
				keys = append(keys, klog.KObj(ps.Pod).String())
				s.deletePlaceholderPod(ps.Pod)
				if s.clr != nil {
					s.clr.expire(ps.Pod)
				}
			}
			delete(s.assumedPlaceholderPods, key)
		}
	}

	if len(keys) > 0 {
		klog.InfoS("succeed to cleanup expired assumed placeholder", "list", keys)
	}
}

func (s *ReservationStore) UpdateSnapshot(store commonstore.Store) error {
	return nil
}

// -------------------------------------- Other Interface --------------------------------------

func (s *ReservationStore) addFakePod(pod *v1.Pod) error {
	if s.storeType == commonstore.Cache {
		key, _ := podutil.GetPodUID(pod)
		if ps, isAssumed := s.handler.GetPodState(key); ps != nil || isAssumed {
			// fakePod already exists in cache, do nothing.
			return nil
		}

		return s.handler.PodOp(pod, true, sets.NewString(string(Name))) // Need skip reservation store
	}
	return nil
}

func (s *ReservationStore) removeFakePod(pod *v1.Pod) error {
	if s.storeType == commonstore.Cache {
		key, _ := podutil.GetPodUID(pod)
		if ps, isAssumed := s.handler.GetPodState(key); ps == nil && !isAssumed {
			// fakePod not existed in cache, do nothing.
			return nil
		}

		return s.handler.PodOp(pod, false, sets.NewString(string(Name))) // Need skip reservation store
	}
	return nil
}

func (s *ReservationStore) updateFakePod(oldPod, newPod *v1.Pod) error {
	// Remove the oldPod if existed.
	{
		key, err := podutil.GetPodUID(oldPod)
		if err != nil {
			return err
		}
		if ps, _ := s.handler.GetPodState(key); ps != nil {
			// Use the pod stored in Cache instead of oldPod.
			if err := s.removeFakePod(ps.Pod); err != nil {
				return err
			}
		}
	}
	// Add the newPod if needed.
	{
		if err := s.addFakePod(newPod); err != nil {
			return err
		}
	}

	return nil
}

// reserve resources with a fake pod.
func (s *ReservationStore) reservePodResources(fakePod *v1.Pod) error {
	if err := s.addFakePod(fakePod); err != nil {
		return fmt.Errorf("failed to add fake fakePod, %v", err)
	}

	if err := s.resetMatchedPod(fakePod); err != nil {
		return fmt.Errorf("failed to reset reservationInfo, %v", err)
	}

	return nil
}

func (s *ReservationStore) consumeReservedResources(matchedPod, fakePod *v1.Pod) (err error) {
	if err := s.removeFakePod(fakePod); err != nil {
		return fmt.Errorf("failed to remove fake pod, %v", err)
	}

	if err := s.setMatchedPod(fakePod, matchedPod); err != nil {
		return fmt.Errorf("failed to set matched pod for reservationInfo, %v", err)
	}

	return nil
}

func (s *ReservationStore) updateReservedResources(oldPod, newPod *v1.Pod) (err error) {
	err = s.updatePlaceholderPod(oldPod, newPod)
	if err != nil {
		klog.ErrorS(err, "update reserved resource failed")
	}

	if err = s.updateFakePod(oldPod, newPod); err != nil {
		klog.ErrorS(err, "update reserved resource failed")
	}
	return nil
}

// assume pod reservation only care about node resource,
// ignore topology information at first stage.
func (s *ReservationStore) assumePodReservation(pod *v1.Pod) error {
	key, err := podutil.GetPodUID(pod)
	if err != nil {
		return err
	}

	nodeName := utils.GetNodeNameFromPod(pod)
	if nodeName == "" {
		err = fmt.Errorf("pod %v is assigned to empty node", pod.Name)
		klog.ErrorS(err, "failed to assume pod reservation", "pod", klog.KObj(pod))
		return nil
	}

	reservationInfo, _ := s.getReservationInfoByPod(pod)
	if reservationInfo != nil {
		klog.V(4).InfoS("reservationInfo is already exist, skip assume", "pod", podutil.GetPodKey(pod), "key", key)
		return nil
	}

	// add reservation information
	s.addFakePod(pod)
	s.addPlaceholderPod(pod)

	// set pod in assumed status.
	// reservation assumed status will expire in ttl,
	// which should be the shorter one of cacheTTL and reservationTTL.
	ttl := s.handler.PodAssumedTTL()
	if ttl > s.handler.ReservationTTL() {
		ttl = s.handler.ReservationTTL()
	}

	dl := time.Now().Add(ttl)
	s.assumedPlaceholderPods[key] = &dl

	klog.V(4).InfoS("succeed to assume pod reservation", "pod", podutil.GetPodKey(pod), "key", key, "ttl", ttl)
	return nil
}

// --------------------------- manipulate reservationInfo store ---------------------------

// ATTENTION: differ from Scheduler Store.

func (s *ReservationStore) GetAvailablePlaceholderPod(pod *v1.Pod) (*v1.Pod, error) {
	identifier, err := reservation.GetReservationIdentifier(pod)
	if err != nil {
		return nil, fmt.Errorf("failed to get reservation identifier, %v", err)
	}
	reservationInfo := s.reservations.GetReservationInfo(identifier)
	if reservationInfo == nil {
		return nil, fmt.Errorf("reservation info %s not found", identifier.String())
	}

	if !reservationInfo.IsAvailable() {
		return nil, fmt.Errorf("reservation placeholder is already matched")
	}
	return reservationInfo.PlaceholderPod, nil
}
