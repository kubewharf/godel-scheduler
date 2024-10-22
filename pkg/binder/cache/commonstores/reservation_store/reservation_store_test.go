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
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

	schedulingv1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	podstore "github.com/kubewharf/godel-scheduler/pkg/binder/cache/commonstores/pod_store"
	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	testing_helper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	"gotest.tools/assert"
)

var (
	reservationTTL = 10 * time.Second
	fakeHandler    commoncache.CacheHandler
	podStore       *podstore.PodStore
)

func makeCacheHandler() commoncache.CacheHandler {
	cacheHandler := commoncache.MakeCacheHandlerWrapper().
		ComponentName("godel-scheduler-0").SchedulerType("godel-scheduler").SubCluster(framework.DefaultSubCluster).
		PodAssumedTTL(15 * time.Minute).Period(10 * time.Second).ReservationTTL(reservationTTL).StopCh(wait.NeverStop).Obj()

	podStore = podstore.NewCache(cacheHandler).(*podstore.PodStore)
	cacheHandler.SetPodHandler(podStore.GetPodState)

	cacheHandler.SetPodOpFunc(func(pod *v1.Pod, isAdd bool, skippedStores sets.String) error {
		if isAdd {
			return podStore.AddPod(pod)
		}
		return podStore.DeletePod(pod)
	})
	return cacheHandler
}

func addPodStore(pod *v1.Pod) error {
	return podStore.AddPod(pod)
}

func assumePodStore(pod *v1.Pod) error {
	key, err := podutil.GetPodUID(pod)
	if err != nil {
		return err
	}
	if _, ok := podStore.PodStates[key]; ok {
		return fmt.Errorf("pod %v is in the cache, so can't be assumed", key)
	}

	podStore.PodStates[key] = &framework.CachePodState{Pod: pod}
	podStore.AssumedPods[key] = true
	return nil
}

func forceCleanupAssumedReservation(s *ReservationStore) error {
	for key := range s.assumedPlaceholderPods {
		ps, _ := s.handler.GetPodState(key)
		if ps != nil && ps.Pod != nil {
			// Use the pod stored in Cache instead of oldPod.
			if err := s.removeFakePod(ps.Pod); err != nil {
				return fmt.Errorf("failed to remove fake pod %s, %v", key, err)
			}

			err := s.deletePlaceholderPod(ps.Pod)
			if err != nil {
				return fmt.Errorf("failed to delete placeholder %s, %v", key, err)
			}
		}
		delete(s.assumedPlaceholderPods, key)
	}
	return nil
}

func getPlaceholderPodKey(pod *v1.Pod) string {
	if podutil.IsPlaceholderPod(pod) {
		return podutil.GetPodKey(pod)
	}
	return fmt.Sprintf("%s/%s%s", pod.Namespace, pod.Name, podutil.ReservationPlaceholderPostFix)
}

func checkReservationIsReleased(t *testing.T, cache *ReservationStore, placeholderPod *v1.Pod) {
	_, err := cache.getReservationInfoByPod(placeholderPod)
	assert.ErrorContains(t, err, "reservation info not found", "reservation info should be deleted")

	key, _ := podutil.GetPodUID(placeholderPod)
	ps, _ := cache.handler.GetPodState(key)
	if ps != nil {
		t.Fatalf("placeholder pod %s should be deleted", podutil.GetPodKey(placeholderPod))
	}
}

func checkReservationIsConsumed(t *testing.T, cache *ReservationStore, placeholderPod *v1.Pod) {
	resInfo, _ := cache.getReservationInfoByPod(placeholderPod)
	if resInfo.IsAvailable() {
		t.Fatalf("reservation %s should be unavailable", podutil.GetPodKey(placeholderPod))
	}

	key, _ := podutil.GetPodUID(placeholderPod)
	ps, _ := cache.handler.GetPodState(key)
	if ps != nil {
		t.Fatalf("placeholder pod %s should be deleted from pod store", podutil.GetPodKey(placeholderPod))
	}
}

func checkReservationIsReserved(t *testing.T, cache *ReservationStore, placeholderPod *v1.Pod) {
	resInfo, _ := cache.getReservationInfoByPod(placeholderPod)
	if !resInfo.IsAvailable() {
		t.Fatalf("reservation %s should be available", podutil.GetPodKey(placeholderPod))
	}

	key, _ := podutil.GetPodUID(placeholderPod)
	ps, _ := cache.handler.GetPodState(key)
	if ps == nil {
		t.Fatalf("placeholder pod %s should be added into pod store", podutil.GetPodKey(placeholderPod))
	}
}

