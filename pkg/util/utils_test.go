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

package util

import (
	"fmt"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	crdclientset "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	godelclientfake "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/fake"
	crdclientscheme "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/scheme"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	restclient "k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"
	utiltesting "k8s.io/client-go/util/testing"
)

func TestGetPodFullName(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test",
			Name:      "pod",
		},
	}
	got := GetPodFullName(pod)
	expected := fmt.Sprintf("%s_%s", pod.Name, pod.Namespace)
	if got != expected {
		t.Errorf("Got wrong full name, got: %s, expected: %s", got, expected)
	}
}

func TestRemoveNominatedNodeName(t *testing.T) {
	tests := []struct {
		name                     string
		currentNominatedNodeName string
		newNominatedNodeName     string
		expectedPatchRequests    int
		expectedPatchData        string
	}{
		{
			name:                     "Should make patch request to clear node name",
			currentNominatedNodeName: "node1",
			expectedPatchRequests:    1,
			expectedPatchData:        `{"status":{"nominatedNodeName":null}}`,
		},
		{
			name:                     "Should not make patch request if nominated node is already cleared",
			currentNominatedNodeName: "",
			expectedPatchRequests:    0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actualPatchRequests := 0
			var actualPatchData string
			cs := &clientsetfake.Clientset{}
			cs.AddReactor("patch", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
				actualPatchRequests++
				patch := action.(clienttesting.PatchAction)
				actualPatchData = string(patch.GetPatch())
				// For this test, we don't care about the result of the patched pod, just that we got the expected
				// patch request, so just returning &v1.Pod{} here is OK because scheduler doesn't use the response.
				return true, &v1.Pod{}, nil
			})

			pod := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "foo"},
				Status:     v1.PodStatus{NominatedNodeName: test.currentNominatedNodeName},
			}

			if err := ClearNominatedNodeName(cs, pod); err != nil {
				t.Fatalf("Error calling removeNominatedNodeName: %v", err)
			}

			if actualPatchRequests != test.expectedPatchRequests {
				t.Fatalf("Actual patch requests (%d) dos not equal expected patch requests (%d)", actualPatchRequests, test.expectedPatchRequests)
			}

			if test.expectedPatchRequests > 0 && actualPatchData != test.expectedPatchData {
				t.Fatalf("Patch data mismatch: Actual was %v, but expected %v", actualPatchData, test.expectedPatchData)
			}
		})
	}
}

func TestPostScheduler(t *testing.T) {
	testSchedulerName := "test-scheduler"

	fakeScheduler := &v1alpha1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{
			Name: testSchedulerName,
		},
	}

	body := runtime.EncodeOrDie(crdclientscheme.Codecs.LegacyCodec(v1alpha1.SchemeGroupVersion), fakeScheduler)
	fakeHandler := utiltesting.FakeHandler{
		StatusCode:   200,
		ResponseBody: string(body),
	}
	testServer := httptest.NewServer(&fakeHandler)
	defer testServer.Close()

	crdclientset := crdclientset.NewForConfigOrDie(&restclient.Config{Host: testServer.URL, ContentConfig: restclient.ContentConfig{GroupVersion: &v1alpha1.SchemeGroupVersion}})

	createdScheduler, err := PostScheduler(crdclientset, fakeScheduler)
	assert.NoError(t, err, "unexpected error: %v", err)

	assertEqualValues(t, fakeScheduler.Name, createdScheduler.Name)
}

func TestUpdateScheduler(t *testing.T) {
	testSchedulerName := "test-scheduler"

	var now metav1.Time
	now = metav1.Now()
	metaScheduler := &v1alpha1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{
			Name: testSchedulerName,
		},
		Status: v1alpha1.SchedulerStatus{
			LastUpdateTime: &now,
		},
	}

	fakeScheduler := metaScheduler.DeepCopy()
	crdclientset := godelclientfake.NewSimpleClientset(fakeScheduler)

	time.Sleep(3 * time.Second)
	updated := metav1.Now()
	expectScheduler := fakeScheduler.DeepCopy()
	expectScheduler.Status.LastUpdateTime = &updated

	updateScheduler, err := UpdateSchedulerStatus(crdclientset, expectScheduler)
	assert.NoError(t, err, "unexpected error: %v", err)

	assertEqualValues(t, expectScheduler.Name, updateScheduler.Name)
	assertNotEqualValues(t, metaScheduler.Status.LastUpdateTime, updateScheduler.Status.LastUpdateTime)
}

