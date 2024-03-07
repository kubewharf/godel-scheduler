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

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	"github.com/onsi/ginkgo"
	_ "github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/uuid"
	utilversion "k8s.io/apimachinery/pkg/util/version"
	clientset "k8s.io/client-go/kubernetes"
	k8utilnet "k8s.io/utils/net"

	"github.com/kubewharf/godel-scheduler/pkg/framework/config"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/nodeaffinity"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/framework/plugins/tainttoleration"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
	"github.com/kubewharf/godel-scheduler/test/e2e/framework"
	e2enode "github.com/kubewharf/godel-scheduler/test/e2e/framework/node"
	e2epod "github.com/kubewharf/godel-scheduler/test/e2e/framework/pod"
	e2erc "github.com/kubewharf/godel-scheduler/test/e2e/framework/rc"
	e2eskipper "github.com/kubewharf/godel-scheduler/test/e2e/framework/skipper"
	testutils "github.com/kubewharf/godel-scheduler/test/utils"
	imageutils "github.com/kubewharf/godel-scheduler/test/utils/image"
)

const (
	maxNumberOfPods int64 = 10
	defaultTimeout        = 3 * time.Minute
)

var localStorageVersion = utilversion.MustParseSemantic("v1.8.0-beta.0")

// variable populated in BeforeEach, never modified afterwards
var workerNodes = sets.String{}

type pausePodConfig struct {
	Name                              string
	Namespace                         string
	Affinity                          *v1.Affinity
	Annotations, Labels, NodeSelector map[string]string
	Resources                         *v1.ResourceRequirements
	RuntimeClassHandler               *string
	Tolerations                       []v1.Toleration
	NodeName                          string
	Ports                             []v1.ContainerPort
	OwnerReferences                   []metav1.OwnerReference
	PriorityClassName                 string
	DeletionGracePeriodSeconds        *int64
	TopologySpreadConstraints         []v1.TopologySpreadConstraint
}

