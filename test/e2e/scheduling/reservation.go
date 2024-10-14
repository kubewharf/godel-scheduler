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

package scheduling

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/test/e2e/framework"
	e2enode "github.com/kubewharf/godel-scheduler/test/e2e/framework/node"
	imageutils "github.com/kubewharf/godel-scheduler/test/utils/image"
	"github.com/onsi/ginkgo"

	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
)

const (
	lowPC  = "system-cluster-critical"
	highPC = "system-node-critical"

	deleteDuration = 120 * time.Second
	createDuration = 30 * time.Second
	reservationTTL = 60 * time.Second

	scheduleTTL = 60 * time.Second
)

var (
	cs                       kubernetes.Interface
	gs                       godelclient.Interface
	nodeList                 *corev1.NodeList
	ns                       string
	reservedResource         corev1.ResourceName
	reservedResourceQuantity = "5"
)

const letterBytes = "abcdefghijklmnopqrstuvwxyz"

func init() {
	rand.Seed(time.Now().UnixNano())
}

func randomResource(n int) corev1.ResourceName {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return corev1.ResourceName("scheduling.k8s.io/" + string(b))
}

// makeDeployment function
func makeDeployment(name, namespace string, annotations, resources map[string]string, pcName string, replicas int) *v1.Deployment {
	zero := intstr.FromString("0%")
	hundred := intstr.FromString("100%")
	request := map[corev1.ResourceName]resource.Quantity{}
	for name, val := range resources {
		request[corev1.ResourceName(name)] = resource.MustParse(val)
	}

	zeroSec := int64(0)
	return &v1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: annotations,
		},
		Spec: v1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": name,
				},
			},
			Strategy: v1.DeploymentStrategy{
				Type: v1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &v1.RollingUpdateDeployment{
					MaxSurge:       &zero,
					MaxUnavailable: &hundred,
				},
			},
			Replicas: pointer.Int32Ptr(int32(replicas)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name": name,
					},
					Annotations: annotations,
				},
				Spec: corev1.PodSpec{
					SchedulerName:                 config.DefaultSchedulerName,
					TerminationGracePeriodSeconds: &zeroSec,
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: imageutils.GetE2EImage(imageutils.Nginx),
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 80,
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: request,
								Limits:   request,
							},
						},
					},
					PriorityClassName: pcName,
				},
			},
		},
	}
}