func reserveReservation(t *testing.T, cache *ReservationStore, placeholderPod *v1.Pod) {
	assert.NilError(t, cache.handler.PodOp(placeholderPod, true, sets.NewString()), "failed to add placeholder pod", "pod", podutil.GetPodKey(placeholderPod))
	assert.NilError(t, cache.addPlaceholderPod(placeholderPod), "failed to add placeholder pod", "pod", podutil.GetPodKey(placeholderPod))
	checkReservationIsReserved(t, cache, placeholderPod)
}

func TestReservationStore_DeletePod(t *testing.T) {
	reqPod := testing_helper.MakePod().Name("req-pod").
		UID("req-pod").
		Annotation(podutil.PodResourceReservationAnnotationForGodel, "true").
		Annotation(podutil.ReservationIndexAnnotation, "ph").
		Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
		Node("node").Obj()
	placeholderPod := podutil.CreateReservationFakePod(reqPod)

	backPod := testing_helper.MakePod().Name("back-pod").
		UID("back-pod").
		Annotation(podutil.ReservationIndexAnnotation, "ph").
		Annotation(podutil.MatchedReservationPlaceholderKey, getPlaceholderPodKey(reqPod)).
		Annotation(podutil.AssumedNodeAnnotationKey, "node").
		Obj()

	// case 1: bound pod will be reserved
	{
		fakeHandler = makeCacheHandler()
		cache := NewCache(fakeHandler).(*ReservationStore)

		// Pod ADD event
		assert.NilError(t, addPodStore(reqPod), "failed to add reqPod into pod store")

		// Pod DELETE event
		assert.NilError(t, cache.DeletePod(reqPod), "failed to delete reqPod from reservation store")

		checkReservationIsReserved(t, cache, placeholderPod)
	}

	// case 2: assumed pod will not be reserved
	{
		fakeHandler = makeCacheHandler()
		cache := NewCache(fakeHandler).(*ReservationStore)

		// Assume Pod
		assert.NilError(t, assumePodStore(reqPod), "failed to assumed reqPod")

		// Pod DELETE event
		assert.NilError(t, cache.DeletePod(reqPod), "failed to delete reqPod from reservation store")

		// check reservation info not exist
		pp, err := cache.getReservationInfoByPod(placeholderPod)
		if pp != nil || err == nil {
			t.Fatalf("assumed pod should not be reserved")
		}
	}

	// case 3: assumed back pod expired, resource will be reserved again
	{
		fakeHandler = makeCacheHandler()
		cache := NewCache(fakeHandler).(*ReservationStore)

		reserveReservation(t, cache, placeholderPod)

		// consume reserved resource
		assert.NilError(t, cache.AssumePod(&framework.CachePodInfo{Pod: backPod}), "failed to consume reserved resource")
		assert.NilError(t, assumePodStore(backPod), "failed to consume reserved resource")
		checkReservationIsConsumed(t, cache, placeholderPod)

		// Assumed Pod expired
		assert.NilError(t, cache.DeletePod(backPod), "failed to delete reqPod from reservation store")
		checkReservationIsReserved(t, cache, placeholderPod)
	}
}

func TestReservationStore_UpdatePod(t *testing.T) {
	reqPod := testing_helper.MakePod().Name("req-pod").
		UID("req-pod").
		Annotation(podutil.PodResourceReservationAnnotationForGodel, "true").
		Annotation(podutil.ReservationIndexAnnotation, "ph").
		Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
		Node("node").Obj()
	placeholderPod := podutil.CreateReservationFakePod(reqPod)

	backPod := testing_helper.MakePod().Name("back-pod").
		UID("back-pod").
		Annotation(podutil.ReservationIndexAnnotation, "ph").
		Annotation(podutil.MatchedReservationPlaceholderKey, getPlaceholderPodKey(reqPod)).
		Annotation(podutil.AssumedNodeAnnotationKey, "node").
		Obj()

	// case 1: pod (assumed -> bound) will consume reservation successfully
	{
		fakeHandler = makeCacheHandler()
		cache := NewCache(fakeHandler).(*ReservationStore)

		// assume pod reservation
		assert.NilError(t, cache.assumePodReservation(placeholderPod), "failed to assume pod reservation")

		// Pod Update event
		assumedPod := backPod.DeepCopy()
		assumedPod.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodAssumed)

		boundPod := assumedPod.DeepCopy()
		boundPod.Spec.NodeName = "node"
		assert.NilError(t, cache.UpdatePod(assumedPod, boundPod), "failed to update pod")
		checkReservationIsConsumed(t, cache, placeholderPod)
	}

	// case 2: pod (dispatched -> assumed) will not consume reservation successfully
	{
		fakeHandler = makeCacheHandler()
		cache := NewCache(fakeHandler).(*ReservationStore)

		// assume pod reservation
		assert.NilError(t, cache.assumePodReservation(placeholderPod), "failed to assume pod reservation")

		// Pod Update event
		dispatched := backPod.DeepCopy()
		dispatched.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodDispatched)

		assumed := dispatched.DeepCopy()
		dispatched.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodAssumed)
		assert.NilError(t, cache.UpdatePod(dispatched, assumed), "failed to update pod")
		checkReservationIsReserved(t, cache, placeholderPod)
	}
}