var _ = SIGDescribe("SchedulingHardConstraints [Serial]", func() {
	var cs clientset.Interface
	var nodeList *v1.NodeList
	var RCName string
	var ns string
	f := framework.NewDefaultFramework("sched-hard-constraints")

	ginkgo.AfterEach(func() {
		rc, err := cs.CoreV1().ReplicationControllers(ns).Get(context.TODO(), RCName, metav1.GetOptions{})
		if err == nil && *(rc.Spec.Replicas) != 0 {
			ginkgo.By("Cleaning up the replication controller")
			err := e2erc.DeleteRCAndWaitForGC(f.ClientSet, ns, RCName)
			framework.ExpectNoError(err)
		}
	})

	ginkgo.BeforeEach(func() {
		cs = f.ClientSet
		ns = f.Namespace.Name
		nodeList = &v1.NodeList{}
		var err error

		framework.AllNodesReady(cs, time.Minute)

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

		for _, node := range nodeList.Items {
			framework.Logf("\nLogging pods the apiserver thinks is on node %v before test", node.Name)
			printAllPodsOnNode(cs, node.Name)
		}
	})

	// This test verifies pods without any resource requirements can be scheduled when there are available nodes
	/*
		Testname: Basic scheduling
		Description: Scheduling Pods MUST succeeds where there are available nodes.
	*/
	framework.ConformanceIt("validates pods that are allowed to run ", func() {
		WaitForStableCluster(cs, workerNodes)
		for _, node := range nodeList.Items {
			nodeReady := false
			for _, condition := range node.Status.Conditions {
				if condition.Type == v1.NodeReady && condition.Status == v1.ConditionTrue {
					nodeReady = true
					break
				}
			}
			if !nodeReady {
				continue
			}
		}

		_, err := cs.CoreV1().Pods(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
		framework.ExpectNoError(err)

		ginkgo.By("Starting Pod without requesting any resources")
		// Create an empty pod to be scheduled.
		podName := "filler-pod-" + string(uuid.NewUUID())
		conf := pausePodConfig{
			Name:        podName,
			Annotations: GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet),
		}
		// Wait for filler pods to schedule.
		WaitForSchedulerAfterAction(f, createPausePodAction(f, conf), ns, podName, true)
		framework.ExpectNoError(e2epod.WaitForPodNameRunningInNamespace(cs, podName, ns))
		verifyResult(cs, 1, 0, ns)
	})

	// This test verifies we don't allow scheduling of pods in a way that sum of local ephemeral storage resource requests of pods is greater than machines capacity.
	// It assumes that cluster add-on pods stay stable and cannot be run in parallel with any other test that touches Nodes or Pods.
	// It is so because we need to have precise control on what's running in the cluster.
	/*
		Testname: local ephemeral storage limits
		Description: Scheduling Pods MUST fail if the ephemeral storage resource is insufficient.
	*/
	ginkgo.It("validates local ephemeral storage resource limits of pods that are allowed to run [Feature:LocalStorageCapacityIsolation]", func() {
		e2eskipper.SkipUnlessServerVersionGTE(localStorageVersion, f.ClientSet.Discovery())

		nodeMaxAllocatable := int64(0)

		nodeToAllocatableMap := make(map[string]int64)
		for _, node := range nodeList.Items {
			allocatable, found := node.Status.Allocatable[v1.ResourceEphemeralStorage]
			framework.ExpectEqual(found, true)
			nodeToAllocatableMap[node.Name] = allocatable.Value()
			if nodeMaxAllocatable < allocatable.Value() {
				nodeMaxAllocatable = allocatable.Value()
			}
		}
		if nodeMaxAllocatable == 0 {
			framework.Logf("no suitable nodes found for ephemeral storage resources")
			return
		}

		WaitForStableCluster(cs, workerNodes)

		pods, err := cs.CoreV1().Pods(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
		framework.ExpectNoError(err)
		for _, pod := range pods.Items {
			_, found := nodeToAllocatableMap[pod.Spec.NodeName]
			if found && pod.Status.Phase != v1.PodSucceeded && pod.Status.Phase != v1.PodFailed {
				framework.Logf("Pod %v requesting local ephemeral resource =%v on Node %v", pod.Name, getRequestedStorageEphemeralStorage(pod), pod.Spec.NodeName)
				nodeToAllocatableMap[pod.Spec.NodeName] -= getRequestedStorageEphemeralStorage(pod)
			}
		}

		var podsNeededForSaturation int
		var ephemeralStoragePerPod int64

		ephemeralStoragePerPod = nodeMaxAllocatable / maxNumberOfPods

		framework.Logf("Using pod capacity: %v", ephemeralStoragePerPod)
		for name, leftAllocatable := range nodeToAllocatableMap {
			framework.Logf("Node: %v has local ephemeral resource allocatable: %v", name, leftAllocatable)
			podsNeededForSaturation += (int)(leftAllocatable / ephemeralStoragePerPod)
		}

		ginkgo.By(fmt.Sprintf("Starting additional %v Pods to fully saturate the cluster local ephemeral resource and trying to start another one", podsNeededForSaturation))

		// As the pods are distributed randomly among nodes,
		// it can easily happen that all nodes are saturated
		// and there is no need to create additional pods.
		// StartPods requires at least one pod to replicate.
		if podsNeededForSaturation > 0 {
			framework.ExpectNoError(testutils.StartPods(cs, podsNeededForSaturation, ns, "overcommit",
				*initPausePod(f, pausePodConfig{
					Name:        "",
					Annotations: GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet),
					Labels:      map[string]string{"name": ""},
					Resources: &v1.ResourceRequirements{
						Limits: v1.ResourceList{
							v1.ResourceEphemeralStorage: *resource.NewQuantity(ephemeralStoragePerPod, "DecimalSI"),
						},
						Requests: v1.ResourceList{
							v1.ResourceEphemeralStorage: *resource.NewQuantity(ephemeralStoragePerPod, "DecimalSI"),
						},
					},
				}), true, framework.Logf))
		}
		podName := "additional-pod"
		conf := pausePodConfig{
			Name:        podName,
			Annotations: GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet),
			Labels:      map[string]string{"name": "additional"},
			Resources: &v1.ResourceRequirements{
				Limits: v1.ResourceList{
					v1.ResourceEphemeralStorage: *resource.NewQuantity(ephemeralStoragePerPod, "DecimalSI"),
				},
				Requests: v1.ResourceList{
					v1.ResourceEphemeralStorage: *resource.NewQuantity(ephemeralStoragePerPod, "DecimalSI"),
				},
			},
		}
		WaitForSchedulerAfterAction(f, createPausePodAction(f, conf), ns, podName, false)
		verifyResult(cs, podsNeededForSaturation, 1, ns)
	})

	// This test verifies we don't allow scheduling of pods in a way that sum of limits +
	// associated overhead is greater than machine's capacity.
	// It assumes that cluster add-on pods stay stable and cannot be run in parallel
	// with any other test that touches Nodes or Pods.
	// Because of this we need to have precise control on what's running in the cluster.
	// Test scenario:
	// 1. Find the first ready node on the system, and add a fake resource for test
	// 2. Create one with affinity to the particular node that uses 70% of the fake resource.
	// 3. Wait for the pod to be scheduled.
	// 4. Create another pod with affinity to the particular node that needs 20% of the fake resource and
	//    an overhead set as 25% of the fake resource.
	// 5. Make sure this additional pod is not scheduled.
	/*
		Testname: resource overhead limits
		Description: Scheduling Pods MUST fail if the resource requests and associated overhead exceed Machine capacity.
	*/
	ginkgo.Context("validates pod overhead is considered along with resource limits of pods that are allowed to run", func() {
		var testNodeName string
		var handler string
		var beardsecond v1.ResourceName = "example.com/beardsecond"

		ginkgo.BeforeEach(func() {
			WaitForStableCluster(cs, workerNodes)
			ginkgo.By("Add RuntimeClass and fake resource")

			// find a node which can run a pod:
			testNodeName = GetNodeThatCanRunPod(f)

			// Get node object:
			node, err := cs.CoreV1().Nodes().Get(context.TODO(), testNodeName, metav1.GetOptions{})
			framework.ExpectNoError(err, "unable to get node object for node %v", testNodeName)

			// update Node API object with a fake resource
			nodeCopy := node.DeepCopy()
			nodeCopy.ResourceVersion = "0"

			nodeCopy.Status.Capacity[beardsecond] = resource.MustParse("1000")
			_, err = cs.CoreV1().Nodes().UpdateStatus(context.TODO(), nodeCopy, metav1.UpdateOptions{})
			framework.ExpectNoError(err, "unable to apply fake resource to %v", testNodeName)

			// Register a runtimeClass with overhead set as 25% of the available beard-seconds
			handler = e2enode.PreconfiguredRuntimeClassHandler(framework.TestContext.ContainerRuntime)

			rc := &nodev1.RuntimeClass{
				ObjectMeta: metav1.ObjectMeta{Name: handler},
				Handler:    handler,
				Overhead: &nodev1.Overhead{
					PodFixed: v1.ResourceList{
						beardsecond: resource.MustParse("250"),
					},
				},
			}
			_, err = cs.NodeV1().RuntimeClasses().Create(context.TODO(), rc, metav1.CreateOptions{})
			framework.ExpectNoError(err, "failed to create RuntimeClass resource")
		})

		ginkgo.AfterEach(func() {
			ginkgo.By("Remove fake resource and RuntimeClass")
			// remove fake resource:
			if testNodeName != "" {
				// Get node object:
				node, err := cs.CoreV1().Nodes().Get(context.TODO(), testNodeName, metav1.GetOptions{})
				framework.ExpectNoError(err, "unable to get node object for node %v", testNodeName)

				nodeCopy := node.DeepCopy()
				// force it to update
				nodeCopy.ResourceVersion = "0"
				delete(nodeCopy.Status.Capacity, beardsecond)
				_, err = cs.CoreV1().Nodes().UpdateStatus(context.TODO(), nodeCopy, metav1.UpdateOptions{})
				framework.ExpectNoError(err, "unable to update node %v", testNodeName)
			}

			// remove RuntimeClass
			cs.NodeV1().RuntimeClasses().Delete(context.TODO(), e2enode.PreconfiguredRuntimeClassHandler(framework.TestContext.ContainerRuntime), metav1.DeleteOptions{})
		})

		ginkgo.It("verify pod overhead is accounted for", func() {
			framework.ExpectEqual(testNodeName != "", true)

			ginkgo.By("Starting Pod to consume most of the node's resource.")

			// Create pod which requires 70% of the available beard-seconds.
			fillerPod := createPausePod(f, pausePodConfig{
				Name:        "filler-pod-" + string(uuid.NewUUID()),
				Annotations: GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet),
				Resources: &v1.ResourceRequirements{
					Requests: v1.ResourceList{beardsecond: resource.MustParse("700")},
					Limits:   v1.ResourceList{beardsecond: resource.MustParse("700")},
				},
			})

			// Wait for filler pod to schedule.
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, fillerPod))

			ginkgo.By("Creating another pod that requires unavailable amount of resources.")
			// Create another pod that requires 20% of available beard-seconds, but utilizes the RuntimeClass
			// which defines a pod overhead that requires an additional 25%.
			// This pod should remain pending as at least 70% of beard-second in
			// the node are already consumed.
			podName := "additional-pod" + string(uuid.NewUUID())
			conf := pausePodConfig{
				RuntimeClassHandler: &handler,
				Name:                podName,
				Annotations:         GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet),
				Labels:              map[string]string{"name": "additional"},
				Resources: &v1.ResourceRequirements{
					Limits: v1.ResourceList{beardsecond: resource.MustParse("200")},
				},
			}

			WaitForSchedulerAfterAction(f, createPausePodAction(f, conf), ns, podName, false)
			verifyResult(cs, 1, 1, ns)
		})
	})

	// This test verifies we don't allow scheduling of pods in a way that sum of
	// resource requests of pods is greater than machines capacity.
	// It assumes that cluster add-on pods stay stable and cannot be run in parallel
	// with any other test that touches Nodes or Pods.
	// It is so because we need to have precise control on what's running in the cluster.
	// Test scenario:
	// 1. Find the amount CPU resources on each node.
	// 2. Create one pod with affinity to each node that uses 70% of the node CPU.
	// 3. Wait for the pods to be scheduled.
	// 4. Create another pod with no affinity to any node that need 50% of the largest node CPU.
	// 5. Make sure this additional pod is not scheduled.
	/*
		Testname: Scheduler, resource limits
		Description: Scheduling Pods MUST fail if the resource requests exceed Machine capacity.
	*/
	framework.ConformanceIt("validates resource limits of pods that are allowed to run ", func() {
		WaitForStableCluster(cs, workerNodes)
		nodeMaxAllocatable := int64(0)
		nodeToAllocatableMap := make(map[string]int64)
		for _, node := range nodeList.Items {
			nodeReady := false
			for _, condition := range node.Status.Conditions {
				if condition.Type == v1.NodeReady && condition.Status == v1.ConditionTrue {
					nodeReady = true
					break
				}
			}
			if !nodeReady {
				continue
			}
			// Apply node label to each node
			framework.AddOrUpdateLabelOnNode(cs, node.Name, "node", node.Name)
			framework.ExpectNodeHasLabel(cs, node.Name, "node", node.Name)
			// Find allocatable amount of CPU.
			allocatable, found := node.Status.Allocatable[v1.ResourceCPU]
			framework.ExpectEqual(found, true)
			nodeToAllocatableMap[node.Name] = allocatable.MilliValue()
			if nodeMaxAllocatable < allocatable.MilliValue() {
				nodeMaxAllocatable = allocatable.MilliValue()
			}
		}
		// Clean up added labels after this test.
		defer func() {
			for nodeName := range nodeToAllocatableMap {
				framework.RemoveLabelOffNode(cs, nodeName, "node")
			}
		}()

		pods, err := cs.CoreV1().Pods(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
		framework.ExpectNoError(err)
		for _, pod := range pods.Items {
			_, found := nodeToAllocatableMap[pod.Spec.NodeName]
			if found && pod.Status.Phase != v1.PodSucceeded && pod.Status.Phase != v1.PodFailed {
				framework.Logf("Pod %v requesting resource cpu=%vm on Node %v", pod.Name, getRequestedCPU(pod), pod.Spec.NodeName)
				nodeToAllocatableMap[pod.Spec.NodeName] -= getRequestedCPU(pod)
			}
		}

		ginkgo.By("Starting Pods to consume most of the cluster CPU.")
		// Create one pod per node that requires 70% of the node remaining CPU.
		fillerPods := []*v1.Pod{}
		for nodeName, cpu := range nodeToAllocatableMap {
			requestedCPU := cpu * 7 / 10
			framework.Logf("Creating a pod which consumes cpu=%vm on Node %v", requestedCPU, nodeName)
			fillerPods = append(fillerPods, createPausePod(f, pausePodConfig{
				Name:        "filler-pod-" + string(uuid.NewUUID()),
				Annotations: WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{nodeaffinity.Name}),
				Resources: &v1.ResourceRequirements{
					Limits: v1.ResourceList{
						v1.ResourceCPU: *resource.NewMilliQuantity(requestedCPU, "DecimalSI"),
					},
					Requests: v1.ResourceList{
						v1.ResourceCPU: *resource.NewMilliQuantity(requestedCPU, "DecimalSI"),
					},
				},
				Affinity: &v1.Affinity{
					NodeAffinity: &v1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
							NodeSelectorTerms: []v1.NodeSelectorTerm{
								{
									MatchExpressions: []v1.NodeSelectorRequirement{
										{
											Key:      "node",
											Operator: v1.NodeSelectorOpIn,
											Values:   []string{nodeName},
										},
									},
								},
							},
						},
					},
				},
			}))
		}
		// Wait for filler pods to schedule.
		for _, pod := range fillerPods {
			framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(cs, pod))
		}
		ginkgo.By("Creating another pod that requires unavailable amount of CPU.")
		// Create another pod that requires 50% of the largest node CPU resources.
		// This pod should remain pending as at least 70% of CPU of other nodes in
		// the cluster are already consumed.
		podName := "additional-pod"
		conf := pausePodConfig{
			Name:        podName,
			Annotations: GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet),
			Labels:      map[string]string{"name": "additional"},
			Resources: &v1.ResourceRequirements{
				Limits: v1.ResourceList{
					v1.ResourceCPU: *resource.NewMilliQuantity(nodeMaxAllocatable*5/10, "DecimalSI"),
				},
				Requests: v1.ResourceList{
					v1.ResourceCPU: *resource.NewMilliQuantity(nodeMaxAllocatable*5/10, "DecimalSI"),
				},
			},
		}
		WaitForSchedulerAfterAction(f, createPausePodAction(f, conf), ns, podName, false)
		verifyResult(cs, len(fillerPods), 1, ns)
	})

	// Test scenario:
	// 1. Find 2 nodes to run pods, add same extra resource and different node label to the nodes.
	// 2. Create one pod with affinity to the same node that uses 70% of the extra resource.
	// 3. Wait for the pods to be scheduled.
	// 4. Create another pod with affinity to the same node that needs 70% of the extra resource.
	// 5. Make sure this additional pod is not scheduled.
	// 6. Create the third pod with affinity to the other node that needs 70% of the extra resource.
	// 7. Make sure the third pod is scheduled.
	/*
		Testname: Scheduling pods with node affinity matching
		Description: Scheduling Pods MUST fail if no resource meets the specified pod
	*/
	ginkgo.Context("scheduling pods to nodes matching node affinity[NodeAffinity]", func() {
		var testNodeNames []string
		testLabelKey := "godel.bytedance.com/test-label"
		testLabelValues := []string{"test", "foo"}
		var beardsecond v1.ResourceName = "example.com/beardsecond"

		ginkgo.BeforeEach(func() {
			WaitForStableCluster(cs, workerNodes)
			ginkgo.By("Add fake label")

			// find a node which can run a pod:
			testNodeNames = Get2NodesThatCanRunPod(f)

			// Get node object:
			for index, testNodeName := range testNodeNames {
				node, err := cs.CoreV1().Nodes().Get(context.TODO(), testNodeName, metav1.GetOptions{})
				framework.ExpectNoError(err, "unable to get node object for node %v", testNodeName)

				nodeCopy := node.DeepCopy()
				nodeCopy.ResourceVersion = "0"

				nodeCopy.Status.Capacity[beardsecond] = resource.MustParse("1000")
				_, err = cs.CoreV1().Nodes().UpdateStatus(context.TODO(), nodeCopy, metav1.UpdateOptions{})
				framework.ExpectNoError(err, "unable to apply fake resource to %v", testNodeName)

				// update Node API object with a fake node label
				framework.AddOrUpdateLabelOnNode(cs, node.Name, testLabelKey, testLabelValues[index])
				framework.ExpectNodeHasLabel(cs, node.Name, testLabelKey, testLabelValues[index])
			}
		})

		ginkgo.AfterEach(func() {
			ginkgo.By("Remove fake label")
			for _, testNodeName := range testNodeNames {
				// remove fake resource:
				if testNodeName != "" {
					// Get node object:
					node, err := cs.CoreV1().Nodes().Get(context.TODO(), testNodeName, metav1.GetOptions{})
					framework.ExpectNoError(err, "unable to get node object for node %v", testNodeName)

					framework.RemoveLabelOffNode(cs, node.Name, testLabelKey)
					framework.ExpectNoError(err, "unable to update node %v", testNodeName)

					nodeCopy := node.DeepCopy()
					// force it to update
					nodeCopy.ResourceVersion = "0"
					delete(nodeCopy.Status.Capacity, beardsecond)
					_, err = cs.CoreV1().Nodes().UpdateStatus(context.TODO(), nodeCopy, metav1.UpdateOptions{})
					framework.ExpectNoError(err, "unable to update node %v", testNodeName)
				}
			}
		})

		ginkgo.It("verify pod affinity matches", func() {
			fillerPods := make([]pausePodConfig, 2)
			for index, testNodeName := range testNodeNames {
				framework.ExpectEqual(testNodeName != "", true)

				fillerPods[index] = pausePodConfig{
					Name:        "filler-pod-" + string(uuid.NewUUID()),
					Annotations: WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{nodeaffinity.Name}),
					Affinity: &v1.Affinity{
						NodeAffinity: &v1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
								NodeSelectorTerms: []v1.NodeSelectorTerm{
									{
										MatchExpressions: []v1.NodeSelectorRequirement{
											{
												Key:      testLabelKey,
												Operator: v1.NodeSelectorOpIn,
												Values:   []string{testLabelValues[0]},
											},
										},
									},
								},
							},
						},
					},
					Resources: &v1.ResourceRequirements{
						Requests: v1.ResourceList{beardsecond: resource.MustParse("700")},
						Limits:   v1.ResourceList{beardsecond: resource.MustParse("700")},
					},
				}
			}
			// Wait for filler pod to schedule.
			WaitForSchedulerAfterAction(f, createPausePodAction(f, fillerPods[0]), ns, fillerPods[0].Name, true)
			framework.ExpectNoError(e2epod.WaitTimeoutForPodRunningInNamespace(f.ClientSet, fillerPods[0].Name, ns, framework.PollShortTimeout))
			framework.ExpectEqual(GetPod(f, ns, fillerPods[0].Name).Spec.NodeName, testNodeNames[0])
			WaitForSchedulerAfterAction(f, createPausePodAction(f, fillerPods[1]), ns, fillerPods[1].Name, false)
			verifyResult(cs, 1, 1, ns)

			thirdPod := pausePodConfig{
				Name:        "filler-pod-" + string(uuid.NewUUID()),
				Annotations: WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{nodeaffinity.Name}),
				Affinity: &v1.Affinity{
					NodeAffinity: &v1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
							NodeSelectorTerms: []v1.NodeSelectorTerm{
								{
									MatchExpressions: []v1.NodeSelectorRequirement{
										{
											Key:      testLabelKey,
											Operator: v1.NodeSelectorOpIn,
											Values:   []string{testLabelValues[1]},
										},
									},
								},
							},
						},
					},
				},
				Resources: &v1.ResourceRequirements{
					Requests: v1.ResourceList{beardsecond: resource.MustParse("700")},
					Limits:   v1.ResourceList{beardsecond: resource.MustParse("700")},
				},
			}
			framework.ExpectEqual(runPodAndGetNodeName(f, thirdPod), testNodeNames[1])
		})
	})

	/*
		Testname: Scheduling pods with node affinity not matching
		Description: Verify pods cannot be scheduled if node affinities do not match
	*/
	ginkgo.It("validates that NodeAffinity is respected if not matching", func() {
		ginkgo.By("Trying to schedule Pod with nonempty NodeSelector.")
		podName := "restricted-pod"

		WaitForStableCluster(cs, workerNodes)

		key, value := GenerateLabelThatMatchNoNode(nodeList.Items)
		conf := pausePodConfig{
			Name:        podName,
			Annotations: WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{nodeaffinity.Name}),
			Affinity: &v1.Affinity{
				NodeAffinity: &v1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
						NodeSelectorTerms: []v1.NodeSelectorTerm{
							{
								MatchExpressions: []v1.NodeSelectorRequirement{
									{
										Key:      key,
										Operator: v1.NodeSelectorOpIn,
										Values:   []string{value},
									},
								},
							},
						},
					},
				},
			},
			Labels: map[string]string{"name": "restricted"},
		}
		WaitForSchedulerAfterAction(f, createPausePodAction(f, conf), ns, podName, false)
		verifyResult(cs, 0, 1, ns)
	})

	// Test scenario:
	// 1. Find 2 nodes to run pods, add same extra resource and different node label to the nodes.
	// 2. Create one pod with node selector to the same node that uses 70% of the extra resource.
	// 3. Wait for the pods to be scheduled.
	// 4. Create another pod with node selector to the same node that needs 70% of the extra resource.
	// 5. Make sure this additional pod is not scheduled.
	// 6. Create the third pod with node selector to the other node that needs 70% of the extra resource.
	// 7. Make sure the third pod is scheduled.
	/*
		Testname: Scheduling pods with node selector matching
		Description: Scheduling Pods MUST fail if no resource meets the specified pod
	*/
	ginkgo.Context("validates pod scheduling fits node selector[NodeSelector]", func() {
		var beardsecond v1.ResourceName = "example.com/beardsecond"
		var nodeNames []string
		nodeSelectors := []map[string]string{
			{}, {},
		}
		keys := make([]string, 2)
		values := make([]string, 2)

		ginkgo.BeforeEach(func() {
			WaitForStableCluster(cs, workerNodes)
			ginkgo.By("cluster is stable")
			nodeNames = Get2NodesThatCanRunPod(f)

			ginkgo.By("Add fake label")
			// Get node object:
			for index, testNodeName := range nodeNames {
				node, err := cs.CoreV1().Nodes().Get(context.TODO(), testNodeName, metav1.GetOptions{})
				framework.ExpectNoError(err, "unable to get node object for node %v", testNodeName)

				nodeCopy := node.DeepCopy()
				nodeCopy.ResourceVersion = "0"

				nodeCopy.Status.Capacity[beardsecond] = resource.MustParse("1000")
				_, err = cs.CoreV1().Nodes().UpdateStatus(context.TODO(), nodeCopy, metav1.UpdateOptions{})
				framework.ExpectNoError(err, "unable to apply fake resource to %v", testNodeName)

				// use nodeSelector to make sure the testing pods get assigned on the same node to explicitly verify there exists conflict or not
				ginkgo.By("Trying to apply a random label on the found node.")
				keys[index] = fmt.Sprintf("kubernetes.io/e2e%v-%s", index, string(uuid.NewUUID()))
				values[index] = string(uuid.NewUUID())
				nodeSelectors[index][keys[index]] = values[index]
				// update Node API object with a fake node label
				framework.AddOrUpdateLabelOnNode(cs, nodeNames[index], keys[index], values[index])
				framework.ExpectNodeHasLabel(cs, nodeNames[index], keys[index], values[index])
			}
		})

		ginkgo.AfterEach(func() {
			ginkgo.By("Remove fake label")
			for index, testNodeName := range nodeNames {
				// remove fake resource:
				if testNodeName != "" {
					// Get node object:
					node, err := cs.CoreV1().Nodes().Get(context.TODO(), testNodeName, metav1.GetOptions{})
					framework.ExpectNoError(err, "unable to get node object for node %v", testNodeName)

					framework.RemoveLabelOffNode(cs, testNodeName, keys[index])
					framework.ExpectNoError(err, "unable to update node %v", testNodeName)

					nodeCopy := node.DeepCopy()
					// force it to update
					nodeCopy.ResourceVersion = "0"
					delete(nodeCopy.Status.Capacity, beardsecond)
					_, err = cs.CoreV1().Nodes().UpdateStatus(context.TODO(), nodeCopy, metav1.UpdateOptions{})
					framework.ExpectNoError(err, "unable to update node %v", testNodeName)
				}
			}
		})

		ginkgo.It("verify pod affinity matches node labels", func() {
			conf := pausePodConfig{
				Name:         "filler-pod-" + string(uuid.NewUUID()),
				Annotations:  WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{nodeaffinity.Name}),
				NodeSelector: nodeSelectors[0],
				Resources: &v1.ResourceRequirements{
					Requests: v1.ResourceList{beardsecond: resource.MustParse("700")},
					Limits:   v1.ResourceList{beardsecond: resource.MustParse("700")},
				},
			}
			WaitForSchedulerAfterAction(f, createPausePodAction(f, conf), ns, conf.Name, true)
			framework.ExpectNoError(e2epod.WaitTimeoutForPodRunningInNamespace(f.ClientSet, conf.Name, ns, framework.PollShortTimeout))
			framework.ExpectEqual(GetPod(f, ns, conf.Name).Spec.NodeName, nodeNames[0])

			waitConf := pausePodConfig{
				Name:         "filler-pod-" + string(uuid.NewUUID()),
				Annotations:  WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{nodeaffinity.Name}),
				NodeSelector: nodeSelectors[0],
				Resources: &v1.ResourceRequirements{
					Requests: v1.ResourceList{beardsecond: resource.MustParse("700")},
					Limits:   v1.ResourceList{beardsecond: resource.MustParse("700")},
				},
			}
			// Wait for filler pod to schedule.
			WaitForSchedulerAfterAction(f, createPausePodAction(f, waitConf), ns, waitConf.Name, false)
			verifyResult(cs, 1, 1, ns)

			thirdPod := pausePodConfig{
				Name:         "filler-pod-" + string(uuid.NewUUID()),
				Annotations:  WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{nodeaffinity.Name}),
				NodeSelector: nodeSelectors[1],
				Resources: &v1.ResourceRequirements{
					Requests: v1.ResourceList{beardsecond: resource.MustParse("700")},
					Limits:   v1.ResourceList{beardsecond: resource.MustParse("700")},
				},
			}
			framework.ExpectEqual(runPodAndGetNodeName(f, thirdPod), nodeNames[1])
		})
	})

	/*
		Testname: Scheduling pods with node selector not matching
		Description: Create a Pod with a NodeSelector set to a value that does not match a node in the cluster. Since there are no nodes matching the criteria the Pod MUST not be scheduled.
	*/
	framework.ConformanceIt("validates that NodeSelector is respected if not matching ", func() {
		WaitForStableCluster(cs, workerNodes)

		// Generate a pair of random key and value which dose not match any node in the cluster
		k, v := GenerateLabelThatMatchNoNode(nodeList.Items)
		ginkgo.By(fmt.Sprintf("Trying to schedule Pod with NodeSelector {%s: %s}", k, v))
		podName := "restricted-pod"
		conf := pausePodConfig{
			Name:        podName,
			Annotations: WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{nodeaffinity.Name}),
			Labels:      map[string]string{"name": "restricted"},
			NodeSelector: map[string]string{
				k: v,
			},
		}

		WaitForSchedulerAfterAction(f, createPausePodAction(f, conf), ns, podName, false)
		verifyResult(cs, 0, 1, ns)
	})

	// TODO InterPodAffinity plugin is needed
	/*
		Testname: Scheduling pods with inter-pod affinity matching
		Description: TODO
	*/
	//framework.ConformanceIt("", func() {
	//})

	// TODO InterPodAffinity plugin is needed
	/*
		Testname: Scheduling pods with inter-pod affinity not matching
		Description: TODO
	*/
	//framework.ConformanceIt("", func() {
	//})

	// Test scenario:
	// 1. Run a pod to get an available node, then delete the pod
	// 2. Taint the node with a random taint
	// 3. Try to relaunch the pod with tolerations tolerate the taints on node,
	//    and the pod's nodeName specified to the name of node found in step 1
	/*
		Testname: Scheduling pods with node taint tolerance matching
		Description: Pod with taint tolerations MUST be able to scheduled to the node with taint if taint tolerations are
		matching
	*/
	framework.ConformanceIt("validates that taints-tolerations is respected if matching", func() {
		nodeName := getNodeThatCanRunPodWithoutToleration(f)

		ginkgo.By("Trying to apply a random taint on the found node.")
		testTaint := addRandomTaintToNode(cs, nodeName, v1.TaintEffectNoSchedule)
		defer e2enode.RemoveTaintOffNode(cs, nodeName, *testTaint)

		ginkgo.By("Trying to apply a random label on the found node.")
		labelKey := fmt.Sprintf("kubernetes.io/e2e-label-key-%s", string(uuid.NewUUID()))
		labelValue := "testing-label-value"
		framework.AddOrUpdateLabelOnNode(cs, nodeName, labelKey, labelValue)
		framework.ExpectNodeHasLabel(cs, nodeName, labelKey, labelValue)
		defer framework.RemoveLabelOffNode(cs, nodeName, labelKey)

		ginkgo.By("Trying to relaunch the pod, now with tolerations.")
		tolerationPodName := "with-tolerations"
		createPausePod(f, pausePodConfig{
			Name:         tolerationPodName,
			Annotations:  WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{tainttoleration.Name, nodeaffinity.Name}),
			Tolerations:  []v1.Toleration{{Key: testTaint.Key, Value: testTaint.Value, Effect: testTaint.Effect}},
			NodeSelector: map[string]string{labelKey: labelValue},
		})

		// check that pod got scheduled. We intentionally DO NOT check that the
		// pod is running because this will create a race condition with the
		// kubelet and the scheduler: the scheduler might have scheduled a pod
		// already when the kubelet does not know about its new taint yet. The
		// kubelet will then refuse to launch the pod.
		framework.ExpectNoError(e2epod.WaitForPodNotPending(cs, ns, tolerationPodName))
		deployedPod, err := cs.CoreV1().Pods(ns).Get(context.TODO(), tolerationPodName, metav1.GetOptions{})
		framework.ExpectNoError(err)
		framework.ExpectEqual(deployedPod.Spec.NodeName, nodeName)
	})

	// Test scenario:
	// 1. Run a pod to get an available node, then delete the pod
	// 2. Taint the node with a random taint
	// 3. Try to relaunch the pod still no tolerations,
	// 	  and the pod's nodeName specified to the name of node found in step 1
	/*
		Testname: Scheduling pods with node taint tolerance not matching
		Description: Pod without taint tolerations MUST NOT be able to scheduled to the node with taint
	*/
	framework.ConformanceIt("validates that taints-tolerations is respected if not matching", func() {
		nodeName := getNodeThatCanRunPodWithoutToleration(f)

		ginkgo.By("Trying to apply a random taint on the found node.")
		testTaint := addRandomTaintToNode(cs, nodeName, v1.TaintEffectNoSchedule)
		framework.ExpectNodeHasTaint(cs, nodeName, testTaint)
		defer e2enode.RemoveTaintOffNode(cs, nodeName, *testTaint)

		ginkgo.By("Trying to apply a random label on the found node.")
		labelKey := fmt.Sprintf("kubernetes.io/e2e-label-key-%s", string(uuid.NewUUID()))
		labelValue := "testing-label-value"
		framework.AddOrUpdateLabelOnNode(cs, nodeName, labelKey, labelValue)
		framework.ExpectNodeHasLabel(cs, nodeName, labelKey, labelValue)
		defer framework.RemoveLabelOffNode(cs, nodeName, labelKey)

		ginkgo.By("Trying to relaunch the pod, still no tolerations.")
		podNameNoTolerations := "still-no-tolerations"
		conf := pausePodConfig{
			Name:         podNameNoTolerations,
			Annotations:  WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{tainttoleration.Name, nodeaffinity.Name}),
			NodeSelector: map[string]string{labelKey: labelValue},
		}

		WaitForSchedulerAfterAction(f, createPausePodAction(f, conf), ns, podNameNoTolerations, false)
		verifyResult(cs, 0, 1, ns)

		ginkgo.By("Removing taint off the node")
		WaitForSchedulerAfterAction(f, removeTaintFromNodeAction(cs, nodeName, *testTaint), ns, podNameNoTolerations, true)
		verifyResult(cs, 1, 0, ns)
	})

	/*
		Testname: Scheduling, HostPort matching and HostIP and Protocol not-matching
		Description: Pods with the same HostPort value MUST be able to be scheduled to the same node
		if the HostIP or Protocol is different.
	*/
	framework.ConformanceIt("validates that there is no conflict between pods with same hostPort but different hostIP and protocol", func() {
		nodeName := GetNodeThatCanRunPod(f)

		// use nodeSelector to make sure the testing pods get assigned on the same node to explicitly verify there exists conflict or not
		ginkgo.By("Trying to apply a random label on the found node.")
		k := fmt.Sprintf("kubernetes.io/e2e-%s", string(uuid.NewUUID()))
		v := "90"

		nodeSelector := make(map[string]string)
		nodeSelector[k] = v

		framework.AddOrUpdateLabelOnNode(cs, nodeName, k, v)
		framework.ExpectNodeHasLabel(cs, nodeName, k, v)
		defer framework.RemoveLabelOffNode(cs, nodeName, k)

		port := int32(54321)
		ginkgo.By(fmt.Sprintf("Trying to create a pod(pod1) with hostport %v and hostIP 127.0.0.1 and expect scheduled", port))
		createHostPortPodOnNode(f, "pod1", ns, "127.0.0.1", port, v1.ProtocolTCP, nodeSelector, true)

		ginkgo.By(fmt.Sprintf("Trying to create another pod(pod2) with hostport %v but hostIP 127.0.0.2 on the node which pod1 resides and expect scheduled", port))
		createHostPortPodOnNode(f, "pod2", ns, "127.0.0.2", port, v1.ProtocolTCP, nodeSelector, true)

		ginkgo.By(fmt.Sprintf("Trying to create a third pod(pod3) with hostport %v, hostIP 127.0.0.2 but use UDP protocol on the node which pod2 resides", port))
		createHostPortPodOnNode(f, "pod3", ns, "127.0.0.2", port, v1.ProtocolUDP, nodeSelector, true)
	})

	/*
		Testname: Scheduling, HostPort and Protocol match, HostIPs different but one is default HostIP (0.0.0.0)
		Description: Pods with the same HostPort and Protocol, but different HostIPs, MUST NOT schedule to the
		same node if one of those IPs is the default HostIP of 0.0.0.0, which represents all IPs on the host.
	*/
	framework.ConformanceIt("validates that there exists conflict between pods with same hostPort and protocol but one using 0.0.0.0 hostIP", func() {
		nodeName := GetNodeThatCanRunPod(f)

		// use nodeSelector to make sure the testing pods get assigned on the same node to explicitly verify there exists conflict or not
		ginkgo.By("Trying to apply a random label on the found node.")
		k := fmt.Sprintf("kubernetes.io/e2e-%s", string(uuid.NewUUID()))
		v := "95"

		nodeSelector := make(map[string]string)
		nodeSelector[k] = v

		framework.AddOrUpdateLabelOnNode(cs, nodeName, k, v)
		framework.ExpectNodeHasLabel(cs, nodeName, k, v)
		defer framework.RemoveLabelOffNode(cs, nodeName, k)

		port := int32(54322)
		ginkgo.By(fmt.Sprintf("Trying to create a pod(pod4) with hostport %v and hostIP 0.0.0.0(empty string here) and expect scheduled", port))
		createHostPortPodOnNode(f, "pod4", ns, "", port, v1.ProtocolTCP, nodeSelector, true)

		ginkgo.By(fmt.Sprintf("Trying to create another pod(pod5) with hostport %v but hostIP 127.0.0.1 on the node which pod4 resides and expect not scheduled", port))
		createPausePod(f, pausePodConfig{
			Name:        "pod5",
			Annotations: WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{nodeaffinity.Name}),
			Ports: []v1.ContainerPort{
				{
					HostPort:      port,
					ContainerPort: 80,
					Protocol:      v1.ProtocolTCP,
					HostIP:        translateIPv4ToIPv6("127.0.0.1"),
				},
			},
			NodeSelector: nodeSelector,
		})
		time.Sleep(waitTime)
		framework.ExpectNoError(e2epod.WaitTimeoutForPodUnschedulableInNamespace(f.ClientSet, "pod5", ns, time.Minute))
	})

	/*
		Testname: Scheduling, unschedulable node
		Description: Pod MUST NOT be scheduled to unschedulable node
	*/
	framework.ConformanceIt("validates that pod can't be scheduled to unschedulable node", func() {
		nodeName := GetNodeThatCanRunPod(f)
		err := setNodeUnschedulable(cs, nodeName, true)
		framework.ExpectNoError(err, "unable to set node: %v unschedulable", nodeName)

		labelKey := fmt.Sprintf("kubernetes.io/e2e-label-key-%s", string(uuid.NewUUID()))
		labelValue := "testing-label-value"
		framework.AddOrUpdateLabelOnNode(cs, nodeName, labelKey, labelValue)
		framework.ExpectNodeHasLabel(cs, nodeName, labelKey, labelValue)

		ginkgo.By("Trying to relaunch a pod whose node selector matches an unschedulable node.")
		podNameToUnschedulableNode := "to-unschedulable-node"
		conf := pausePodConfig{
			Name:         podNameToUnschedulableNode,
			Annotations:  WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{nodeaffinity.Name}),
			NodeSelector: map[string]string{labelKey: labelValue},
		}

		WaitForSchedulerAfterAction(f, createPausePodAction(f, conf), ns, podNameToUnschedulableNode, false)
		verifyResult(cs, 0, 1, ns)

		ginkgo.By("Set node unschedulable as false")
		WaitForSchedulerAfterAction(f, setNodeUnschedulableAction(cs, nodeName, false), ns, podNameToUnschedulableNode, true)
		verifyResult(cs, 1, 0, ns)
	})

	// TODO PodTopologySpread plugin is needed
	/*
		Testname: Scheduling pods with pod topology spread constraints
		Description: TODO
	*/
	//framework.ConformanceIt("", func() {
	//})
})