var _ = SIGDescribe("Reservation E2E", func() {
	f := framework.NewDefaultFramework("reservation-e2e")

	ginkgo.BeforeEach(func() {
		cs = f.ClientSet
		gs = f.Godelclient
		ns = f.Namespace.Name

		var err error
		framework.AllNodesReady(cs, time.Minute)

		nodeList, err = e2enode.GetReadySchedulableNodes(cs)
		framework.ExpectNoError(err, "failed to get nodes")

		framework.ExpectNoErrorWithOffset(0, err)
		for _, n := range nodeList.Items {
			workerNodes.Insert(n.Name)
		}

		reservedResource = randomResource(3)
		framework.Logf("set reserved resource to %v", reservedResource)

		for _, node := range nodeList.Items {
			nodeCopy := node.DeepCopy()
			nodeCopy.Status.Capacity[reservedResource] = resource.MustParse(reservedResourceQuantity)
			framework.ExpectNoError(patchNode(cs, &node, nodeCopy))
		}

		framework.ExpectNoError(framework.CheckTestingNSDeletedExcept(cs, ns), "failed to check testing namespace")
		for _, node := range nodeList.Items {
			framework.Logf("\nLogging pods the apiserver thinks is on node %v before test", node.Name)
			printAllPodsOnNode(cs, node.Name)
		}
	})

	ginkgo.AfterEach(func() {
		for _, node := range nodeList.Items {
			n, err := cs.CoreV1().Nodes().Get(context.Background(), node.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)

			nodeCopy := n.DeepCopy()
			delete(nodeCopy.Status.Capacity, reservedResource)
			framework.ExpectNoError(patchNode(cs, n, nodeCopy))
		}

		reservations, err := gs.SchedulingV1alpha1().Reservations(ns).List(context.TODO(), metav1.ListOptions{})
		framework.ExpectNoError(err)

		for _, reservation := range reservations.Items {
			framework.Logf("Deleting reservation %v", reservation.Name)
			gs.SchedulingV1alpha1().Reservations(ns).Delete(context.TODO(), reservation.Name, metav1.DeleteOptions{GracePeriodSeconds: int64Ptr(0)})
		}
	})

	ginkgo.It("reserved resource can not be acquired by other pods", func() {
		replicas := len(nodeList.Items)
		resDeploy := makeDeployment("reservation-deploy", ns, map[string]string{
			podutil.PodResourceReservationAnnotation:         "true",
			podutil.PodResourceReservationAnnotationForGodel: "true",
			util.CanBePreemptedAnnotationKey:                 "true",
			podutil.PodLauncherAnnotationKey:                 "kubelet",
			podutil.PodResourceTypeAnnotationKey:             "guaranteed",
		}, map[string]string{
			string(reservedResource): reservedResourceQuantity,
		}, lowPC, replicas)

		nonResDeploy := makeDeployment("non-res", ns, map[string]string{
			util.CanBePreemptedAnnotationKey:     "true",
			podutil.PodLauncherAnnotationKey:     "kubelet",
			podutil.PodResourceTypeAnnotationKey: "guaranteed",
		}, map[string]string{
			string(reservedResource): reservedResourceQuantity,
		}, highPC, replicas)

		{
			framework.Logf("Creating reservation request deployment")
			createDeployment(cs, resDeploy)

			framework.Logf("Waiting reservation request to be scheduled")
			waitForDeployBeScheduled(cs, resDeploy, scheduleTTL)

			framework.Logf("Scale down reservation request deployment")
			oldReplicas := *resDeploy.Spec.Replicas
			scaleDeployment(cs, resDeploy, 0)

			framework.Logf("Waiting for reservation cr to be created")
			framework.ExpectNoError(
				waitForReservationCreated(gs, resDeploy.Namespace, labels.NewSelector(), int(oldReplicas), createDuration),
				"failed to wait for reservation created")
		}

		{
			framework.Logf("Creating non-reservation request deployment")
			createDeployment(cs, nonResDeploy)
			time.Sleep(10 * time.Second)

			framework.Logf("Waiting for non-reservation pods to be pending")
			checkDeployBePending(cs, nonResDeploy, 5*time.Second)

			framework.Logf("Scale down non-reservation request pods")
			scaleDeployment(cs, nonResDeploy, 0)
		}

		{
			framework.Logf("Remove reservation annotation and scale up reservation request deployment")
			removeReservationAnnotation(cs, resDeploy)
			scaleDeployment(cs, resDeploy, int32(replicas))

			framework.Logf("Waiting for reservation pods to be scheduled")
			waitForDeployBeScheduled(cs, resDeploy, scheduleTTL)

			framework.Logf("Waiting for non-reservation pods to be scheduled by preempting reservation pods")
			// scale up non-res deploy
			scaleDeployment(cs, nonResDeploy, int32(replicas))
			waitForDeployBeScheduled(cs, nonResDeploy, scheduleTTL)
		}
	})

	ginkgo.It("reserved resource can be acquired by other pods after reservation timeout", func() {
		replicas := len(nodeList.Items)
		resDeploy := makeDeployment("reservation-deploy", ns, map[string]string{
			podutil.PodResourceReservationAnnotationForGodel: "true",
			util.CanBePreemptedAnnotationKey:                 "true",
			podutil.PodLauncherAnnotationKey:                 "kubelet",
			podutil.PodResourceTypeAnnotationKey:             "guaranteed",
		}, map[string]string{
			string(reservedResource): reservedResourceQuantity,
		}, lowPC, replicas)

		nonResDeploy := makeDeployment("non-res", ns, map[string]string{
			util.CanBePreemptedAnnotationKey:     "true",
			podutil.PodLauncherAnnotationKey:     "kubelet",
			podutil.PodResourceTypeAnnotationKey: "guaranteed",
		}, map[string]string{
			string(reservedResource): reservedResourceQuantity,
		}, highPC, replicas)

		{
			framework.Logf("Creating reservation request deployment")
			createDeployment(cs, resDeploy)

			framework.Logf("Waiting for reservation pods to be scheduled")
			waitForDeployBeScheduled(cs, resDeploy, scheduleTTL)
		}

		{
			framework.Logf("Scale down reservation request pods")
			oldReplicas := *(resDeploy.Spec.Replicas)
			scaleDeployment(cs, resDeploy, 0)

			framework.Logf("Waiting for reservation cr to be created")
			framework.ExpectNoError(
				waitForReservationCreated(gs, resDeploy.Namespace, labels.NewSelector(), int(oldReplicas), createDuration),
				"failed to wait for reservation created")
		}

		{
			framework.Logf("Creating non-reservation request deployment")
			createDeployment(cs, nonResDeploy)

			duration := time.Duration(int(reservationTTL) / 2)
			time.Sleep(duration)

			framework.Logf("Checking non-reservation pods should be pending during reservation ttl")
			checkDeployBePending(cs, nonResDeploy, 10*time.Second)
			deleteDeployment(cs, nonResDeploy)

			time.Sleep(duration)
			createDeployment(cs, nonResDeploy) // create again to skip backoff duration

			framework.Logf(fmt.Sprintf("Waiting for non-reservation pods to be scheduled after reservation ttl"))
			waitForDeployBeScheduled(cs, nonResDeploy, reservationTTL)
		}
	})

	ginkgo.It("reservation return pods can schedule without reservation cr", func() {
		replicas := len(nodeList.Items)
		resDeploy := makeDeployment("reservation-deploy", ns, map[string]string{
			podutil.PodResourceReservationAnnotation:         "true",
			podutil.PodResourceReservationAnnotationForGodel: "true",
			util.CanBePreemptedAnnotationKey:                 "true",
			podutil.PodLauncherAnnotationKey:                 "kubelet",
			podutil.PodResourceTypeAnnotationKey:             "guaranteed",
		}, map[string]string{
			string(reservedResource): reservedResourceQuantity,
		}, lowPC, replicas)

		{
			framework.Logf("Creating reservation request deployment")
			createDeployment(cs, resDeploy)

			framework.Logf("Waiting reservation request to be scheduled")
			waitForDeployBeScheduled(cs, resDeploy, scheduleTTL)

			framework.Logf("Scale down reservation request deployment")
			oldReplicas := *resDeploy.Spec.Replicas
			scaleDeployment(cs, resDeploy, 0)

			framework.Logf("Waiting for reservation cr to be created")
			framework.ExpectNoError(
				waitForReservationCreated(gs, resDeploy.Namespace, labels.NewSelector(), int(oldReplicas), createDuration),
				"failed to wait for reservation created")

		}

		{
			framework.Logf("Delete reservation cr")
			reservations, err := gs.SchedulingV1alpha1().Reservations(ns).List(context.TODO(), metav1.ListOptions{})
			framework.ExpectNoError(err)

			for _, reservation := range reservations.Items {
				framework.ExpectNoError(
					gs.SchedulingV1alpha1().Reservations(ns).Delete(context.TODO(), reservation.Name, metav1.DeleteOptions{GracePeriodSeconds: int64Ptr(0)}),
					"failed to delete reservation %s", reservation.Name)
			}

			framework.ExpectNoError(waitForReservationCreated(gs, ns, labels.NewSelector(), 0, deleteDuration), "reservation cr should be deleted")
		}

		{
			framework.Logf("Remove reservation annotation and scale up reservation request deployment")
			removeReservationAnnotation(cs, resDeploy)
			scaleDeployment(cs, resDeploy, int32(replicas))

			framework.Logf("Waiting for reservation return pods to be scheduled")
			waitForDeployBeScheduled(cs, resDeploy, scheduleTTL)
		}
	})

	// --------------------------------------------- Single Pod ---------------------------------------------

	ginkgo.It("reservation pods can be scheduled to other nodes when reserved node are unavailable", func() {
		pods := len(nodeList.Items)
		reservationPods := make([]string, 0, pods)
		for i := 0; i < pods; i++ {
			name := fmt.Sprintf("res-%d", i)
			framework.Logf("create reservation pod %s", name)
			p := createPausePod(f, pausePodConfig{
				Name:      name,
				Namespace: ns,
				Resources: &corev1.ResourceRequirements{
					Requests: map[corev1.ResourceName]resource.Quantity{
						reservedResource: resource.MustParse("1"),
					},
					Limits: map[corev1.ResourceName]resource.Quantity{
						reservedResource: resource.MustParse("1"),
					},
				},
				Annotations: map[string]string{
					podutil.ReservationIndexAnnotation:               name, // trigger resource reservation
					podutil.PodResourceReservationAnnotationForGodel: "true",
					util.CanBePreemptedAnnotationKey:                 "true",
					podutil.PodLauncherAnnotationKey:                 "kubelet",
					podutil.PodResourceTypeAnnotationKey:             "guaranteed",
				},
			})

			framework.ExpectNoError(waitForPodScheduled(cs, p.Namespace, p.Name, scheduleTTL), "failed to wait for pod %s to be scheduled", p.Name)
			reservationPods = append(reservationPods, p.Name)
		}

		reservedNode := make(map[string]string)
		for _, reservationPod := range reservationPods {
			p, err := cs.CoreV1().Pods(ns).Get(context.TODO(), reservationPod, metav1.GetOptions{})
			framework.ExpectNoError(err, "failed to get pod %s", reservationPod)
			framework.Logf("pod %s is scheduled on node %s", reservationPod, p.Spec.NodeName)

			if len(p.Spec.NodeName) == 0 {
				framework.Failf("pod %s has no node name", reservationPods)
			}
			reservedNode[reservationPod] = p.Spec.NodeName

			framework.ExpectNoError(
				cs.CoreV1().Pods(ns).Delete(context.TODO(), reservationPod, metav1.DeleteOptions{GracePeriodSeconds: int64Ptr(0)}),
				"failed to delete pod %s", reservationPod)
		}

		framework.Logf("waiting for pods to be deleted")
		for _, pod := range reservationPods {
			framework.ExpectNoError(waitForPodDeleted(cs, ns, pod, deleteDuration))
		}

		var randomNode string
		for _, nodeName := range reservedNode {
			randomNode = nodeName
			break
		}

		framework.Logf("taint node %s to unavailable", randomNode)
		taintNode(cs, randomNode, "test-taint", "", true)

		defer func() {
			framework.Logf("untaint node %s", randomNode)
			taintNode(cs, randomNode, "test-taint", "", false)
		}()

		for index := range reservedNode {
			name := fmt.Sprintf("return-%s", index)
			framework.Logf("create reservation return pod %s", name)
			p := createPausePod(f, pausePodConfig{
				Name:      name,
				Namespace: ns,
				Resources: &corev1.ResourceRequirements{
					Requests: map[corev1.ResourceName]resource.Quantity{
						reservedResource: resource.MustParse("1"),
					},
					Limits: map[corev1.ResourceName]resource.Quantity{
						reservedResource: resource.MustParse("1"),
					},
				},
				Annotations: map[string]string{
					podutil.ReservationIndexAnnotation:   index, // trigger resource reservation
					util.CanBePreemptedAnnotationKey:     "true",
					podutil.PodLauncherAnnotationKey:     "kubelet",
					podutil.PodResourceTypeAnnotationKey: "guaranteed",
				},
			})

			framework.Logf("Waiting for pod %s to be scheduled", p.Name)
			framework.ExpectNoError(waitForPodScheduled(cs, p.Namespace, p.Name, scheduleTTL), "failed to wait for pod %s to be scheduled", p.Name)

			pod, err := cs.CoreV1().Pods(ns).Get(context.TODO(), name, metav1.GetOptions{})
			framework.ExpectNoError(err, "failed to get pod %s", name)

			if pod.Spec.NodeName == randomNode {
				framework.Failf("pod %s should not be scheduled to unavailable node %s", name, randomNode)
			}
		}
	})

	// --------------------------------------------- Statefulset Pod ---------------------------------------------
	ginkgo.It("statefulset pods can trigger resource reservation", func() {
		pods := len(nodeList.Items)
		reservationPods := make([]string, 0, pods)
		for i := 0; i < pods; i++ {
			name := fmt.Sprintf("res-%d", i)
			framework.Logf("create reservation statefulset pod %s", name)
			p := createPausePod(f, pausePodConfig{
				Name:      name,
				Namespace: ns,
				Resources: &corev1.ResourceRequirements{
					Requests: map[corev1.ResourceName]resource.Quantity{
						reservedResource: resource.MustParse("3"),
					},
					Limits: map[corev1.ResourceName]resource.Quantity{
						reservedResource: resource.MustParse("3"),
					},
				},
				Annotations: map[string]string{
					podutil.PodResourceReservationAnnotationForGodel: "true",
					util.CanBePreemptedAnnotationKey:                 "true",
					podutil.PodLauncherAnnotationKey:                 "kubelet",
					podutil.PodResourceTypeAnnotationKey:             "guaranteed",
				},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       podutil.StatefulSetExtension, // trigger resource reservation
						Name:       name,
						UID:        types.UID(name),
						Controller: boolPtr(true),
					},
				},
			})

			framework.ExpectNoError(waitForPodScheduled(cs, p.Namespace, p.Name, scheduleTTL), "failed to wait for pod %s to be scheduled", p.Name)
			reservationPods = append(reservationPods, p.Name)
		}

		reservedNode := make(map[string]string)
		for _, reservationPod := range reservationPods {
			p, err := cs.CoreV1().Pods(ns).Get(context.TODO(), reservationPod, metav1.GetOptions{})
			framework.ExpectNoError(err, "failed to get pod %s", reservationPod)
			framework.Logf("pod %s is scheduled on node %s", reservationPod, p.Spec.NodeName)

			if len(p.Spec.NodeName) == 0 {
				framework.Failf("pod %s has no node name", reservationPods)
			}
			reservedNode[reservationPod] = p.Spec.NodeName

			framework.ExpectNoError(
				cs.CoreV1().Pods(ns).Delete(context.TODO(), reservationPod, metav1.DeleteOptions{GracePeriodSeconds: int64Ptr(0)}),
				"failed to delete pod %s", reservationPod)
		}

		framework.Logf("waiting for pods to be deleted")
		for _, pod := range reservationPods {
			framework.ExpectNoError(waitForPodDeleted(cs, ns, pod, deleteDuration))
		}

		framework.Logf("Waiting for reservation cr to be created")
		framework.ExpectNoError(
			waitForReservationCreated(gs, ns, labels.NewSelector(), pods, createDuration),
			"failed to wait for reservation created")

		for index, nodeName := range reservedNode {
			name := fmt.Sprintf("return-%s", index)
			framework.Logf("create reservation return pod %s", name)
			p := createPausePod(f, pausePodConfig{
				Name:      name,
				Namespace: ns,
				Resources: &corev1.ResourceRequirements{
					Requests: map[corev1.ResourceName]resource.Quantity{
						reservedResource: resource.MustParse("3"),
					},
					Limits: map[corev1.ResourceName]resource.Quantity{
						reservedResource: resource.MustParse("3"),
					},
				},
				Annotations: map[string]string{
					util.CanBePreemptedAnnotationKey:     "true",
					podutil.PodLauncherAnnotationKey:     "kubelet",
					podutil.PodResourceTypeAnnotationKey: "guaranteed",
				},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       podutil.StatefulSetExtension, // trigger resource reservation
						Name:       index,
						UID:        types.UID(index),
						Controller: boolPtr(true),
					},
				},
			})

			framework.Logf("Waiting for pod %s to be scheduled", p.Name)
			framework.ExpectNoError(waitForPodScheduled(cs, p.Namespace, p.Name, scheduleTTL), "failed to wait for pod %s to be scheduled", p.Name)

			pod, err := cs.CoreV1().Pods(ns).Get(context.TODO(), name, metav1.GetOptions{})
			framework.ExpectNoError(err, "failed to get pod %s", name)

			if pod.Spec.NodeName != nodeName {
				framework.Failf("pod %s should be scheduled to node %s", name, nodeName)
			}
		}
	})
})

