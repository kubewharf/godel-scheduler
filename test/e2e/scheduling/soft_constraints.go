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
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/onsi/ginkgo"
	_ "github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"

	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/interpodaffinity"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodeaffinity"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodepreferavoidpods"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/podtopologyspread"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/tainttoleration"
	schedutil "github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/pkg/util/pod/qos"
	"github.com/kubewharf/godel-scheduler/test/e2e/framework"
	e2enode "github.com/kubewharf/godel-scheduler/test/e2e/framework/node"
	e2epod "github.com/kubewharf/godel-scheduler/test/e2e/framework/pod"
	e2erc "github.com/kubewharf/godel-scheduler/test/e2e/framework/rc"
	e2eskipper "github.com/kubewharf/godel-scheduler/test/e2e/framework/skipper"
	testutils "github.com/kubewharf/godel-scheduler/test/utils"
	imageutils "github.com/kubewharf/godel-scheduler/test/utils/image"
)

// Resource is a collection of compute resource.
type Resource struct {
	MilliCPU int64
	Memory   int64
}

var balancePodLabel = map[string]string{"name": "priority-balanced-memory"}

var podRequestedResource = &v1.ResourceRequirements{
	Limits: v1.ResourceList{
		v1.ResourceMemory: resource.MustParse("100Mi"),
		v1.ResourceCPU:    resource.MustParse("100m"),
	},
	Requests: v1.ResourceList{
		v1.ResourceMemory: resource.MustParse("100Mi"),
		v1.ResourceCPU:    resource.MustParse("100m"),
	},
}

// addOrUpdateAvoidPodOnNode adds avoidPods annotations to node, will override if it exists
func addOrUpdateAvoidPodOnNode(c clientset.Interface, nodeName string, avoidPods v1.AvoidPods) {
	err := wait.PollImmediate(framework.Poll, framework.SingleCallTimeout, func() (bool, error) {
		node, err := c.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
		if err != nil {
			if testutils.IsRetryableAPIError(err) {
				return false, nil
			}
			return false, err
		}

		taintsData, err := json.Marshal(avoidPods)
		framework.ExpectNoError(err)

		if node.Annotations == nil {
			node.Annotations = make(map[string]string)
		}
		node.Annotations[v1.PreferAvoidPodsAnnotationKey] = string(taintsData)
		_, err = c.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})
		if err != nil {
			if !apierrors.IsConflict(err) {
				framework.ExpectNoError(err)
			} else {
				framework.Logf("Conflict when trying to add/update avoidPods %v to %v with error %v", avoidPods, nodeName, err)
				return false, nil
			}
		}
		return true, nil
	})
	framework.ExpectNoError(err)
}

// removeAvoidPodsOffNode removes AvoidPods annotations from the node. It does not fail if no such annotation exists.
func removeAvoidPodsOffNode(c clientset.Interface, nodeName string) {
	err := wait.PollImmediate(framework.Poll, framework.SingleCallTimeout, func() (bool, error) {
		node, err := c.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
		if err != nil {
			if testutils.IsRetryableAPIError(err) {
				return false, nil
			}
			return false, err
		}

		if node.Annotations == nil {
			return true, nil
		}
		delete(node.Annotations, v1.PreferAvoidPodsAnnotationKey)
		_, err = c.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})
		if err != nil {
			if !apierrors.IsConflict(err) {
				framework.ExpectNoError(err)
			} else {
				framework.Logf("Conflict when trying to remove avoidPods to %v", nodeName)
				return false, nil
			}
		}
		return true, nil
	})
	framework.ExpectNoError(err)
}

