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

package scheduling

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/uuid"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/framework/config"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	"github.com/kubewharf/godel-scheduler/pkg/util/constraints"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/test/e2e/framework"
)

var (
	timeout  = 10 * time.Minute
	waitTime = 2 * time.Second
)

// SIGDescribe annotates the test with the SIG label.
func SIGDescribe(text string, body func()) bool {
	return ginkgo.Describe("[sig-scheduling] "+text, body)
}

// WaitForStableCluster waits until all existing pods are scheduled and returns their amount.
func WaitForStableCluster(c clientset.Interface, workerNodes sets.String) int {
	startTime := time.Now()
	// Wait for all pods to be scheduled.
	allScheduledPods, allNotScheduledPods := getScheduledAndUnscheduledPods(c, workerNodes)
	for len(allNotScheduledPods) != 0 {
		time.Sleep(waitTime)
		if startTime.Add(timeout).Before(time.Now()) {
			framework.Logf("Timed out waiting for the following pods to schedule")
			for _, p := range allNotScheduledPods {
				framework.Logf("%v/%v", p.Namespace, p.Name)
			}
			framework.Failf("Timed out after %v waiting for stable cluster.", timeout)
			break
		}
		allScheduledPods, allNotScheduledPods = getScheduledAndUnscheduledPods(c, workerNodes)
	}
	return len(allScheduledPods)
}

// WaitForPodsToBeDeleted waits until pods that are terminating to get deleted.
func WaitForPodsToBeDeleted(c clientset.Interface) {
	startTime := time.Now()
	deleting := getDeletingPods(c, metav1.NamespaceAll)
	for len(deleting) != 0 {
		if startTime.Add(timeout).Before(time.Now()) {
			framework.Logf("Pods still not deleted")
			for _, p := range deleting {
				framework.Logf("%v/%v", p.Namespace, p.Name)
			}
			framework.Failf("Timed out after %v waiting for pods to be deleted", timeout)
			break
		}
		time.Sleep(waitTime)
		deleting = getDeletingPods(c, metav1.NamespaceAll)
	}
}

// getScheduledAndUnscheduledPods lists scheduled and not scheduled pods in all namespaces, with succeeded and failed pods filtered out.
func getScheduledAndUnscheduledPods(c clientset.Interface, workerNodes sets.String) (scheduledPods, notScheduledPods []v1.Pod) {
	pods, err := c.CoreV1().Pods(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	framework.ExpectNoError(err, fmt.Sprintf("listing all pods in namespace %q while waiting for stable cluster", metav1.NamespaceAll))

	// API server returns also Pods that succeeded. We need to filter them out.
	filteredPods := make([]v1.Pod, 0, len(pods.Items))
	for _, p := range pods.Items {
		if !podTerminated(p) {
			filteredPods = append(filteredPods, p)
		}
	}
	pods.Items = filteredPods
	return GetPodsScheduled(workerNodes, pods)
}

// getDeletingPods returns whether there are any pods marked for deletion.
func getDeletingPods(c clientset.Interface, ns string) []v1.Pod {
	pods, err := c.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{})
	framework.ExpectNoError(err, fmt.Sprintf("listing all pods in namespace %q while waiting for pods to terminate", ns))
	var deleting []v1.Pod
	for _, p := range pods.Items {
		if p.ObjectMeta.DeletionTimestamp != nil && !podTerminated(p) {
			deleting = append(deleting, p)
		}
	}
	return deleting
}

func podTerminated(p v1.Pod) bool {
	return p.Status.Phase == v1.PodSucceeded || p.Status.Phase == v1.PodFailed
}

func GetPodAnnotations(resourceType podutil.PodResourceType, podLauncher podutil.PodLauncher) map[string]string {
	return map[string]string{
		podutil.PodResourceTypeAnnotationKey: string(resourceType),
		podutil.PodLauncherAnnotationKey:     string(podLauncher),
	}
}

func WithVictimPods(anno map[string]string, nominatedNode api.NominatedNode) map[string]string {
	e, _ := json.Marshal(nominatedNode)
	anno[podutil.PodStateAnnotationKey] = string(podutil.PodAssumed)
	anno[podutil.NominatedNodeAnnotationKey] = string(e)
	anno[podutil.SchedulerAnnotationKey] = config.DefaultSchedulerName

	return anno
}

func WithPreemptible(anno map[string]string, preemptible bool) map[string]string {
	if preemptible {
		anno[util.CanBePreemptedAnnotationKey] = util.CanBePreempted
	} else {
		anno[util.CanBePreemptedAnnotationKey] = util.CannotBePreempted
	}
	return anno
}

func WithPodGroup(anno map[string]string, podGroupName string) map[string]string {
	anno[podutil.PodGroupNameAnnotationKey] = podGroupName
	return anno
}

func WithHardConstraints(anno map[string]string, constraintNames []string) map[string]string {
	anno[constraints.HardConstraintsAnnotationKey] = strings.Join(constraintNames, ",")
	return anno
}

func WithSoftConstraints(anno map[string]string, constraintNames []string) map[string]string {
	anno[constraints.SoftConstraintsAnnotationKey] = strings.Join(constraintNames, ",")
	return anno
}

func GenerateLabelThatMatchNoNode(nodes []v1.Node) (key, value string) {
	nodeLabels := make(map[string]string)
	for _, n := range nodes {
		for k, v := range n.Labels {
			nodeLabels[k] = v
		}
	}

	key = fmt.Sprintf("godel.bytedance.com/e2e-%s", string(uuid.NewUUID()))
	value = string(uuid.NewUUID())
	for {
		if nodeLabels[key] == value {
			key = fmt.Sprintf("godel.bytedance.com/e2e-%s", string(uuid.NewUUID()))
			value = string(uuid.NewUUID())
		} else {
			break
		}
	}
	return
}