func int64Ptr(i int64) *int64 {
	return &i
}

func boolPtr(val bool) *bool {
	return &val
}

func taintNode(cs kubernetes.Interface, nodeName string, taintKey string, taintValue string, add bool) {
	node, err := cs.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
	framework.ExpectNoError(err, "failed to get node %s", nodeName)

	nodeCopy := node.DeepCopy()
	taint := corev1.Taint{
		Key:    taintKey,
		Value:  taintValue,
		Effect: corev1.TaintEffectNoSchedule,
	}

	if add {
		nodeCopy.Spec.Taints = append(node.Spec.Taints, taint)
	} else {
		taints := make([]corev1.Taint, 0, len(node.Spec.Taints))
		for _, t := range node.Spec.Taints {
			if t.Key != taintKey {
				taints = append(taints, t)
			}
		}
		nodeCopy.Spec.Taints = taints
	}

	oldData, _ := json.Marshal(node)
	newData, _ := json.Marshal(nodeCopy)
	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, &corev1.Node{})
	framework.ExpectNoError(err, "failed to create two way merge patch")

	_, err = cs.CoreV1().Nodes().Patch(context.TODO(), nodeName, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
	framework.ExpectNoError(err, "failed to patch node %s", nodeName)
}

func removeReservationAnnotation(cs kubernetes.Interface, dp *v1.Deployment) {
	dpCopy := dp.DeepCopy()
	delete(dpCopy.Annotations, podutil.PodResourceReservationAnnotationForGodel)

	oldData, _ := json.Marshal(dp)
	newData, _ := json.Marshal(dpCopy)
	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, &v1.Deployment{})
	framework.ExpectNoError(err, "failed to create two way merge patch")

	_, err = cs.AppsV1().Deployments(dp.Namespace).Patch(
		context.TODO(),
		dp.Name,
		types.MergePatchType,
		patchBytes,
		metav1.PatchOptions{},
	)

	framework.ExpectNoError(err, "failed to patch deployment %s", dp.Name)
}