// printAllPodsOnNode outputs status of all kubelet pods into log.
func printAllPodsOnNode(c clientset.Interface, nodeName string) {
	podList, err := c.CoreV1().Pods(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{FieldSelector: "spec.nodeName=" + nodeName})
	if err != nil {
		framework.Logf("Unable to retrieve pods for node %v: %v", nodeName, err)
		return
	}
	for _, p := range podList.Items {
		framework.Logf("%v from %v started at %v (%d container statuses recorded)", p.Name, p.Namespace, p.Status.StartTime, len(p.Status.ContainerStatuses))
		for _, c := range p.Status.ContainerStatuses {
			framework.Logf("\tContainer %v ready: %v, restart count %v",
				c.Name, c.Ready, c.RestartCount)
		}
	}
}

func initPausePod(f *framework.Framework, conf pausePodConfig) *v1.Pod {
	gracePeriod := int64(1)
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            conf.Name,
			Namespace:       conf.Namespace,
			Labels:          conf.Labels,
			Annotations:     conf.Annotations,
			OwnerReferences: conf.OwnerReferences,
		},
		Spec: v1.PodSpec{
			NodeSelector:              conf.NodeSelector,
			Affinity:                  conf.Affinity,
			TopologySpreadConstraints: conf.TopologySpreadConstraints,
			RuntimeClassName:          conf.RuntimeClassHandler,
			Containers: []v1.Container{
				{
					Name:  conf.Name,
					Image: imageutils.GetPauseImageName(),
					Ports: conf.Ports,
				},
			},
			Tolerations:                   conf.Tolerations,
			PriorityClassName:             conf.PriorityClassName,
			TerminationGracePeriodSeconds: &gracePeriod,
			SchedulerName:                 config.DefaultSchedulerName,
		},
	}

	// TODO: setting the Pod's nodeAffinity instead of setting .spec.nodeName works around the
	// Preemption e2e flake (#88441), but we should investigate deeper to get to the bottom of it.
	if len(conf.NodeName) != 0 {
		e2epod.SetNodeAffinity(&pod.Spec, conf.NodeName)
	}
	if conf.Resources != nil {
		pod.Spec.Containers[0].Resources = *conf.Resources
	}
	if conf.DeletionGracePeriodSeconds != nil {
		pod.ObjectMeta.DeletionGracePeriodSeconds = conf.DeletionGracePeriodSeconds
	}
	return pod
}

