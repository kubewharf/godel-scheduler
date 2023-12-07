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

package scheduling

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo"
	_ "github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	clientset "k8s.io/client-go/kubernetes"

	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/test/e2e/framework"
	e2enode "github.com/kubewharf/godel-scheduler/test/e2e/framework/node"
	e2epod "github.com/kubewharf/godel-scheduler/test/e2e/framework/pod"
)

var _ = SIGDescribe("Coscheduling", func() {
	var cs clientset.Interface
	var nodeList *v1.NodeList
	var ns string
	f := framework.NewDefaultFramework("coscheduling")

	ginkgo.BeforeEach(func() {
		cs = f.ClientSet
		ns = f.Namespace.Name
		nodeList = &v1.NodeList{}
		var err error

		e2enode.WaitForTotalHealthy(cs, time.Minute)
		nodeList, err = e2enode.GetReadySchedulableNodes(cs)
		if err != nil {
			framework.Logf("Unexpected error occurred: %v", err)
		}
		framework.ExpectNoErrorWithOffset(0, err)
		for _, n := range nodeList.Items {
			workerNodes.Insert(n.Name)
		}

		err = framework.CheckTestingNSDeletedExcept(cs, ns)
		framework.ExpectNoError(err)
	})

	ginkgo.AfterEach(func() {
		for _, node := range nodeList.Items {
			nodeCopy := node.DeepCopy()
			delete(nodeCopy.Status.Capacity, testExtendedResource)
			err := patchNode(cs, &node, nodeCopy)
			framework.ExpectNoError(err)
		}
	})

	framework.ConformanceIt("Coscheduling plugin successful test", func() {
		var podRes v1.ResourceList
		minMember := len(nodeList.Items)

		ginkgo.By("Create pods that use 2/3 of node resources.")
		pods := make([]*v1.Pod, 0, len(nodeList.Items))
		for i, node := range nodeList.Items {
			nodeCopy := node.DeepCopy()
			nodeCopy.Status.Capacity[testExtendedResource] = resource.MustParse("3")
			err := patchNode(cs, &node, nodeCopy)
			framework.ExpectNoError(err)

			podRes = v1.ResourceList{}
			podRes[testExtendedResource] = resource.MustParse("2")

			pods = append(pods, createPausePod(f, pausePodConfig{
				Name:      fmt.Sprintf("pod%d", i),
				Namespace: ns,
				Resources: &v1.ResourceRequirements{
					Requests: podRes,
					Limits:   podRes,
				},
				Labels:      WithPodGroup(map[string]string{}, "pg"),
				Annotations: WithPodGroup(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), "pg"),
			}))
			framework.Logf("Created pod: %v", pods[i].Name)
		}

		// Let's make sure we pass in a valid minMember
		min := int32(minMember / 2)
		if min == 0 {
			min = 1
		}
		pg := createPodGroup(f, "pg", ns, min)
		framework.Logf("Created pod group: %v", pg.Name)

		ginkgo.By("Wait for pods to be scheduled.")
		time.Sleep(time.Duration(minMember) * time.Second)
		for _, pod := range pods {
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		}
	})

	framework.ConformanceIt("Coscheduling plugin failure test", func() {
		var podRes v1.ResourceList
		minMember := len(nodeList.Items) * 2

		ginkgo.By("Create pods that use 2/3 of node resources.")
		pods := make([]*v1.Pod, 0, len(nodeList.Items))
		for i, node := range nodeList.Items {
			nodeCopy := node.DeepCopy()
			nodeCopy.Status.Capacity[testExtendedResource] = resource.MustParse("3")
			err := patchNode(cs, &node, nodeCopy)
			framework.ExpectNoError(err)

			podRes = v1.ResourceList{}
			podRes[testExtendedResource] = resource.MustParse("2")

			pods = append(pods, createPausePod(f, pausePodConfig{
				Name:      fmt.Sprintf("pod%d", i),
				Namespace: ns,
				Resources: &v1.ResourceRequirements{
					Requests: podRes,
					Limits:   podRes,
				},
				Labels:      WithPodGroup(map[string]string{}, "pg"),
				Annotations: WithPodGroup(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), "pg"),
			}))
			framework.Logf("Created pod: %v", pods[i].Name)
		}

		pg := createPodGroup(f, "pg", ns, int32(minMember))
		framework.Logf("Created pod group: %v", pg.Name)

		ginkgo.By("Wait for pods to be scheduled.")
		time.Sleep(time.Duration(minMember) / 2 * time.Second)
		for _, pod := range pods {
			framework.ExpectNoError(e2epod.WaitForPodCondition(cs, pod.Namespace, pod.Name, "", 60*time.Second, func(pod *v1.Pod) (bool, error) {
				if pod.Status.Phase == v1.PodPending {
					return true, nil
				}
				return false, nil
			}))
		}
	})
})