func waitForDeployBeScheduled(cs kubernetes.Interface, dp *v1.Deployment, timeout time.Duration) {
	// check if the deployment is created by list pods
	list, err := cs.CoreV1().Pods(dp.Namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set(map[string]string{
			"name": dp.Name,
		})).String(),
	})

	framework.ExpectNoError(err, "failed to list pods of deployment %s", dp.Name)
	framework.ExpectEqual(len(list.Items), int(*dp.Spec.Replicas), "pod count is not equal to expected replicas")

	scheduled, err := waitForPodsPassCheck(cs, list, timeout, func(pod *corev1.Pod) bool { return len(pod.Spec.NodeName) > 0 })
	framework.ExpectNoError(err, "deployment %s should be scheduled", dp.Name)
	framework.ExpectEqual(scheduled, int(*dp.Spec.Replicas), "scheduled pods count is not equal to reservation request pods count")
}

func checkDeployBePending(cs kubernetes.Interface, dp *v1.Deployment, timeout time.Duration) {
	// check if the deployment is created by list pods
	list, err := cs.CoreV1().Pods(dp.Namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set(map[string]string{
			"name": dp.Name,
		})).String(),
	})

	framework.ExpectNoError(err, "failed to list pods of deployment %s", dp.Name)
	framework.ExpectEqual(len(list.Items), int(*dp.Spec.Replicas), "pod count is not equal to expected replicas")

	unScheduled, err := waitForPodsPassCheck(cs, list, timeout, func(pod *corev1.Pod) bool { return len(pod.Spec.NodeName) == 0 })
	framework.ExpectNoError(err, "deployment %s should remain in Pending status", dp.Name)
	framework.ExpectEqual(unScheduled, int(*dp.Spec.Replicas), "unscheduled pods count is not equal to non-reservation request pods count")
}