func createPausePod(f *framework.Framework, conf pausePodConfig) *v1.Pod {
	namespace := conf.Namespace
	if len(namespace) == 0 {
		namespace = f.Namespace.Name
	}
	pod, err := f.ClientSet.CoreV1().Pods(namespace).Create(context.TODO(), initPausePod(f, conf), metav1.CreateOptions{})
	framework.ExpectNoError(err)
	return pod
}

func createPodGroup(f *framework.Framework, name, namespace string, minMember int32) *schedulingv1a1.PodGroup {
	podGroup := &schedulingv1a1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: schedulingv1a1.PodGroupSpec{
			MinMember: minMember,
		},
	}

	podGroup, err := f.Godelclient.SchedulingV1alpha1().PodGroups(namespace).Create(context.TODO(), podGroup, metav1.CreateOptions{})
	framework.ExpectNoError(err)
	return podGroup
}

func createPodGroupTimeout(f *framework.Framework, name, namespace string, minMember int32, timeout int32) *schedulingv1a1.PodGroup {
	podGroup := &schedulingv1a1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: schedulingv1a1.PodGroupSpec{
			MinMember:              minMember,
			ScheduleTimeoutSeconds: &timeout,
		},
	}

	podGroup, err := f.Godelclient.SchedulingV1alpha1().PodGroups(namespace).Create(context.TODO(), podGroup, metav1.CreateOptions{})
	framework.ExpectNoError(err)
	return podGroup
}

