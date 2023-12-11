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

package unit

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	testinghelper "github.com/kubewharf/godel-scheduler/pkg/testing-helper"
	podAnnotations "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func TestPodGroupName(t *testing.T) {
	id := "uid"
	name := "pg"
	namespace := "default"

	// Empty annotation key
	pod := testinghelper.MakePod().Name(name).UID(id).Namespace(namespace).Obj()
	assert.Equal(t, "", GetPodGroupName(pod))

	// Normal case
	pod = testinghelper.MakePod().Name(name).UID(id).Namespace(namespace).Obj()
	annotations := map[string]string{
		podAnnotations.PodGroupNameAnnotationKey: name,
	}
	pod.SetAnnotations(annotations)
	assert.Equal(t, name, GetPodGroupName(pod))
}

func TestPodGroupFullName(t *testing.T) {
	id := "uid"
	name := "pg"
	namespace := "default"

	fullName := fmt.Sprintf("%v/%v", namespace, name)

	// Empty annotation key
	pod := testinghelper.MakePod().Name(name).UID(id).Namespace(namespace).Obj()
	assert.Equal(t, "", GetPodGroupFullName(pod))

	// Normal case
	pod = testinghelper.MakePod().Name(name).UID(id).Namespace(namespace).Obj()
	annotations := map[string]string{
		podAnnotations.PodGroupNameAnnotationKey: name,
	}
	pod.SetAnnotations(annotations)
	assert.Equal(t, fullName, GetPodGroupFullName(pod))
}