// This test suite is used to verifies scheduler soft constraints functions
var _ = SIGDescribe("SchedulingSoftConstraints [Serial]", func() {
	var cs clientset.Interface
	var fs godelclient.Interface
	var nodeList *v1.NodeList
	var systemPodsNo int
	var ns string
	f := framework.NewDefaultFramework("sched-priority")

	ginkgo.AfterEach(func() {
		for _, node := range nodeList.Items {
			err := fs.NodeV1alpha1().NMNodes().Delete(context.TODO(), node.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
		}
	})

	ginkgo.BeforeEach(func() {
		cs = f.ClientSet
		fs = f.Godelclient
		ns = f.Namespace.Name
		nodeList = &v1.NodeList{}
		var err error

		e2enode.WaitForTotalHealthy(cs, time.Minute)
		nodeList, err = e2enode.GetReadySchedulableNodes(cs)
		if err != nil {
			framework.Logf("Unexpected error occurred: %v", err)
		}
		framework.ExpectNoErrorWithOffset(0, err)

		for _, node := range nodeList.Items {
			nmnode := GetNMNodeTemplateByNode(&node)
			_, err := fs.NodeV1alpha1().NMNodes().Create(context.TODO(), nmnode, metav1.CreateOptions{})
			framework.ExpectNoError(err)
		}

		err = framework.CheckTestingNSDeletedExcept(cs, ns)
		framework.ExpectNoError(err)
		err = e2epod.WaitForPodsRunningReady(cs, metav1.NamespaceSystem, int32(systemPodsNo), 0, framework.PodReadyBeforeTimeout, map[string]string{})
		framework.ExpectNoError(err)
	})

	// Create a pod with a label and then create a pod with preferred podAntiAffinity terms that match the label.
	// Verify that the pod is not scheduled to the node that the first pod is scheduled to.
	ginkgo.It("Pod should be scheduled to node that don't match the PodAntiAffinity terms", func() {

		e2eskipper.SkipUnlessNodeCountIsAtLeast(2)

		ginkgo.By("Trying to launch a pod with a label to get a node which can launch it.")
		pod := runPausePod(f, pausePodConfig{
			Name:        "pod-with-label-security-s1",
			Annotations: GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet),
			Labels:      map[string]string{"security": "S1"},
		})
		nodeName := pod.Spec.NodeName

		k := v1.LabelHostname
		ginkgo.By("Verifying the node has a label " + k)
		node, err := cs.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
		framework.ExpectNoError(err)
		if _, hasLabel := node.Labels[k]; !hasLabel {
			// If the label is not exists, label all nodes for testing.

			ginkgo.By("Trying to apply a label on the found node.")
			k = "kubernetes.io/e2e-node-topologyKey"
			v := "topologyvalue1"
			framework.AddOrUpdateLabelOnNode(cs, nodeName, k, v)
			framework.ExpectNodeHasLabel(cs, nodeName, k, v)
			defer framework.RemoveLabelOffNode(cs, nodeName, k)

			ginkgo.By("Trying to apply a label on other nodes.")
			v = "topologyvalue2"
			for _, node := range nodeList.Items {
				if node.Name != nodeName {
					framework.AddOrUpdateLabelOnNode(cs, node.Name, k, v)
					framework.ExpectNodeHasLabel(cs, node.Name, k, v)
					defer framework.RemoveLabelOffNode(cs, node.Name, k)
				}
			}
		}

		// make the nodes have balanced cpu,mem usage
		ginkgo.By("Trying to create balanced pods for all nodes.")
		err = createBalancedPodForNodes(f, cs, ns, nodeList.Items, podRequestedResource, 0.6)
		framework.ExpectNoError(err)
		ginkgo.By("Trying to launch the pod with podAntiAffinity.")
		labelPodName := "pod-with-pod-antiaffinity"
		pod = createPausePod(f, pausePodConfig{
			Resources: podRequestedResource,
			Name:      labelPodName,
			Annotations: WithSoftConstraints(
				WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet),
					[]string{interpodaffinity.Name}), []string{interpodaffinity.Name}),
			Affinity: &v1.Affinity{
				PodAntiAffinity: &v1.PodAntiAffinity{
					PreferredDuringSchedulingIgnoredDuringExecution: []v1.WeightedPodAffinityTerm{
						{
							PodAffinityTerm: v1.PodAffinityTerm{
								LabelSelector: &metav1.LabelSelector{
									MatchExpressions: []metav1.LabelSelectorRequirement{
										{
											Key:      "security",
											Operator: metav1.LabelSelectorOpIn,
											Values:   []string{"S1", "value2"},
										},
										{
											Key:      "security",
											Operator: metav1.LabelSelectorOpNotIn,
											Values:   []string{"S2"},
										}, {
											Key:      "security",
											Operator: metav1.LabelSelectorOpExists,
										},
									},
								},
								TopologyKey: k,
								Namespaces:  []string{ns},
							},
							Weight: 10,
						},
					},
				},
			},
		})
		ginkgo.By("Wait the pod becomes running")
		framework.ExpectNoError(e2epod.WaitForPodNameRunningInNamespace(f.ClientSet, pod.Name, f.Namespace.Name))
		labelPod, err := cs.CoreV1().Pods(ns).Get(context.TODO(), labelPodName, metav1.GetOptions{})
		framework.ExpectNoError(err)
		ginkgo.By("Verify the pod was scheduled to the expected node.")
		framework.ExpectNotEqual(labelPod.Spec.NodeName, nodeName)
	})

	// Create a pod[node-manager] with a label and then create a pod[kubelet] with preferred podAntiAffinity terms that match the label.
	// Verify that the pod is not scheduled to the node that the first pod is scheduled to.
	ginkgo.It("Pod should be scheduled to node that don't match the PodAntiAffinity terms for mix of nmnodes and nodes", func() {

		e2eskipper.SkipUnlessNodeCountIsAtLeast(2)

		ginkgo.By("Trying to launch a pod with a label to get a node which can launch it.")
		pod := runPausePod(f, pausePodConfig{
			Name:        "pod-with-label-security-s1",
			Annotations: GetPodAnnotations(podutil.GuaranteedPod, podutil.NodeManager),
			Labels:      map[string]string{"security": "S1"},
		})
		nodeName := pod.Spec.NodeName

		// make the nodes have balanced cpu,mem usage
		ginkgo.By("Trying to create balanced pods for all nodes.")
		err := createBalancedPodForNodes(f, cs, ns, nodeList.Items, podRequestedResource, 0.6)
		framework.ExpectNoError(err)
		ginkgo.By("Trying to launch the pod with podAntiAffinity.")
		labelPodName := "pod-with-pod-antiaffinity"
		pod = createPausePod(f, pausePodConfig{
			Resources: podRequestedResource,
			Name:      labelPodName,
			Annotations: WithSoftConstraints(
				WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet),
					[]string{interpodaffinity.Name}), []string{interpodaffinity.Name}),
			Affinity: &v1.Affinity{
				PodAntiAffinity: &v1.PodAntiAffinity{
					PreferredDuringSchedulingIgnoredDuringExecution: []v1.WeightedPodAffinityTerm{
						{
							PodAffinityTerm: v1.PodAffinityTerm{
								LabelSelector: &metav1.LabelSelector{
									MatchExpressions: []metav1.LabelSelectorRequirement{
										{
											Key:      "security",
											Operator: metav1.LabelSelectorOpIn,
											Values:   []string{"S1", "value2"},
										},
										{
											Key:      "security",
											Operator: metav1.LabelSelectorOpNotIn,
											Values:   []string{"S2"},
										}, {
											Key:      "security",
											Operator: metav1.LabelSelectorOpExists,
										},
									},
								},
								TopologyKey: v1.LabelHostname,
								Namespaces:  []string{ns},
							},
							Weight: 10,
						},
					},
				},
			},
		})
		ginkgo.By("Wait the pod becomes running")
		framework.ExpectNoError(e2epod.WaitForPodNameRunningInNamespace(f.ClientSet, pod.Name, f.Namespace.Name))
		labelPod, err := cs.CoreV1().Pods(ns).Get(context.TODO(), labelPodName, metav1.GetOptions{})
		framework.ExpectNoError(err)
		ginkgo.By("Verify the pod was scheduled to the expected node.")
		framework.ExpectNotEqual(labelPod.Spec.NodeName, nodeName)
	})

	// Create as many pods as the number of nodes with pod topology sprea constrain (MaxSkew = 1) and check whether they are evenly scheduled on each node.
	ginkgo.It("Pod should be evenly scheduled to node that match the preferred pod topology spread constraint", func() {

		e2eskipper.SkipUnlessNodeCountIsAtLeast(2)

		testLabelKey := "godel.bytedance.com/test-label-preferred-pod-topology"
		testLabelValue := "test-value-pod-topology"
		nodeListNames := make([]string, 0, len(nodeList.Items))
		for _, node := range nodeList.Items {
			nodeListNames = append(nodeListNames, node.Name)
			ginkgo.By(fmt.Sprintf("node %s has label %s=%s", node.Name, v1.LabelHostname, node.Labels[v1.LabelHostname]))
		}
		scheduledNodeNames := make([]string, 0, len(nodeListNames))

		// make the nodes have balanced cpu,mem usage
		ginkgo.By("Trying to create balanced pods for all nodes.")
		err := createBalancedPodForNodes(f, cs, ns, nodeList.Items, podRequestedResource, 0.6)
		framework.ExpectNoError(err)
		ginkgo.By(fmt.Sprintf("Trying to launch %d pod with preferred pod topology spread constraints evenly.", len(nodeList.Items)))
		for i := 0; i < len(nodeListNames); i++ {
			pod := createPausePod(f, pausePodConfig{
				// Because the github ci machine has limited resources, the kind-control-plane node does not have enough resources
				// So we don't set the resource requests and limits for the pod
				// Resources: podRequestedResource,
				Name:   "topology-spread-pod-" + string(uuid.NewUUID()),
				Labels: map[string]string{testLabelKey: testLabelValue},
				Annotations: WithSoftConstraints(
					WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet),
						[]string{podtopologyspread.Name}), []string{podtopologyspread.Name}),
				TopologySpreadConstraints: []v1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       v1.LabelHostname,
						WhenUnsatisfiable: v1.ScheduleAnyway,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{testLabelKey: testLabelValue},
						},
					},
				},
			})

			ginkgo.By("Wait the pod becomes running")
			framework.ExpectNoError(e2epod.WaitForPodNameRunningInNamespace(f.ClientSet, pod.Name, f.Namespace.Name))
			scheduledPod, err := cs.CoreV1().Pods(ns).Get(context.TODO(), pod.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			scheduledNodeNames = append(scheduledNodeNames, scheduledPod.Spec.NodeName)
			ginkgo.By(fmt.Sprintf("Pod %s was scheduled to node %s", pod.Name, scheduledPod.Spec.NodeName))
		}

		ginkgo.By("Verify the pod was evenly scheduled to the nodes.")
		framework.ExpectConsistOf(scheduledNodeNames, nodeListNames)
	})

	ginkgo.It("Pod should avoid nodes that have avoidPod annotation", func() {
		nodeName := nodeList.Items[0].Name
		// make the nodes have balanced cpu,mem usage
		err := createBalancedPodForNodes(f, cs, ns, nodeList.Items, podRequestedResource, 0.5)
		framework.ExpectNoError(err)
		ginkgo.By("Create a RC, with 0 replicas")
		rc := createRC(ns, "scheduler-priority-avoid-pod", int32(0), map[string]string{"name": "scheduler-priority-avoid-pod"}, f, podRequestedResource)
		// Cleanup the replication controller when we are done.
		defer func() {
			// Resize the replication controller to zero to get rid of pods.
			if err := e2erc.DeleteRCAndWaitForGC(f.ClientSet, f.Namespace.Name, rc.Name); err != nil {
				framework.Logf("Failed to cleanup replication controller %v: %v.", rc.Name, err)
			}
		}()

		ginkgo.By("Trying to apply avoidPod annotations on the first node.")
		avoidPod := v1.AvoidPods{
			PreferAvoidPods: []v1.PreferAvoidPodsEntry{
				{
					PodSignature: v1.PodSignature{
						PodController: &metav1.OwnerReference{
							APIVersion: "v1",
							Kind:       "ReplicationController",
							Name:       rc.Name,
							UID:        rc.UID,
							Controller: func() *bool { b := true; return &b }(),
						},
					},
					Reason:  "some reson",
					Message: "some message",
				},
			},
		}
		action := func() error {
			addOrUpdateAvoidPodOnNode(cs, nodeName, avoidPod)
			return nil
		}
		predicate := func(node *v1.Node) bool {
			val, err := json.Marshal(avoidPod)
			if err != nil {
				return false
			}
			return node.Annotations[v1.PreferAvoidPodsAnnotationKey] == string(val)
		}
		success, err := observeNodeUpdateAfterAction(f.ClientSet, nodeName, predicate, action)
		framework.ExpectNoError(err)
		framework.ExpectEqual(success, true)

		defer removeAvoidPodsOffNode(cs, nodeName)

		ginkgo.By(fmt.Sprintf("Scale the RC: %s to len(nodeList.Item)-1 : %v.", rc.Name, len(nodeList.Items)-1))

		e2erc.ScaleRC(f.ClientSet, f.ScalesGetter, ns, rc.Name, uint(len(nodeList.Items)-1), true)
		testPods, err := cs.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{
			LabelSelector: "name=scheduler-priority-avoid-pod",
		})
		framework.ExpectNoError(err)
		ginkgo.By(fmt.Sprintf("Verify the pods should not scheduled to the node: %s", nodeName))
		for _, pod := range testPods.Items {
			framework.ExpectNotEqual(pod.Spec.NodeName, nodeName)
		}
	})

	// Create as many pods as the number of nodes with pod topology sprea constrain (MaxSkew = 1) and check whether they are evenly scheduled on each node.
	ginkgo.It("Pod that match the preferred pod topology spread constraint should be evenly scheduled to nmnodes and nodes", func() {

		e2eskipper.SkipUnlessNodeCountIsAtLeast(2)

		testLabelKey := "godel.bytedance.com/test-label-preferred-pod-topology"
		testLabelValue := "test-value-pod-topology"
		nodeListNames := make([]string, 0, len(nodeList.Items))
		for _, node := range nodeList.Items {
			nodeListNames = append(nodeListNames, node.Name)
			ginkgo.By(fmt.Sprintf("node %s has label %s=%s", node.Name, v1.LabelHostname, node.Labels[v1.LabelHostname]))
		}
		scheduledNodeNames := make([]string, 0, len(nodeListNames))

		// make the nodes have balanced cpu,mem usage
		ginkgo.By("Trying to create balanced pods for all nodes.")
		err := createBalancedPodForNodes(f, cs, ns, nodeList.Items, podRequestedResource, 0.6)
		framework.ExpectNoError(err)
		ginkgo.By(fmt.Sprintf("Trying to launch %d pod with preferred pod topology spread constraints evenly.", len(nodeList.Items)))
		for i := 0; i < len(nodeListNames); i++ {
			pod := createPausePod(f, pausePodConfig{
				// Because the github ci machine has limited resources, the kind-control-plane node does not have enough resources
				// So we don't set the resource requests and limits for the pod
				// Resources: podRequestedResource,
				Name:   "topology-spread-pod-" + string(uuid.NewUUID()),
				Labels: map[string]string{testLabelKey: testLabelValue},
				Annotations: WithSoftConstraints(
					WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet),
						[]string{podtopologyspread.Name}), []string{podtopologyspread.Name}),
				TopologySpreadConstraints: []v1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       v1.LabelHostname,
						WhenUnsatisfiable: v1.ScheduleAnyway,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{testLabelKey: testLabelValue},
						},
					},
				},
			})

			if i%2 == 0 {
				pod.Annotations = WithSoftConstraints(
					WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.NodeManager),
						[]string{podtopologyspread.Name}), []string{podtopologyspread.Name})
			} else {
				pod.Annotations = WithSoftConstraints(
					WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet),
						[]string{podtopologyspread.Name}), []string{podtopologyspread.Name})
			}

			ginkgo.By("Wait the pod becomes running")
			framework.ExpectNoError(e2epod.WaitForPodNameRunningInNamespace(f.ClientSet, pod.Name, f.Namespace.Name))
			scheduledPod, err := cs.CoreV1().Pods(ns).Get(context.TODO(), pod.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			scheduledNodeNames = append(scheduledNodeNames, scheduledPod.Spec.NodeName)
			ginkgo.By(fmt.Sprintf("Pod %s was scheduled to node %s", pod.Name, scheduledPod.Spec.NodeName))
		}

		ginkgo.By("Verify the pod was evenly scheduled to the nodes.")
		framework.ExpectConsistOf(scheduledNodeNames, nodeListNames)
	})

	ginkgo.It("Pod should be preferably scheduled to nodes pod can tolerate[Tolerate]", func() {
		// make the nodes have balanced cpu,mem usage ratio
		err := createBalancedPodForNodes(f, cs, ns, nodeList.Items, podRequestedResource, 0.5)
		framework.ExpectNoError(err)
		// Apply 10 taints to first node
		nodeName := nodeList.Items[0].Name

		ginkgo.By(fmt.Sprintf("Trying to apply 10 (tolerable) taints on the first node: %s.", nodeName))
		var tolerations []v1.Toleration
		for i := 0; i < 10; i++ {
			testTaint := addRandomTaintToNode(cs, nodeName, v1.TaintEffectPreferNoSchedule)
			tolerations = append(tolerations, v1.Toleration{Key: testTaint.Key, Value: testTaint.Value, Effect: testTaint.Effect})
			defer e2enode.RemoveTaintOffNode(cs, nodeName, *testTaint)
		}
		ginkgo.By("Adding 10 intolerable taints to all other nodes")
		for i := 1; i < len(nodeList.Items); i++ {
			node := nodeList.Items[i]
			for j := 0; j < 10; j++ {
				testTaint := addRandomTaintToNode(cs, node.Name, v1.TaintEffectPreferNoSchedule)
				defer e2enode.RemoveTaintOffNode(cs, node.Name, *testTaint)
			}
		}

		tolerationPodName := "with-tolerations"
		ginkgo.By("Create a pod that tolerates all the taints of the first node.")
		pod := createPausePod(f, pausePodConfig{
			Name: tolerationPodName,
			Annotations: WithSoftConstraints(
				WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{tainttoleration.Name}),
				[]string{tainttoleration.Name},
			),
			Tolerations: tolerations,
		})
		framework.ExpectNoError(e2epod.WaitForPodNameRunningInNamespace(f.ClientSet, pod.Name, f.Namespace.Name))

		ginkgo.By("Pod should prefer scheduled to the node that pod can tolerate.")
		tolePod, err := cs.CoreV1().Pods(ns).Get(context.TODO(), tolerationPodName, metav1.GetOptions{})
		framework.ExpectNoError(err)
		framework.ExpectEqual(tolePod.Spec.NodeName, nodeName)
	})
})