func createDeployment(cs kubernetes.Interface, dp *v1.Deployment) {
	_, err := cs.AppsV1().Deployments(dp.Namespace).Create(context.TODO(), dp, metav1.CreateOptions{})
	framework.ExpectNoError(err, fmt.Sprintf("failed to create deployment %s", dp.Name))

	selector := labels.SelectorFromSet(map[string]string{"name": dp.Name})
	framework.ExpectNoError(waitForPodsCreated(cs, selector, int(*dp.Spec.Replicas), createDuration), fmt.Sprintf("failed to create pods of deployment %s", dp.Name))
	return
}

func deleteDeployment(cs kubernetes.Interface, dp *v1.Deployment) {
	err := cs.AppsV1().Deployments(dp.Namespace).Delete(context.TODO(), dp.Name, metav1.DeleteOptions{
		GracePeriodSeconds: int64Ptr(0),
	})
	framework.ExpectNoError(err, fmt.Sprintf("failed to delete deployment %s", dp.Name))

	selector := labels.SelectorFromSet(map[string]string{"name": dp.Name})
	framework.ExpectNoError(waitForPodsCreated(cs, selector, 0, deleteDuration), "failed to delete pods of deployment %s", dp.Name)
	return
}

func scaleDeployment(cs kubernetes.Interface, dp *v1.Deployment, replicas int32) {
	dp.Spec.Replicas = &replicas
	_, err := cs.AppsV1().Deployments(dp.Namespace).Update(context.TODO(), dp, metav1.UpdateOptions{})
	framework.ExpectNoError(err, fmt.Sprintf("failed to update deployment %s, scale to %d", dp.Name, replicas))

	framework.ExpectNoError(
		waitForPodsCreated(cs, labels.SelectorFromSet(map[string]string{"name": dp.Name}), int(replicas), deleteDuration),
		fmt.Sprintf("failed to scale deployment %s, scale to %d", dp.Name, replicas),
	)

	return
}