func TestReservationStore_AssumePod(t *testing.T) {
	reqPod := testing_helper.MakePod().Name("req-pod").
		UID("req-pod").
		Annotation(podutil.PodResourceReservationAnnotationForGodel, "true").
		Annotation(podutil.ReservationIndexAnnotation, "ph").
		Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
		Node("node").Obj()
	placeholderPod := podutil.CreateReservationFakePod(reqPod)

	backPod := testing_helper.MakePod().Name("back-pod").
		UID("back-pod").
		Annotation(podutil.ReservationIndexAnnotation, "ph").
		Annotation(podutil.MatchedReservationPlaceholderKey, getPlaceholderPodKey(reqPod)).
		Annotation(podutil.AssumedNodeAnnotationKey, "node").
		Obj()

	// case 1: reservation back pod will consume placeholder pod successfully
	{
		fakeHandler = makeCacheHandler()
		cache := NewCache(fakeHandler).(*ReservationStore)

		reserveReservation(t, cache, placeholderPod)
		assert.NilError(t, cache.AssumePod(&framework.CachePodInfo{Pod: backPod}), "failed to assume pod")
		checkReservationIsConsumed(t, cache, placeholderPod)
	}
}

func TestReservationStore_ForgetPod(t *testing.T) {
	reqPod := testing_helper.MakePod().Name("req-pod").
		UID("req-pod").
		Annotation(podutil.PodResourceReservationAnnotationForGodel, "true").
		Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
		Annotation(podutil.ReservationIndexAnnotation, "ph").
		Node("node").Obj()
	placeholderPod := podutil.CreateReservationFakePod(reqPod)

	backPod := testing_helper.MakePod().Name("back-pod").
		UID("back-pod").
		Annotation(podutil.ReservationIndexAnnotation, "ph").
		Annotation(podutil.MatchedReservationPlaceholderKey, getPlaceholderPodKey(reqPod)).
		Annotation(podutil.AssumedNodeAnnotationKey, "node").
		Obj()

	// case 1: forget reservation back pod will reset placeholder pod successfully
	{
		fakeHandler = makeCacheHandler()
		cache := NewCache(fakeHandler).(*ReservationStore)

		reserveReservation(t, cache, placeholderPod)
		assert.NilError(t, cache.AssumePod(&framework.CachePodInfo{Pod: backPod}), "failed to assume pod")
		checkReservationIsConsumed(t, cache, placeholderPod)

		assert.NilError(t, cache.ForgetPod(&framework.CachePodInfo{Pod: backPod}), "failed to forget pod")
		checkReservationIsReserved(t, cache, placeholderPod)
	}
}