func runPausePod(f *framework.Framework, conf pausePodConfig) *v1.Pod {
	pod := createPausePod(f, conf)
	framework.ExpectNoError(e2epod.WaitTimeoutForPodRunningInNamespace(f.ClientSet, pod.Name, pod.Namespace, framework.PollShortTimeout))
	return GetPod(f, pod.Namespace, conf.Name)
}

func runPodAndGetNodeName(f *framework.Framework, conf pausePodConfig) string {
	// launch a pod to find a node which can launch a pod. We intentionally do
	// not just take the node list and choose the first of them. Depending on the
	// cluster and the scheduler it might be that a "normal" pod cannot be
	// scheduled onto it.
	pod := runPausePod(f, conf)

	ginkgo.By("Explicitly delete pod here to free the resource it takes.")
	err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), pod.Name, *metav1.NewDeleteOptions(0))
	framework.ExpectNoError(err)

	return pod.Spec.NodeName
}

func getRequestedCPU(pod v1.Pod) int64 {
	var result int64
	for _, container := range pod.Spec.Containers {
		result += container.Resources.Requests.Cpu().MilliValue()
	}
	return result
}

func getRequestedStorageEphemeralStorage(pod v1.Pod) int64 {
	var result int64
	for _, container := range pod.Spec.Containers {
		result += container.Resources.Requests.StorageEphemeral().Value()
	}
	return result
}

