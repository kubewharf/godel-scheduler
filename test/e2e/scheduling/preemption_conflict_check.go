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
	"context"
	"fmt"
	"time"

	"github.com/onsi/ginkgo"
	_ "github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/test/e2e/framework"
	e2enode "github.com/kubewharf/godel-scheduler/test/e2e/framework/node"
	e2epod "github.com/kubewharf/godel-scheduler/test/e2e/framework/pod"
)

var _ = SIGDescribe("PreemptionConflictCheck", func() {
	var cs clientset.Interface
	var nodeList *v1.NodeList
	var ns string
	f := framework.NewDefaultFramework("preemption")

	lowPriority, highPriority := int32(1), int32(1000)
	lowPriorityClassName := f.BaseName + "-low-priority"
	highPriorityClassName := f.BaseName + "-high-priority"
	priorities := []priorityPair{
		{name: lowPriorityClassName, value: lowPriority},
		{name: highPriorityClassName, value: highPriority},
	}

	ginkgo.BeforeEach(func() {
		cs = f.ClientSet
		ns = f.Namespace.Name
		nodeList = &v1.NodeList{}
		var err error

		for _, pair := range priorities {
			_, err := f.ClientSet.SchedulingV1().PriorityClasses().Create(context.TODO(), &schedulingv1.PriorityClass{ObjectMeta: metav1.ObjectMeta{Name: pair.name}, Value: pair.value}, metav1.CreateOptions{})
			framework.ExpectEqual(err == nil || apierrors.IsAlreadyExists(err), true)
		}

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
		for _, pair := range priorities {
			cs.SchedulingV1().PriorityClasses().Delete(context.TODO(), pair.name, *metav1.NewDeleteOptions(0))
		}
		for _, node := range nodeList.Items {
			nodeCopy := node.DeepCopy()
			delete(nodeCopy.Status.Capacity, testExtendedResource)
			err := patchNode(cs, &node, nodeCopy)
			framework.ExpectNoError(err)
		}
	})

	framework.ConformanceIt("Preempt victim pod at binder", func() {
		node := nodeList.Items[0]
		nodeCopy := node.DeepCopy()
		nodeCopy.Status.Capacity[testExtendedResource] = resource.MustParse("3")
		err := patchNode(cs, &node, nodeCopy)
		framework.ExpectNoError(err)

		ginkgo.By("Create victim pod")
		podRes := v1.ResourceList{}
		podRes[testExtendedResource] = resource.MustParse("2")
		pod := createPausePod(f, pausePodConfig{
			Name:      fmt.Sprintf("victim-pod"),
			Namespace: ns,
			Resources: &v1.ResourceRequirements{
				Requests: podRes,
				Limits:   podRes,
			},
			Annotations:       WithPreemptible(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), true),
			PriorityClassName: lowPriorityClassName,
		})
		framework.Logf("Created pod: %v", pod.Name)
		ginkgo.By("Wait for pod to be scheduled.")
		framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		ginkgo.By("Pod scheduled.")

		ginkgo.By("Create pod in assumed state with victim pod")
		nominatedNode := api.NominatedNode{
			NodeName: node.Name,
			VictimPods: api.VictimPods{
				api.VictimPod{
					Name:      pod.Name,
					Namespace: pod.Namespace,
					UID:       string(pod.UID),
				},
			},
		}
		podRes = v1.ResourceList{}
		podRes[testExtendedResource] = resource.MustParse("2")
		pod = createPausePod(f, pausePodConfig{
			Name:      fmt.Sprintf("preemption-pod"),
			Namespace: ns,
			Resources: &v1.ResourceRequirements{
				Requests: podRes,
				Limits:   podRes,
			},
			Annotations:       WithVictimPods(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), nominatedNode),
			PriorityClassName: highPriorityClassName,
		})
		framework.Logf("Created pod: %v", pod.Name)
		ginkgo.By("Wait for pod to be scheduled.")
		framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		ginkgo.By("Pod scheduled.")
	})

	framework.ConformanceIt("Victim pod is not present at binder preemption", func() {
		node := nodeList.Items[0]
		nominatedNode := api.NominatedNode{
			NodeName: node.Name,
			VictimPods: api.VictimPods{
				api.VictimPod{
					Name:      "test-pod",
					Namespace: "test-namespace",
					UID:       "test-uid",
				},
			},
		}

		nodeCopy := node.DeepCopy()
		nodeCopy.Status.Capacity[testExtendedResource] = resource.MustParse("3")
		err := patchNode(cs, &node, nodeCopy)
		framework.ExpectNoError(err)

		ginkgo.By("Create pod in assumed state with missing victim pod")
		podRes := v1.ResourceList{}
		podRes[testExtendedResource] = resource.MustParse("2")
		pod := createPausePod(f, pausePodConfig{
			Name:      fmt.Sprintf("preemption-pod"),
			Namespace: ns,
			Resources: &v1.ResourceRequirements{
				Requests: podRes,
				Limits:   podRes,
			},
			Annotations: WithVictimPods(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), nominatedNode),
		})
		framework.Logf("Created pod: %v", pod.Name)

		ginkgo.By("Wait for pods to be scheduled.")
		time.Sleep(10 * time.Second)
		framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		ginkgo.By("pod scheduled.")
	})
})