func TestReservationStore_ShouldReserveResources(t *testing.T) {
	tests := []struct {
		name       string
		pod        *v1.Pod
		deployment *appsv1.Deployment
		want       bool
	}{
		{
			name: "pod with reservation annotation key should reserve resources",
			pod: testing_helper.MakePod().Name("req-pod").
				UID("req-pod").
				Annotation(podutil.PodResourceReservationAnnotationForGodel, "true").
				Node("node").
				Obj(),
			deployment: nil,
			want:       true,
		},
		{
			name: "pod without reservation annotation key should not reserve resources",
			pod: testing_helper.MakePod().Name("req-pod").
				UID("req-pod").
				Node("node").
				Obj(),
			deployment: nil,
			want:       false,
		},
		{
			name: "deployment with reservation annotation should reserve resources",
			pod: testing_helper.MakePod().Name("req-pod").
				UID("req-pod").
				Label(util.DeployNameKeyInPodLabels, "req-deploy").
				Node("node").
				Obj(),
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "req-deploy",
					Labels: map[string]string{
						util.DeployNameKeyInPodLabels: "req-deploy",
					},

					Annotations: map[string]string{
						podutil.PodResourceReservationAnnotationForGodel: "true",
					},
				},
			},
			want: true,
		},
		{
			name: "deployment without reservation annotation should not reserve resources",
			pod: testing_helper.MakePod().Name("req-pod").
				UID("req-pod").
				Label(util.DeployNameKeyInPodLabels, "req-deploy").
				Node("node").
				Obj(),
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "req-deploy",
					Labels: map[string]string{
						util.DeployNameKeyInPodLabels: "req-deploy",
					},

					Annotations: map[string]string{},
				},
			},
			want: false,
		},
		{
			name: "non-reservation pod without deploy should not reserve resources",
			pod: testing_helper.MakePod().Name("req-pod").
				UID("req-pod").
				Node("node").
				Obj(),
			deployment: nil,
			want:       false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeHandler = makeCacheHandler()
			cache := NewCache(fakeHandler).(*ReservationStore)

			if test.deployment != nil {
				assert.NilError(t, cache.AddDeployment(test.deployment), "failed to add deployment into cache")
			}

			if got := cache.shouldReserveResources(test.pod); got != test.want {
				t.Fatalf("want %v, got %v", test.want, got)
			}
		})
	}
}

func TestReservationStore_CleanupExpiredAssumedPodReservation(t *testing.T) {
	tests := []struct {
		name           string
		pod            *v1.Pod
		addReservation bool
		wantErr        bool
	}{
		{
			name: "assumed reservation will be cleaned up without adding reservation",
			pod: testing_helper.MakePod().Name("req-pod").
				UID("req-pod").
				Annotation(podutil.PodResourceReservationAnnotationForGodel, "true").
				Annotation(podutil.ReservationIndexAnnotation, "ph").
				Node("node").
				Obj(),
			addReservation: false,
			wantErr:        true,
		},
		{
			name: "assumed reservation will not be cleaned up with adding reservation",
			pod: testing_helper.MakePod().Name("req-pod").
				UID("req-pod").
				Annotation(podutil.PodResourceReservationAnnotationForGodel, "true").
				Annotation(podutil.ReservationIndexAnnotation, "ph").
				Node("node").
				Obj(),
			addReservation: true,
			wantErr:        false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeHandler = makeCacheHandler()
			cache := NewCache(fakeHandler).(*ReservationStore)
			placeholderPod := podutil.CreateReservationFakePod(test.pod)

			assert.NilError(t, cache.assumePodReservation(placeholderPod), "failed to assume pod reservation")

			if test.addReservation {
				reservation, _ := podutil.ConstructReservationAccordingToPod(test.pod, int64(reservationTTL))
				assert.NilError(t, cache.AddReservation(reservation), "failed to add reservation")
			}

			assert.NilError(t, forceCleanupAssumedReservation(cache), "failed to cleanup assumed reservation")
			_, err := cache.getReservationInfoByPod(placeholderPod)
			if wantErr := test.wantErr; wantErr != (err != nil) {
				t.Errorf("wantErr %v, got %v", wantErr, err)
			}
		})
	}
}