// createBalancedPodForNodes creates a pod per node that asks for enough resources to make all nodes have the same mem/cpu usage ratio.
func createBalancedPodForNodes(f *framework.Framework, cs clientset.Interface, ns string, nodes []v1.Node, requestedResource *v1.ResourceRequirements, ratio float64) error {
	// find the max, if the node has the max,use the one, if not,use the ratio parameter
	var maxCPUFraction, maxMemFraction float64 = ratio, ratio
	cpuFractionMap := make(map[string]float64)
	memFractionMap := make(map[string]float64)
	for _, node := range nodes {
		cpuFraction, memFraction := computeCPUMemFraction(cs, node, requestedResource)
		cpuFractionMap[node.Name] = cpuFraction
		memFractionMap[node.Name] = memFraction
		if cpuFraction > maxCPUFraction {
			maxCPUFraction = cpuFraction
		}
		if memFraction > maxMemFraction {
			maxMemFraction = memFraction
		}
	}
	// we need the max one to keep the same cpu/mem use rate
	ratio = math.Max(maxCPUFraction, maxMemFraction)
	for _, node := range nodes {
		memAllocatable, found := node.Status.Allocatable[v1.ResourceMemory]
		framework.ExpectEqual(found, true)
		memAllocatableVal := memAllocatable.Value()

		cpuAllocatable, found := node.Status.Allocatable[v1.ResourceCPU]
		framework.ExpectEqual(found, true)
		cpuAllocatableMil := cpuAllocatable.MilliValue()

		needCreateResource := v1.ResourceList{}
		cpuFraction := cpuFractionMap[node.Name]
		memFraction := memFractionMap[node.Name]
		needCreateResource[v1.ResourceCPU] = *resource.NewMilliQuantity(int64((ratio-cpuFraction)*float64(cpuAllocatableMil)), resource.DecimalSI)

		needCreateResource[v1.ResourceMemory] = *resource.NewQuantity(int64((ratio-memFraction)*float64(memAllocatableVal)), resource.BinarySI)

		podConfig := &pausePodConfig{
			Name: "",
			Annotations: WithSoftConstraints(
				WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{nodeaffinity.Name}),
				[]string{nodepreferavoidpods.Name},
			),
			Labels: balancePodLabel,
			Resources: &v1.ResourceRequirements{
				Limits:   needCreateResource,
				Requests: needCreateResource,
			},
			Affinity: &v1.Affinity{
				NodeAffinity: &v1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
						NodeSelectorTerms: []v1.NodeSelectorTerm{
							{
								MatchFields: []v1.NodeSelectorRequirement{
									{Key: "metadata.name", Operator: v1.NodeSelectorOpIn, Values: []string{node.Name}},
								},
							},
						},
					},
				},
			},
		}

		err := testutils.StartPods(cs, 1, ns, string(uuid.NewUUID()),
			*initPausePod(f, *podConfig), true, framework.Logf)
		if err != nil {
			return err
		}
	}

	for _, node := range nodes {
		ginkgo.By("Compute Cpu, Mem Fraction after create balanced pods.")
		computeCPUMemFraction(cs, node, requestedResource)
	}

	return nil
}