// removeTaintFromNodeAction returns a closure that removes the given taint
// from the given node upon invocation.
func removeTaintFromNodeAction(cs clientset.Interface, nodeName string, testTaint v1.Taint) Action {
	return func() error {
		e2enode.RemoveTaintOffNode(cs, nodeName, testTaint)
		return nil
	}
}

// createPausePodAction returns a closure that creates a pause pod upon invocation.
func createPausePodAction(f *framework.Framework, conf pausePodConfig) Action {
	return func() error {
		namespace := conf.Namespace
		if len(namespace) == 0 {
			namespace = f.Namespace.Name
		}
		_, err := f.ClientSet.CoreV1().Pods(namespace).Create(context.TODO(), initPausePod(f, conf), metav1.CreateOptions{})
		return err
	}
}

// WaitForSchedulerAfterAction performs the provided action and then waits for
// scheduler to act on the given pod.
func WaitForSchedulerAfterAction(f *framework.Framework, action Action, ns, podName string, expectSuccess bool) {
	predicate := scheduleFailureEvent(podName)
	if expectSuccess {
		predicate = scheduleSuccessEvent(ns, podName, "" /* any node */)
	}
	success, err := observeEventAfterAction(f.ClientSet, f.Namespace.Name, predicate, action)
	framework.ExpectNoError(err)
	framework.ExpectEqual(success, true)
}

