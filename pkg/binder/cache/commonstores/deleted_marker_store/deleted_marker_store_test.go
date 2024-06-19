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

package deletedmarkerstore

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
)

func TestCleanupPodDeletionMarker(t *testing.T) {
	ttl := 10 * time.Second
	cleanupTime := time.Now().Add(700 * time.Second)
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "testPod",
			Namespace:       "testNS",
			UID:             "testUID",
			ResourceVersion: "1",
		},
		Spec: v1.PodSpec{
			NodeName: "node-1",
		},
	}
	podKey, _ := framework.GetPodKey(pod)

	preemptor := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "testPreemptor",
			Namespace:       "testNS",
			UID:             "testUID2",
			ResourceVersion: "1",
		},
	}
	cacheHandler := commoncache.MakeCacheHandlerWrapper().
		Period(10 * time.Second).PodAssumedTTL(ttl).StopCh(nil).
		ComponentName("godel-binder").Obj()

	cacheHandler.SetPodHandler(func(s string) (*framework.CachePodState, bool) {
		if s == podKey {
			dl := time.Now().Add(ttl)
			return &framework.CachePodState{
				Pod:      pod,
				Deadline: &dl,
			}, false
		}
		return nil, false
	})

	cache := NewCacheDeletedMarkerStore(cacheHandler).(*DeletedMarkerStore)

	cache.AddPod(pod)
	{
		p, _ := cache.Store[podKey]
		assert.NotEqual(t, nil, p)
	}
	{
		cache.MarkPodToDelete(pod, preemptor)
		markedToBeDeleted, _ := cache.IsPodMarkedToDelete(pod)
		assert.Equal(t, true, markedToBeDeleted)
	}
	{
		cache.CleanupPodDeletionMarker(&sync.RWMutex{}, cleanupTime)
		markedToBeDeleted, _ := cache.IsPodMarkedToDelete(pod)
		assert.Equal(t, false, markedToBeDeleted)
	}
}