func computeCPUMemFraction(cs clientset.Interface, node v1.Node, resource *v1.ResourceRequirements) (float64, float64) {
	framework.Logf("ComputeCPUMemFraction for node: %v", node.Name)
	totalRequestedCPUResource := resource.Requests.Cpu().MilliValue()
	totalRequestedMemResource := resource.Requests.Memory().Value()
	allpods, err := cs.CoreV1().Pods(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		framework.Failf("Expect error of invalid, got : %v", err)
	}
	for _, pod := range allpods.Items {
		if pod.Spec.NodeName == node.Name {
			framework.Logf("Pod for on the node: %v, Cpu: %v, Mem: %v", pod.Name, getNonZeroRequests(&pod).MilliCPU, getNonZeroRequests(&pod).Memory)
			// Ignore best effort pods while computing fractions as they won't be taken in account by scheduler.
			if qos.GetKubernetesDefinedPodQOS(&pod) == v1.PodQOSBestEffort {
				continue
			}
			totalRequestedCPUResource += getNonZeroRequests(&pod).MilliCPU
			totalRequestedMemResource += getNonZeroRequests(&pod).Memory
		}
	}
	cpuAllocatable, found := node.Status.Allocatable[v1.ResourceCPU]
	framework.ExpectEqual(found, true)
	cpuAllocatableMil := cpuAllocatable.MilliValue()

	floatOne := float64(1)
	cpuFraction := float64(totalRequestedCPUResource) / float64(cpuAllocatableMil)
	if cpuFraction > floatOne {
		cpuFraction = floatOne
	}
	memAllocatable, found := node.Status.Allocatable[v1.ResourceMemory]
	framework.ExpectEqual(found, true)
	memAllocatableVal := memAllocatable.Value()
	memFraction := float64(totalRequestedMemResource) / float64(memAllocatableVal)
	if memFraction > floatOne {
		memFraction = floatOne
	}

	framework.Logf("Node: %v, totalRequestedCPUResource: %v, cpuAllocatableMil: %v, cpuFraction: %v", node.Name, totalRequestedCPUResource, cpuAllocatableMil, cpuFraction)
	framework.Logf("Node: %v, totalRequestedMemResource: %v, memAllocatableVal: %v, memFraction: %v", node.Name, totalRequestedMemResource, memAllocatableVal, memFraction)

	return cpuFraction, memFraction
}