// TODO: upgrade calls in PodAffinity tests when we're able to run them
func verifyResult(c clientset.Interface, expectedScheduled int, expectedNotScheduled int, ns string) {
	allPods, err := c.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{})
	framework.ExpectNoError(err)
	scheduledPods, notScheduledPods := GetPodsScheduled(workerNodes, allPods)

	framework.ExpectEqual(len(notScheduledPods), expectedNotScheduled, fmt.Sprintf("Not scheduled Pods: %#v", notScheduledPods))
	framework.ExpectEqual(len(scheduledPods), expectedScheduled, fmt.Sprintf("Scheduled Pods: %#v", scheduledPods))
}

// GetNodeThatCanRunPod trying to launch a pod without a label to get a node which can launch it
func GetNodeThatCanRunPod(f *framework.Framework) string {
	ginkgo.By("Trying to launch a pod without a label to get a node which can launch it.")
	return runPodAndGetNodeName(f, pausePodConfig{
		Name:        "without-label",
		Annotations: GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet),
	})
}

// Get2NodesThatCanRunPod return a 2-node slice where can run pod.
func Get2NodesThatCanRunPod(f *framework.Framework) []string {
	firstNode := GetNodeThatCanRunPod(f)
	ginkgo.By("Trying to launch a pod without a label to get a node which can launch it.")
	pod := pausePodConfig{
		Name:        "without-label",
		Annotations: WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{nodeaffinity.Name}),
		Affinity: &v1.Affinity{
			NodeAffinity: &v1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
					NodeSelectorTerms: []v1.NodeSelectorTerm{
						{
							MatchFields: []v1.NodeSelectorRequirement{
								{Key: "metadata.name", Operator: v1.NodeSelectorOpNotIn, Values: []string{firstNode}},
							},
						},
					},
				},
			},
		},
	}
	secondNode := runPodAndGetNodeName(f, pod)
	return []string{firstNode, secondNode}
}