func TestReservationStore_AddReservation(t *testing.T) {
	reqPod := testing_helper.MakePod().Name("req-pod").
		UID("req-pod").
		Annotation(podutil.PodResourceReservationAnnotationForGodel, "true").
		Annotation(podutil.ReservationIndexAnnotation, "ph").
		Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
		Node("node").Obj()
	placeholderPod := podutil.CreateReservationFakePod(reqPod)
	reservation, _ := podutil.ConstructReservationAccordingToPod(reqPod, int64(reservationTTL))

	// case 1: add reservation after cleaning up expired assumed reservation
	{
		fakeHandler = makeCacheHandler()
		cache := NewCache(fakeHandler).(*ReservationStore)

		assert.NilError(t, cache.assumePodReservation(placeholderPod), "failed to assume pod reservation")
		assert.NilError(t, forceCleanupAssumedReservation(cache), "failed to cleanup assumed reservation")

		assert.NilError(t, cache.AddReservation(reservation), "failed to add reservation")
		checkReservationIsReserved(t, cache, placeholderPod)
	}

	// case 2: add reservation before cleaning up expired assumed reservation
	{
		fakeHandler = makeCacheHandler()
		cache := NewCache(fakeHandler).(*ReservationStore)

		assert.NilError(t, cache.assumePodReservation(placeholderPod), "failed to assume pod reservation")
		assert.NilError(t, cache.AddReservation(reservation), "failed to add reservation")

		assert.NilError(t, forceCleanupAssumedReservation(cache), "failed to cleanup assumed reservation")
		checkReservationIsReserved(t, cache, placeholderPod)
	}

	// case 3: matched reservation should not reserve resources
	{
		fakeHandler = makeCacheHandler()
		cache := NewCache(fakeHandler).(*ReservationStore)
		res := reservation.DeepCopy()
		res.Status.Phase = schedulingv1alpha1.ReservationMatched

		assert.NilError(t, cache.AddReservation(res), "failed to add reservation")
		checkReservationIsReleased(t, cache, placeholderPod)
	}

	// case 3: timeout reservation should not reserve resources
	{
		fakeHandler = makeCacheHandler()
		cache := NewCache(fakeHandler).(*ReservationStore)
		res := reservation.DeepCopy()
		res.Status.Phase = schedulingv1alpha1.ReservationTimeOut

		assert.NilError(t, cache.AddReservation(res), "failed to add reservation")
		checkReservationIsReleased(t, cache, placeholderPod)
	}
}

func TestReservationStore_DeleteReservation(t *testing.T) {
	reqPod := testing_helper.MakePod().Name("req-pod").
		UID("req-pod").
		Annotation(podutil.PodResourceReservationAnnotationForGodel, "true").
		Annotation(podutil.ReservationIndexAnnotation, "ph").
		Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
		Node("node").Obj()
	placeholderPod := podutil.CreateReservationFakePod(reqPod)
	reservation, _ := podutil.ConstructReservationAccordingToPod(reqPod, int64(reservationTTL))

	// case 1: delete reservation will release reserved resources
	{
		fakeHandler = makeCacheHandler()
		cache := NewCache(fakeHandler).(*ReservationStore)

		assert.NilError(t, cache.AddReservation(reservation), "failed to add reservation")
		checkReservationIsReserved(t, cache, placeholderPod)

		assert.NilError(t, cache.DeleteReservation(reservation), "failed to delete reservation")
		checkReservationIsReleased(t, cache, placeholderPod)
	}
}

func TestReservationStore_UpdateReservation(t *testing.T) {
	reqPod := testing_helper.MakePod().Name("req-pod").
		UID("req-pod").
		Annotation(podutil.PodResourceReservationAnnotationForGodel, "true").
		Annotation(podutil.ReservationIndexAnnotation, "ph").
		Annotation(podutil.PodStateAnnotationKey, string(podutil.PodAssumed)).
		Node("node").Obj()
	backPod := testing_helper.MakePod().Name("back-pod").
		UID("back-pod").
		Annotation(podutil.ReservationIndexAnnotation, "ph").
		Annotation(podutil.MatchedReservationPlaceholderKey, getPlaceholderPodKey(reqPod)).
		Node("node").
		Obj()
	placeholderPod := podutil.CreateReservationFakePod(reqPod)
	reservation, _ := podutil.ConstructReservationAccordingToPod(reqPod, int64(reservationTTL))

	// case 1: update reservation (pending -> matched) will consume reserved resources
	{
		fakeHandler = makeCacheHandler()
		cache := NewCache(fakeHandler).(*ReservationStore)

		assert.NilError(t, cache.AddReservation(reservation), "failed to add reservation")
		checkReservationIsReserved(t, cache, placeholderPod)

		matchedRes := reservation.DeepCopy()
		matchedRes.Status.Phase = schedulingv1alpha1.ReservationMatched
		matchedRes.Status.CurrentOwners = schedulingv1alpha1.CurrentOwners{
			Name: "back-pod",
			UID:  "back-pod",
		}

		assert.NilError(t, addPodStore(backPod), "failed to add pod")
		assert.NilError(t, cache.UpdateReservation(reservation, matchedRes), "failed to update reservation")
		checkReservationIsConsumed(t, cache, placeholderPod)

		assert.NilError(t, cache.UpdateReservation(matchedRes, reservation), "failed to update reservation")
		checkReservationIsReserved(t, cache, placeholderPod)
	}
}