func getNonZeroRequests(pod *v1.Pod) Resource {
	result := Resource{}
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		cpu, memory := schedutil.GetNonzeroRequests(&container.Resources.Requests)
		result.MilliCPU += cpu
		result.Memory += memory
	}
	return result
}

func createRC(ns, rsName string, replicas int32, rcPodLabels map[string]string, f *framework.Framework, resource *v1.ResourceRequirements) *v1.ReplicationController {
	rc := &v1.ReplicationController{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ReplicationController",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: rsName,
		},
		Spec: v1.ReplicationControllerSpec{
			Replicas: &replicas,
			Template: &v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      rcPodLabels,
					Annotations: WithSoftConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{nodepreferavoidpods.Name}),
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:      rsName,
							Image:     imageutils.GetPauseImageName(),
							Resources: *resource,
						},
					},
				},
			},
		},
	}
	rc, err := f.ClientSet.CoreV1().ReplicationControllers(ns).Create(context.TODO(), rc, metav1.CreateOptions{})
	framework.ExpectNoError(err)
	return rc
}

func addRandomTaintToNode(cs clientset.Interface, nodeName string, effect v1.TaintEffect) *v1.Taint {
	testTaint := v1.Taint{
		Key:    fmt.Sprintf("kubernetes.io/e2e-taint-key-%s", string(uuid.NewUUID())),
		Value:  fmt.Sprintf("testing-taint-value-%s", string(uuid.NewUUID())),
		Effect: effect,
	}
	e2enode.AddOrUpdateTaintOnNode(cs, nodeName, testTaint)
	framework.ExpectNodeHasTaint(cs, nodeName, &testTaint)
	return &testTaint
}