func getNodeThatCanRunPodWithoutToleration(f *framework.Framework) string {
	ginkgo.By("Trying to launch a pod without a toleration to get a node which can launch it.")
	return runPodAndGetNodeName(f, pausePodConfig{
		Name:        "without-toleration",
		Annotations: WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{tainttoleration.Name}),
	})
}

// CreateHostPortPods creates RC with host port 4321
func CreateHostPortPods(f *framework.Framework, id string, replicas int, expectRunning bool) {
	ginkgo.By(fmt.Sprintf("Running RC which reserves host port"))
	config := &testutils.RCConfig{
		Client:    f.ClientSet,
		Name:      id,
		Namespace: f.Namespace.Name,
		Timeout:   defaultTimeout,
		Image:     imageutils.GetPauseImageName(),
		Replicas:  replicas,
		HostPorts: map[string]int{"port1": 4321},
	}
	err := e2erc.RunRC(*config)
	if expectRunning {
		framework.ExpectNoError(err)
	}
}

// CreateNodeSelectorPods creates RC with host port 4321 and defines node selector
func CreateNodeSelectorPods(f *framework.Framework, id string, replicas int, nodeSelector map[string]string, expectRunning bool) error {
	ginkgo.By(fmt.Sprintf("Running RC which reserves host port and defines node selector"))

	config := &testutils.RCConfig{
		Client:       f.ClientSet,
		Name:         id,
		Namespace:    f.Namespace.Name,
		Timeout:      defaultTimeout,
		Image:        imageutils.GetPauseImageName(),
		Replicas:     replicas,
		HostPorts:    map[string]int{"port1": 4321},
		NodeSelector: nodeSelector,
	}
	err := e2erc.RunRC(*config)
	if expectRunning {
		return err
	}
	return nil
}

// create pod which using hostport on the specified node according to the nodeSelector
func createHostPortPodOnNode(f *framework.Framework, podName, ns, hostIP string, port int32, protocol v1.Protocol, nodeSelector map[string]string, expectScheduled bool) {
	hostIP = translateIPv4ToIPv6(hostIP)
	createPausePod(f, pausePodConfig{
		Name:        podName,
		Annotations: WithHardConstraints(GetPodAnnotations(podutil.GuaranteedPod, podutil.Kubelet), []string{nodeaffinity.Name}),
		Ports: []v1.ContainerPort{
			{
				HostPort:      port,
				ContainerPort: 80,
				Protocol:      protocol,
				HostIP:        hostIP,
			},
		},
		NodeSelector: nodeSelector,
	})

	err := e2epod.WaitForPodNotPending(f.ClientSet, ns, podName)
	if expectScheduled {
		framework.ExpectNoError(err)
	}
}

// translateIPv4ToIPv6 maps an IPv4 address into a valid IPv6 address
// adding the well known prefix "0::ffff:" https://tools.ietf.org/html/rfc2765
// if the ip is IPv4 and the cluster IPFamily is IPv6, otherwise returns the same ip
func translateIPv4ToIPv6(ip string) string {
	if framework.TestContext.IPFamily == "ipv6" && ip != "" && !k8utilnet.IsIPv6String(ip) {
		ip = "0::ffff:" + ip
	}
	return ip
}

// GetPodsScheduled returns a number of currently scheduled and not scheduled Pods on worker nodes.
func GetPodsScheduled(workerNodes sets.String, pods *v1.PodList) (scheduledPods, notScheduledPods []v1.Pod) {
	for _, pod := range pods.Items {
		if pod.Spec.NodeName != "" && workerNodes.Has(pod.Spec.NodeName) {
			_, scheduledCondition := podutil.GetPodCondition(&pod.Status, v1.PodScheduled)
			framework.ExpectEqual(scheduledCondition != nil, true)
			if scheduledCondition != nil {
				framework.ExpectEqual(scheduledCondition.Status, v1.ConditionTrue)
				scheduledPods = append(scheduledPods, pod)
			}
		} else if pod.Spec.NodeName == "" {
			notScheduledPods = append(notScheduledPods, pod)
		}
	}
	return
}

func GetPod(f *framework.Framework, namespace, name string) *v1.Pod {
	pod, err := f.ClientSet.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	framework.ExpectNoError(err)
	return pod
}

func setNodeUnschedulable(cs clientset.Interface, nodeName string, unschedulabel bool) error {
	node, err := cs.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if node.Spec.Unschedulable == unschedulabel {
		return nil
	}
	node.Spec.Unschedulable = unschedulabel
	node.ResourceVersion = "0"
	_, err = cs.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})
	return err
}

func setNodeUnschedulableAction(cs clientset.Interface, nodeName string, unschedulabel bool) Action {
	return func() error {
		return setNodeUnschedulable(cs, nodeName, unschedulabel)
	}
}
