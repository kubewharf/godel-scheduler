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
	"math"
	"time"

	"github.com/onsi/ginkgo"
	_ "github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"

	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/test/e2e/framework"
	e2enode "github.com/kubewharf/godel-scheduler/test/e2e/framework/node"
	e2epod "github.com/kubewharf/godel-scheduler/test/e2e/framework/pod"
	e2eskipper "github.com/kubewharf/godel-scheduler/test/e2e/framework/skipper"
)

var _ = SIGDescribe("Scheduling-Unit", func() {
	var cs clientset.Interface
	var nodeList *v1.NodeList
	var ns string
	f := framework.NewDefaultFramework("scheduling-unit")

	lowPriority, mediumPriority, highPriority := int32(1), int32(100), int32(1000)
	lowPriorityClassName := f.BaseName + "-low-priority"
	mediumPriorityClassName := f.BaseName + "-medium-priority"
	highPriorityClassName := f.BaseName + "-high-priority"
	priorityPairs := []priorityPair{
		{name: lowPriorityClassName, value: lowPriority},
		{name: mediumPriorityClassName, value: mediumPriority},
		{name: highPriorityClassName, value: highPriority},
	}

	ginkgo.BeforeEach(func() {
		cs = f.ClientSet
		ns = f.Namespace.Name
		framework.Logf("namespace: %v", ns)
		nodeList = &v1.NodeList{}
		var err error
		for _, pair := range priorityPairs {
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
		for _, pair := range priorityPairs {
			cs.SchedulingV1().PriorityClasses().Delete(context.TODO(), pair.name, *metav1.NewDeleteOptions(0))
		}
		for _, node := range nodeList.Items {
			nodeCopy := node.DeepCopy()
			delete(nodeCopy.Status.Capacity, testExtendedResource)
			err := patchNode(cs, &node, nodeCopy)
			framework.ExpectNoError(err)
		}
	})

	framework.ConformanceIt("Create pods less than min members followed by pods to reach min member count", func() {
		minMember := int32(math.Max(2, float64(len(nodeList.Items))+1))

		ginkgo.By("create pods less than min member count")
		pods := make([]*v1.Pod, 0, len(nodeList.Items))
		podRes := v1.ResourceList{}
		podRes[testExtendedResource] = resource.MustParse("1")
		for i, node := range nodeList.Items {
			// 1. Set the node's capacity
			{
				nodeCopy := node.DeepCopy()
				nodeCopy.Status.Capacity[testExtendedResource] = resource.MustParse("4")
				framework.ExpectNoError(patchNode(cs, &node, nodeCopy))
			}

			// 2. Create pause pod
			pods = append(pods, createPausePod(f, pausePodConfig{
				Name:      fmt.Sprintf("pod%d", i),
				Namespace: ns,
				Resources: &v1.ResourceRequirements{
					Requests: podRes,
					Limits:   podRes,
				},
				Annotations: WithPodGroup(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), "pg"),
			}))
			framework.Logf("created pod: %v", pods[i].Name)
		}

		pg := createPodGroup(f, "pg", ns, int32(minMember))
		framework.Logf("created pod group: %v", pg.Name)

		ginkgo.By("less than min member pods should be in pending state")
		time.Sleep(time.Duration(minMember) * time.Second)
		for _, pod := range pods {
			framework.ExpectNoError(e2epod.WaitForPodCondition(cs, pod.Namespace, pod.Name, "", 60*time.Second,
				func(pod *v1.Pod) (bool, error) {
					return pod.Status.Phase == v1.PodPending, nil
				}))
		}

		ginkgo.By("add pods to reach min member count")
		pod_extra := createPausePod(f, pausePodConfig{
			Name:      "podextra",
			Namespace: ns,
			Resources: &v1.ResourceRequirements{
				Requests: podRes,
				Limits:   podRes,
			},
			Annotations: WithPodGroup(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), "pg"),
		})
		framework.Logf("created pod: %v", "podextra")
		time.Sleep(time.Duration(minMember) * time.Second)
		framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod_extra))
		ginkgo.By("all pods are running after reaching min member count")
	})

	framework.ConformanceIt("Create pods satisfying min members, but podgroup created later", func() {
		minMember := int32(math.Max(2, float64(len(nodeList.Items))))

		ginkgo.By("create pods greater than satisfying min member.")
		pods := make([]*v1.Pod, 0, len(nodeList.Items))
		podRes := v1.ResourceList{}
		podRes[testExtendedResource] = resource.MustParse("1")
		for i, node := range nodeList.Items {
			// 1. Set the node's capacity
			{
				nodeCopy := node.DeepCopy()
				nodeCopy.Status.Capacity[testExtendedResource] = resource.MustParse("4")
				framework.ExpectNoError(patchNode(cs, &node, nodeCopy))
			}

			// 2. Create pause pod
			pods = append(pods, createPausePod(f, pausePodConfig{
				Name:      fmt.Sprintf("pod-%d", i),
				Namespace: ns,
				Resources: &v1.ResourceRequirements{
					Requests: podRes,
					Limits:   podRes,
				},
				Labels:      WithPodGroup(map[string]string{}, "pg"),
				Annotations: WithPodGroup(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), "pg"),
			}))
			framework.Logf("created pod: %v", pods[i].Name)
		}

		ginkgo.By("without podgroup, min member pods should be in pending state")
		time.Sleep(time.Duration(minMember) * time.Second)
		for _, pod := range pods {
			framework.ExpectNoError(e2epod.WaitForPodCondition(cs, pod.Namespace, pod.Name, "", 60*time.Second,
				func(pod *v1.Pod) (bool, error) {
					return pod.Status.Phase == v1.PodPending, nil
				}))
		}

		pg := createPodGroup(f, "pg", ns, minMember)
		framework.Logf("created pod group: %v", pg.Name)

		ginkgo.By("wait for min member pods to be scheduled.")
		time.Sleep(time.Duration(minMember) * time.Second)
		for _, pod := range pods {
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		}
		ginkgo.By("min members pods are scheduled.")
	})

	framework.ConformanceIt("Create pods satisfying min members, but podgroup timeout", func() {
		minMember := int32(math.Max(2, float64(len(nodeList.Items))))

		ginkgo.By("create pods greater than satisfying min member.")
		pods := make([]*v1.Pod, 0, len(nodeList.Items))
		podRes := v1.ResourceList{}
		podRes[testExtendedResource] = resource.MustParse("1")
		for i, node := range nodeList.Items {
			// 1. Set the node's capacity
			{
				nodeCopy := node.DeepCopy()
				nodeCopy.Status.Capacity[testExtendedResource] = resource.MustParse("4")
				framework.ExpectNoError(patchNode(cs, &node, nodeCopy))
			}

			// 2. Create pause pod
			pods = append(pods, createPausePod(f, pausePodConfig{
				Name:      fmt.Sprintf("pod-%d", i),
				Namespace: ns,
				Resources: &v1.ResourceRequirements{
					Requests: podRes,
					Limits:   podRes,
				},
				Labels:      WithPodGroup(map[string]string{}, "pg"),
				Annotations: WithPodGroup(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), "pg"),
			}))
			framework.Logf("created pod: %v", pods[i].Name)
		}

		ginkgo.By("without podgroup, min member pods should be in pending state")
		time.Sleep(time.Duration(minMember) * time.Second)
		for _, pod := range pods {
			framework.ExpectNoError(e2epod.WaitForPodCondition(cs, pod.Namespace, pod.Name, "", 60*time.Second,
				func(pod *v1.Pod) (bool, error) {
					return pod.Status.Phase == v1.PodPending, nil
				}))
		}

		pg := createPodGroupTimeout(f, "pg", ns, minMember, 0)
		framework.Logf("created timeout pod group: %v", pg.Name)

		ginkgo.By("min member pods should not be scheduled cause timeout podgroup.")
		time.Sleep(time.Duration(minMember) * time.Second)
		for _, pod := range pods {
			framework.ExpectNoError(e2epod.WaitForPodCondition(cs, pod.Namespace, pod.Name, "", 60*time.Second,
				func(pod *v1.Pod) (bool, error) {
					return pod.Status.Phase == v1.PodPending, nil
				}))
		}
		ginkgo.By("min members pods are not scheduled.")
	})

	framework.ConformanceIt("Create pods min members + extra", func() {
		minMember := int32(math.Max(2, float64(len(nodeList.Items))))

		ginkgo.By("create pods greater than equal to min member count.")
		pods := make([]*v1.Pod, 0, len(nodeList.Items))
		podRes := v1.ResourceList{}
		podRes[testExtendedResource] = resource.MustParse("1")
		for i, node := range nodeList.Items {
			// 1. Set the node's capacity
			{
				nodeCopy := node.DeepCopy()
				nodeCopy.Status.Capacity[testExtendedResource] = resource.MustParse("4")
				framework.ExpectNoError(patchNode(cs, &node, nodeCopy))
			}

			// 2. Create pause pod
			pods = append(pods, createPausePod(f, pausePodConfig{
				Name:      fmt.Sprintf("pod-%d", i),
				Namespace: ns,
				Resources: &v1.ResourceRequirements{
					Requests: podRes,
					Limits:   podRes,
				},
				Labels:      WithPodGroup(map[string]string{}, "pg"),
				Annotations: WithPodGroup(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), "pg"),
			}))
			framework.Logf("created pod: %v", pods[i].Name)
		}

		pg := createPodGroup(f, "pg", ns, minMember)
		framework.Logf("created pod group: %v", pg.Name)

		ginkgo.By("wait for min member pods to be scheduled.")
		time.Sleep(time.Duration(minMember) * time.Second)
		for _, pod := range pods {
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		}
		ginkgo.By("min members pods are scheduled.")

		ginkgo.By("as min member pods are running, add pods less than min members should run as well.")
		pod_extra := createPausePod(f, pausePodConfig{
			Name:      "podextra",
			Namespace: ns,
			Resources: &v1.ResourceRequirements{
				Requests: podRes,
				Limits:   podRes,
			},
			Labels:      WithPodGroup(map[string]string{}, "pg"),
			Annotations: WithPodGroup(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), "pg"),
		})
		framework.Logf("created pod: %v", "podextra")
		time.Sleep(time.Duration(minMember) * time.Second)
		framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod_extra))
	})

	framework.ConformanceIt("Min member pod count leads to preemption", func() {
		minMember := int32(math.Max(2, float64(len(nodeList.Items))+1))

		ginkgo.By("create pods less than min member count")
		pods := make([]*v1.Pod, 0, len(nodeList.Items))
		podgs := make([]*v1.Pod, 0, len(nodeList.Items))
		podRes := v1.ResourceList{}
		podRes[testExtendedResource] = resource.MustParse("2")
		for i, node := range nodeList.Items {
			// 1. Set the node's capacity
			{
				nodeCopy := node.DeepCopy()
				nodeCopy.Status.Capacity[testExtendedResource] = resource.MustParse("4")
				framework.ExpectNoError(patchNode(cs, &node, nodeCopy))
			}

			// 2. Create pause pod
			pods = append(pods, createPausePod(f, pausePodConfig{
				Name:              fmt.Sprintf("pod%d", i),
				Namespace:         ns,
				PriorityClassName: lowPriorityClassName,
				Resources: &v1.ResourceRequirements{
					Requests: podRes,
					Limits:   podRes,
				},
				Annotations: WithPreemptible(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), true),
			}))
			framework.Logf("created pod: %v", pods[i].Name)

			podgs = append(podgs, createPausePod(f, pausePodConfig{
				Name:              fmt.Sprintf("podg%d", i),
				Namespace:         ns,
				PriorityClassName: highPriorityClassName,
				Resources: &v1.ResourceRequirements{
					Requests: podRes,
					Limits:   podRes,
				},
				Labels:      WithPodGroup(map[string]string{}, "pg"),
				Annotations: WithPodGroup(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), "pg"),
			}))
			framework.Logf("created pod: %v", podgs[i].Name)
		}

		pg := createPodGroup(f, "pg", ns, int32(minMember))
		framework.Logf("created pod group: %v", pg.Name)

		ginkgo.By("less than min member pods should be in pending state")
		time.Sleep(time.Duration(minMember) * time.Second)
		for _, pod := range pods {
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		}
		for _, pod := range podgs {
			framework.ExpectNoError(e2epod.WaitForPodCondition(cs, pod.Namespace, pod.Name, "", 60*time.Second, func(pod *v1.Pod) (bool, error) {
				if pod.Status.Phase == v1.PodPending {
					return true, nil
				}
				return false, nil
			}))
		}

		ginkgo.By("add pods to reach min member count and should trigger preemption")
		podRes = v1.ResourceList{}
		podRes[testExtendedResource] = resource.MustParse("1")
		pod_extra := createPausePod(f, pausePodConfig{
			Name:              "podextra",
			Namespace:         ns,
			PriorityClassName: highPriorityClassName,
			Resources: &v1.ResourceRequirements{
				Requests: podRes,
				Limits:   podRes,
			},
			Labels:      WithPodGroup(map[string]string{}, "pg"),
			Annotations: WithPodGroup(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), "pg"),
		})
		framework.Logf("created pod: %v", "podextra")
		time.Sleep(time.Duration(minMember) * time.Second)
		for _, pod := range podgs {
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		}
		framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod_extra))
		ginkgo.By("all pods are running after reaching min member count and preemption")
	})

	framework.ConformanceIt("Multiple pod groups launched together but no deadlock issue", func() {
		e2eskipper.Skipf("TODO")
		var podRes v1.ResourceList
		minMember := int32(math.Max(2, float64(len(nodeList.Items)+1)))

		ginkgo.By("create two jobs with min member count but only one job will be scheduled")
		podGroupA := "joba"
		podGroupB := "jobb"
		jobA := make([]*v1.Pod, 0, len(nodeList.Items))
		jobB := make([]*v1.Pod, 0, len(nodeList.Items))
		for i, node := range nodeList.Items {
			nodeCopy := node.DeepCopy()
			nodeCopy.Status.Capacity[testExtendedResource] = resource.MustParse("4")
			err := patchNode(cs, &node, nodeCopy)
			framework.ExpectNoError(err)

			podRes = v1.ResourceList{}
			podRes[testExtendedResource] = resource.MustParse("2")
			jobA = append(jobA, createPausePod(f, pausePodConfig{
				Name:      fmt.Sprintf("joba-%d", i),
				Namespace: ns,
				Resources: &v1.ResourceRequirements{
					Requests: podRes,
					Limits:   podRes,
				},
				Annotations: WithPodGroup(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), podGroupA),
			}))
			framework.Logf("created pod: %v", jobA[i].Name)

			podRes = v1.ResourceList{}
			podRes[testExtendedResource] = resource.MustParse("2")
			jobB = append(jobB, createPausePod(f, pausePodConfig{
				Name:      fmt.Sprintf("jobb-%d", i),
				Namespace: ns,
				Resources: &v1.ResourceRequirements{
					Requests: podRes,
					Limits:   podRes,
				},
				Annotations: WithPodGroup(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), podGroupB),
			}))
			framework.Logf("created pod: %v", jobB[i].Name)
		}

		jobAPG := createPodGroup(f, podGroupA, ns, int32(minMember))
		framework.Logf("created pod group: %v", jobAPG.Name)

		jobBPG := createPodGroup(f, podGroupB, ns, int32(minMember))
		framework.Logf("created pod group: %v", jobBPG.Name)

		for _, pod := range jobA {
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		}
		for _, pod := range jobB {
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		}

		// podRes = v1.ResourceList{}
		// podRes[testExtendedResource] = resource.MustParse("1")
		// jobA = append(jobA, createPausePod(f, pausePodConfig{
		// 	Name:      "podextra-joba",
		// 	Namespace: ns,
		// 	Resources: &v1.ResourceRequirements{
		// 		Requests: podRes,
		// 		Limits:   podRes,
		// 	},
		// 	Annotations: WithPodGroup(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), podGroupA),
		// }))
		// framework.Logf("created pod: %v", "podextra-joba")

		// jobB = append(jobB, createPausePod(f, pausePodConfig{
		// 	Name:      "podextra-jobb",
		// 	Namespace: ns,
		// 	Resources: &v1.ResourceRequirements{
		// 		Requests: podRes,
		// 		Limits:   podRes,
		// 	},
		// 	Annotations: WithPodGroup(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), podGroupB),
		// }))
		// framework.Logf("created pod: %v", "podextra-jobb")

		// var isRunningA bool
		// var isRunningB bool
		// time.Sleep(time.Duration(minMember) * time.Second)
		// var count int
		// for {
		// 	isRunningA = e2epod.CheckPodsRunningReady(cs, jobA[0].Namespace, []string{jobA[0].Name}, 1*time.Second)
		// 	if isRunningA {
		// 		for _, pod := range jobA {
		// 			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		// 			e2epod.DeletePodWithWait(cs, pod)
		// 		}
		// 		for _, pod := range jobB {
		// 			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		// 		}
		// 		break
		// 	}

		// 	isRunningB = e2epod.CheckPodsRunningReady(cs, jobB[0].Namespace, []string{jobB[0].Name}, 1*time.Second)
		// 	if isRunningB {
		// 		for _, pod := range jobB {
		// 			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		// 			e2epod.DeletePodWithWait(cs, pod)
		// 		}
		// 		for _, pod := range jobA {
		// 			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		// 		}
		// 		break
		// 	}
		// 	if count == 10 {
		// 		framework.ExpectError(fmt.Errorf("both pod groups did not run"))
		// 		break
		// 	}
		// 	time.Sleep(5 * time.Second)
		// 	count++
		// }
	})
})