func assertEqualValues(t *testing.T, expected, actual interface{}) {
	assert.EqualValues(t, expected, actual, "expected %v, got %v", expected, actual)
}

func assertNotEqualValues(t *testing.T, expected, actual interface{}) {
	assert.NotEqual(t, expected, actual, "expected %v, got %v", expected, actual)
}

func TestUnmarshalMicroTopology(t *testing.T) {
	tests := []struct {
		name             string
		microTopologyStr string
		expectedResult   map[int]*v1.ResourceList
		expectedError    error
	}{
		{
			name:             "arr length is not 2",
			microTopologyStr: "0:cpu=20,memory=100Gi:1",
			expectedResult:   nil,
			expectedError:    fmt.Errorf("failed to parse micro topology in annotation: 0:cpu=20,memory=100Gi:1"),
		},
		{
			name:             "failed to parse numa id",
			microTopologyStr: "0a:cpu=20,memory=100Gi",
			expectedResult:   nil,
			expectedError:    fmt.Errorf("failed to parse numa id in annotation '0a': strconv.ParseInt: parsing \"0a\": invalid syntax"),
		},
		{
			name:             "failed to parse resource requests",
			microTopologyStr: "0:cpu==20,memory=100Gi",
			expectedResult:   nil,
			expectedError:    fmt.Errorf("failed to parse resource requests cpu==20"),
		},
		{
			name:             "failed to parse resource quantity",
			microTopologyStr: "0:cpu=20a,memory=100Gi",
			expectedResult:   nil,
			expectedError:    fmt.Errorf("failed to parse resource quantity 20a: quantities must match the regular expression '^([+-]?[0-9.]+)([eEinumkKMGTP]*[-+]?[0-9]*)$'"),
		},
		{
			name:             "pass",
			microTopologyStr: "0:cpu=20,memory=100Gi;1:cpu=18,memory=80Gi",
			expectedResult: map[int]*v1.ResourceList{
				0: {
					v1.ResourceCPU:    resource.MustParse("20"),
					v1.ResourceMemory: resource.MustParse("100Gi"),
				},
				1: {
					v1.ResourceCPU:    resource.MustParse("18"),
					v1.ResourceMemory: resource.MustParse("80Gi"),
				},
			},
			expectedError: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotResult, gotErr := UnmarshalMicroTopology(test.microTopologyStr)
			if !reflect.DeepEqual(gotErr, test.expectedError) {
				t.Errorf("expected got error: %v, but actually got: %v", test.expectedError, gotErr)
			}
			if !reflect.DeepEqual(gotResult, test.expectedResult) {
				t.Errorf("expected got result: %v, but actually got: %v", test.expectedResult, gotResult)
			}
		})
	}
}

func TestMemoryEnhancement(t *testing.T) {
	str := ""
	numaBinding, numaExclusive := memoryEnhancement(str)
	if numaBinding {
		t.Errorf("no need to bind numa")
	}
	if numaExclusive {
		t.Errorf("no need to monopolize numa")
	}

	str = "{\"numa_binding\":\"true\",\"numa_exclusive\":\"true\"}"
	numaBinding, numaExclusive = memoryEnhancement(str)
	if !numaBinding {
		t.Errorf("need to bind numa")
	}
	if !numaExclusive {
		t.Errorf("need to monopolize numa")
	}
}

func TestEqualMap(t *testing.T) {
	m1 := map[string]string{
		"a": "a",
		"b": "b",
	}

	m2 := map[string]string{
		"a": "a",
		"b": "b",
	}

	m3 := map[string]string{
		"a": "a",
		"c": "c",
	}

	if !EqualMap(m1, m2) {
		t.Errorf("m1 is equal to m2")
	}

	if EqualMap(m2, m3) {
		t.Errorf("m2 is not equal to m3")
	}
}

func TestParallelizeUntil(t *testing.T) {
	worker := 10
	for length := 10000; length < 10010; length++ {
		t.Run(fmt.Sprintf("for length %d", length), func(t *testing.T) {
			s := make([]int, length)
			doWorkPiece := func(i int) {
				s[i] = i
			}

			stop := false
			ParallelizeUntil(&stop, worker, length, doWorkPiece)

			for i := 0; i < length; i++ {
				if s[i] != i {
					t.Errorf("error do work piece")
					return
				}
			}
		})
	}
}