func waitForPodsPassCheck(cs kubernetes.Interface, pods *corev1.PodList, duration time.Duration, check func(pod *corev1.Pod) bool) (int, error) {
	pass := 0
	err := wait.PollImmediate(1*time.Second, duration, func() (bool, error) {
		pass = 0
		for _, pod := range pods.Items {
			p, err := cs.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}

			if check(p) {
				pass++
			}
		}

		if pass == len(pods.Items) {
			return true, nil
		}

		klog.InfoS("Waiting for pods pass check", "passed", pass, "all", len(pods.Items))
		return false, nil
	})
	if err != nil {
		return pass, fmt.Errorf("timed out waiting for pods, %v", err)
	}

	return pass, nil
}

func waitForPodsCreated(cs kubernetes.Interface, labelSelector labels.Selector, expect int, duration time.Duration) error {
	actual := 0
	err := wait.PollImmediate(1*time.Second, duration, func() (bool, error) {
		pods, err := cs.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{
			LabelSelector: labelSelector.String(),
		})
		if err != nil {
			return false, err
		}

		actual = len(pods.Items)
		if actual == expect {
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return fmt.Errorf("timed out waiting for pods created, expected %d got %d, %v", expect, actual, err)
	}

	return nil
}

func waitForReservationCreated(gs godelclient.Interface, ns string, labelSelector labels.Selector, expect int, duration time.Duration) error {
	actual := 0
	err := wait.PollImmediate(1*time.Second, duration, func() (bool, error) {
		res, err := gs.SchedulingV1alpha1().Reservations(ns).List(context.TODO(), metav1.ListOptions{
			LabelSelector: labelSelector.String(),
		})
		if err != nil {
			return false, err
		}

		actual = len(res.Items)
		if actual == expect {
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return fmt.Errorf("timed out waiting for reservations created, expected %d got %d, %v", expect, actual, err)
	}

	return nil
}

func waitForPodDeleted(cs kubernetes.Interface, ns, name string, duration time.Duration) error {
	err := wait.PollImmediate(1*time.Second, duration, func() (bool, error) {
		_, err := cs.CoreV1().Pods(ns).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("timed out waiting for pod deleted, %v", err)
	}

	return nil
}

func waitForPodScheduled(cs kubernetes.Interface, ns, name string, duration time.Duration) error {
	err := wait.PollImmediate(1*time.Second, duration, func() (done bool, err error) {
		p, err := cs.CoreV1().Pods(ns).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		if len(p.Spec.NodeName) > 0 {
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return fmt.Errorf("timed out waiting for pod deleted, %v", err)
	}

	return nil
}

func waitForNPodsScheduled(cs kubernetes.Interface, ns string, selector labels.Selector, expect int, duration time.Duration) error {
	err := wait.PollImmediate(1*time.Second, duration, func() (done bool, err error) {
		pods, err := cs.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{
			LabelSelector: selector.String(),
		})
		if err != nil {
			return false, err
		}

		scheduled := 0
		for _, pod := range pods.Items {
			if len(pod.Spec.NodeName) > 0 {
				scheduled++
			}
		}

		if scheduled == expect {
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return fmt.Errorf("timed out waiting for %d pods scheduled, %v", expect, err)
	}

	return nil
}
