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

package testing_helper

import (
	"fmt"
	"time"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	scheduling "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

var zero int64

// NodeSelectorWrapper wraps a NodeSelector inside.
type NodeSelectorWrapper struct{ v1.NodeSelector }

// MakeNodeSelector creates a NodeSelector wrapper.
func MakeNodeSelector() *NodeSelectorWrapper {
	return &NodeSelectorWrapper{v1.NodeSelector{}}
}

// In injects a matchExpression (with an operator IN) as a selectorTerm
// to the inner nodeSelector.
// NOTE: appended selecterTerms are ORed.
func (s *NodeSelectorWrapper) In(key string, vals []string) *NodeSelectorWrapper {
	expression := v1.NodeSelectorRequirement{
		Key:      key,
		Operator: v1.NodeSelectorOpIn,
		Values:   vals,
	}
	selectorTerm := v1.NodeSelectorTerm{}
	selectorTerm.MatchExpressions = append(selectorTerm.MatchExpressions, expression)
	s.NodeSelectorTerms = append(s.NodeSelectorTerms, selectorTerm)
	return s
}

// NotIn injects a matchExpression (with an operator NotIn) as a selectorTerm
// to the inner nodeSelector.
func (s *NodeSelectorWrapper) NotIn(key string, vals []string) *NodeSelectorWrapper {
	expression := v1.NodeSelectorRequirement{
		Key:      key,
		Operator: v1.NodeSelectorOpNotIn,
		Values:   vals,
	}
	selectorTerm := v1.NodeSelectorTerm{}
	selectorTerm.MatchExpressions = append(selectorTerm.MatchExpressions, expression)
	s.NodeSelectorTerms = append(s.NodeSelectorTerms, selectorTerm)
	return s
}

// Obj returns the inner NodeSelector.
func (s *NodeSelectorWrapper) Obj() *v1.NodeSelector {
	return &s.NodeSelector
}

// LabelSelectorWrapper wraps a LabelSelector inside.
type LabelSelectorWrapper struct{ metav1.LabelSelector }

// MakeLabelSelector creates a LabelSelector wrapper.
func MakeLabelSelector() *LabelSelectorWrapper {
	return &LabelSelectorWrapper{metav1.LabelSelector{}}
}

// Label applies a {k,v} pair to the inner LabelSelector.
func (s *LabelSelectorWrapper) Label(k, v string) *LabelSelectorWrapper {
	if s.MatchLabels == nil {
		s.MatchLabels = make(map[string]string)
	}
	s.MatchLabels[k] = v
	return s
}

// In injects a matchExpression (with an operator In) to the inner labelSelector.
func (s *LabelSelectorWrapper) In(key string, vals []string) *LabelSelectorWrapper {
	expression := metav1.LabelSelectorRequirement{
		Key:      key,
		Operator: metav1.LabelSelectorOpIn,
		Values:   vals,
	}
	s.MatchExpressions = append(s.MatchExpressions, expression)
	return s
}

// NotIn injects a matchExpression (with an operator NotIn) to the inner labelSelector.
func (s *LabelSelectorWrapper) NotIn(key string, vals []string) *LabelSelectorWrapper {
	expression := metav1.LabelSelectorRequirement{
		Key:      key,
		Operator: metav1.LabelSelectorOpNotIn,
		Values:   vals,
	}
	s.MatchExpressions = append(s.MatchExpressions, expression)
	return s
}

// Exists injects a matchExpression (with an operator Exists) to the inner labelSelector.
func (s *LabelSelectorWrapper) Exists(k string) *LabelSelectorWrapper {
	expression := metav1.LabelSelectorRequirement{
		Key:      k,
		Operator: metav1.LabelSelectorOpExists,
	}
	s.MatchExpressions = append(s.MatchExpressions, expression)
	return s
}

// NotExist injects a matchExpression (with an operator NotExist) to the inner labelSelector.
func (s *LabelSelectorWrapper) NotExist(k string) *LabelSelectorWrapper {
	expression := metav1.LabelSelectorRequirement{
		Key:      k,
		Operator: metav1.LabelSelectorOpDoesNotExist,
	}
	s.MatchExpressions = append(s.MatchExpressions, expression)
	return s
}

// Obj returns the inner LabelSelector.
func (s *LabelSelectorWrapper) Obj() *metav1.LabelSelector {
	return &s.LabelSelector
}

// PodWrapper wraps a Pod inside.
type PodWrapper struct{ v1.Pod }

// MakePod creates a Pod wrapper.
func MakePod() *PodWrapper {
	return &PodWrapper{v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				podutil.PodLauncherAnnotationKey:     string(podutil.Kubelet),
				podutil.PodResourceTypeAnnotationKey: string(podutil.GuaranteedPod),
			},
		},
	}}
}

// Obj returns the inner Pod.
func (p *PodWrapper) Obj() *v1.Pod {
	return &p.Pod
}

// Name sets `s` as the name of the inner pod.
func (p *PodWrapper) Name(s string) *PodWrapper {
	p.SetName(s)
	return p
}

// UID sets `s` as the UID of the inner pod.
func (p *PodWrapper) UID(s string) *PodWrapper {
	p.SetUID(types.UID(s))
	return p
}

func (p *PodWrapper) SetCreationTimestampAt(creation time.Time) *PodWrapper {
	p.SetCreationTimestamp(metav1.NewTime(creation))
	return p
}

func (p *PodWrapper) ControllerRef(ref metav1.OwnerReference) *PodWrapper {
	p.OwnerReferences = append(p.OwnerReferences, ref)
	return p
}

func (p *PodWrapper) ResourceVersion(v string) *PodWrapper {
	p.ObjectMeta.ResourceVersion = v
	return p
}

// SchedulerName sets `s` as the scheduler name of the inner pod.
func (p *PodWrapper) SchedulerName(s string) *PodWrapper {
	p.Spec.SchedulerName = s
	return p
}

// Namespace sets `s` as the namespace of the inner pod.
func (p *PodWrapper) Namespace(s string) *PodWrapper {
	p.SetNamespace(s)
	return p
}

// Container appends a container into PodSpec of the inner pod.
func (p *PodWrapper) Container(s string) *PodWrapper {
	p.Spec.Containers = append(p.Spec.Containers, v1.Container{
		Name:  fmt.Sprintf("con%d", len(p.Spec.Containers)),
		Image: s,
	})
	return p
}

// Priority sets a priority value into PodSpec of the inner pod.
func (p *PodWrapper) Priority(val int32) *PodWrapper {
	p.Spec.Priority = &val
	return p
}

// Terminating sets the inner pod's deletionTimestamp to current timestamp.
func (p *PodWrapper) Terminating() *PodWrapper {
	now := metav1.Now()
	p.DeletionTimestamp = &now
	return p
}

// ZeroTerminationGracePeriod sets the TerminationGracePeriodSeconds of the inner pod to zero.
func (p *PodWrapper) ZeroTerminationGracePeriod() *PodWrapper {
	p.Spec.TerminationGracePeriodSeconds = &zero
	return p
}

// Node sets `s` as the nodeName of the inner pod.
func (p *PodWrapper) Node(s string) *PodWrapper {
	p.Spec.NodeName = s
	return p
}

// NodeSelector sets `m` as the nodeSelector of the inner pod.
func (p *PodWrapper) NodeSelector(m map[string]string) *PodWrapper {
	p.Spec.NodeSelector = m
	return p
}

type NodeAffinityKind int

const (
	NodeAffinityWithRequiredReq NodeAffinityKind = iota
	NodeAffinityWithPreferredReq
)

// NodeAffinityIn creates a HARD node affinity (with the operator In)
// and injects into the inner pod.
func (p *PodWrapper) NodeAffinityIn(key string, vals []string, kind NodeAffinityKind) *PodWrapper {
	if p.Spec.Affinity == nil {
		p.Spec.Affinity = &v1.Affinity{}
	}
	if p.Spec.Affinity.NodeAffinity == nil {
		p.Spec.Affinity.NodeAffinity = &v1.NodeAffinity{}
	}
	nodeSelector := MakeNodeSelector().In(key, vals).Obj()
	switch kind {
	case NodeAffinityWithRequiredReq:
		p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = nodeSelector
	case NodeAffinityWithPreferredReq:
		p.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []v1.PreferredSchedulingTerm{
			{
				Weight: 1,
				Preference: v1.NodeSelectorTerm{
					MatchExpressions: []v1.NodeSelectorRequirement{
						{
							Key:      key,
							Operator: v1.NodeSelectorOpIn,
							Values:   vals,
						},
					},
				},
			},
		}
	}

	return p
}

// NodeAffinityNotIn creates a HARD node affinity (with the operator NotIn)
// and injects into the inner pod.
func (p *PodWrapper) NodeAffinityNotIn(key string, vals []string, kind NodeAffinityKind) *PodWrapper {
	if p.Spec.Affinity == nil {
		p.Spec.Affinity = &v1.Affinity{}
	}
	if p.Spec.Affinity.NodeAffinity == nil {
		p.Spec.Affinity.NodeAffinity = &v1.NodeAffinity{}
	}
	nodeSelector := MakeNodeSelector().NotIn(key, vals).Obj()
	switch kind {
	case NodeAffinityWithRequiredReq:
		p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = nodeSelector
	case NodeAffinityWithPreferredReq:
		p.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []v1.PreferredSchedulingTerm{
			{
				Preference: v1.NodeSelectorTerm{
					MatchExpressions: []v1.NodeSelectorRequirement{
						{
							Key:      key,
							Operator: v1.NodeSelectorOpNotIn,
							Values:   vals,
						},
					},
				},
			},
		}
	}
	return p
}

// StartTime sets `t` as .status.startTime for the inner pod.
func (p *PodWrapper) StartTime(t metav1.Time) *PodWrapper {
	p.Status.StartTime = &t
	return p
}

// NominatedNodeName sets `n` as the .Status.NominatedNodeName of the inner pod.
func (p *PodWrapper) NominatedNodeName(n string) *PodWrapper {
	p.Status.NominatedNodeName = n
	return p
}

// PodAffinityKind represents different kinds of PodAffinity.
type PodAffinityKind int

const (
	// NilPodAffinity is a no-op which doesn't apply any PodAffinity.
	NilPodAffinity PodAffinityKind = iota
	// PodAffinityWithRequiredReq applies a HARD requirement to pod.spec.affinity.PodAffinity.
	PodAffinityWithRequiredReq
	// PodAffinityWithPreferredReq applies a SOFT requirement to pod.spec.affinity.PodAffinity.
	PodAffinityWithPreferredReq
	// PodAffinityWithRequiredPreferredReq applies HARD and SOFT requirements to pod.spec.affinity.PodAffinity.
	PodAffinityWithRequiredPreferredReq
	// PodAntiAffinityWithRequiredReq applies a HARD requirement to pod.spec.affinity.PodAntiAffinity.
	PodAntiAffinityWithRequiredReq
	// PodAntiAffinityWithPreferredReq applies a SOFT requirement to pod.spec.affinity.PodAntiAffinity.
	PodAntiAffinityWithPreferredReq
	// PodAntiAffinityWithRequiredPreferredReq applies HARD and SOFT requirements to pod.spec.affinity.PodAntiAffinity.
	PodAntiAffinityWithRequiredPreferredReq
)

// PodAffinityExists creates an PodAffinity with the operator "Exists"
// and injects into the inner pod.
func (p *PodWrapper) PodAffinityExists(labelKey, topologyKey string, kind PodAffinityKind) *PodWrapper {
	if kind == NilPodAffinity {
		return p
	}

	if p.Spec.Affinity == nil {
		p.Spec.Affinity = &v1.Affinity{}
	}
	if p.Spec.Affinity.PodAffinity == nil {
		p.Spec.Affinity.PodAffinity = &v1.PodAffinity{}
	}
	labelSelector := MakeLabelSelector().Exists(labelKey).Obj()
	term := v1.PodAffinityTerm{LabelSelector: labelSelector, TopologyKey: topologyKey}
	switch kind {
	case PodAffinityWithRequiredReq:
		p.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(
			p.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
			term,
		)
	case PodAffinityWithPreferredReq:
		p.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(
			p.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution,
			v1.WeightedPodAffinityTerm{Weight: 1, PodAffinityTerm: term},
		)
	case PodAffinityWithRequiredPreferredReq:
		p.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(
			p.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
			term,
		)
		p.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(
			p.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution,
			v1.WeightedPodAffinityTerm{Weight: 1, PodAffinityTerm: term},
		)
	}
	return p
}

// PodAntiAffinityExists creates an PodAntiAffinity with the operator "Exists"
// and injects into the inner pod.
func (p *PodWrapper) PodAntiAffinityExists(labelKey, topologyKey string, kind PodAffinityKind) *PodWrapper {
	if kind == NilPodAffinity {
		return p
	}

	if p.Spec.Affinity == nil {
		p.Spec.Affinity = &v1.Affinity{}
	}
	if p.Spec.Affinity.PodAntiAffinity == nil {
		p.Spec.Affinity.PodAntiAffinity = &v1.PodAntiAffinity{}
	}
	labelSelector := MakeLabelSelector().Exists(labelKey).Obj()
	term := v1.PodAffinityTerm{LabelSelector: labelSelector, TopologyKey: topologyKey}
	switch kind {
	case PodAntiAffinityWithRequiredReq:
		p.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(
			p.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
			term,
		)
	case PodAntiAffinityWithPreferredReq:
		p.Spec.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(
			p.Spec.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution,
			v1.WeightedPodAffinityTerm{Weight: 1, PodAffinityTerm: term},
		)
	case PodAntiAffinityWithRequiredPreferredReq:
		p.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(
			p.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
			term,
		)
		p.Spec.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(
			p.Spec.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution,
			v1.WeightedPodAffinityTerm{Weight: 1, PodAffinityTerm: term},
		)
	}
	return p
}

// SpreadConstraint constructs a TopologySpreadConstraint object and injects
// into the inner pod.
func (p *PodWrapper) SpreadConstraint(maxSkew int, tpKey string, mode v1.UnsatisfiableConstraintAction, selector *metav1.LabelSelector) *PodWrapper {
	c := v1.TopologySpreadConstraint{
		MaxSkew:           int32(maxSkew),
		TopologyKey:       tpKey,
		WhenUnsatisfiable: mode,
		LabelSelector:     selector,
	}
	p.Spec.TopologySpreadConstraints = append(p.Spec.TopologySpreadConstraints, c)
	return p
}

// Label sets a {k,v} pair to the inner pod.
func (p *PodWrapper) Label(k, v string) *PodWrapper {
	if p.Labels == nil {
		p.Labels = make(map[string]string)
	}
	p.Labels[k] = v
	return p
}

// Annotation sets a {k,v} pair to the inner pod.
func (p *PodWrapper) Annotation(k, v string) *PodWrapper {
	if p.Annotations == nil {
		p.Annotations = make(map[string]string)
	}
	p.Annotations[k] = v
	return p
}

// Req adds a new container to the inner pod with given resource map.
func (p *PodWrapper) Req(resMap map[v1.ResourceName]string) *PodWrapper {
	if len(resMap) == 0 {
		return p
	}

	res := v1.ResourceList{}
	for k, v := range resMap {
		res[k] = resource.MustParse(v)
	}
	p.Spec.Containers = append(p.Spec.Containers, v1.Container{
		Resources: v1.ResourceRequirements{
			Requests: res,
		},
	})
	return p
}

// PreemptionPolicy sets the give preemption policy to the inner pod.
func (p *PodWrapper) PreemptionPolicy(policy v1.PreemptionPolicy) *PodWrapper {
	p.Spec.PreemptionPolicy = &policy
	return p
}

// PriorityClassName sets 'n' as .Spec.PriorityClassName to the inner pod.
func (p *PodWrapper) PriorityClassName(n string) *PodWrapper {
	p.Spec.PriorityClassName = n
	return p
}

type NodeInfoWrapper struct {
	framework.NodeInfo
}

func MakeNodeInfo() *NodeInfoWrapper {
	nodeInfo := framework.NewNodeInfo()
	node := v1.Node{}
	nodeInfo.SetNode(&node)
	cnr := katalystv1alpha1.CustomNodeResource{}
	nodeInfo.SetCNR(&cnr)
	return &NodeInfoWrapper{nodeInfo}
}

func (n *NodeInfoWrapper) Obj() framework.NodeInfo {
	return n.NodeInfo
}

func (n *NodeInfoWrapper) Name(s string) *NodeInfoWrapper {
	n.NodeInfo.GetNode().SetName(s)
	n.NodeInfo.GetCNR().SetName(s)
	return n
}

// Label applies a {k,v} label pair to the inner node.
func (n *NodeInfoWrapper) Label(k, v string) *NodeInfoWrapper {
	node := n.GetNode()
	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}
	node.Labels[k] = v
	return n
}

// Capacity sets the capacity and the allocatable resources of the inner node.
// Each entry in `resources` corresponds to a resource name and its quantity.
// By default, the capacity and allocatable number of pods are set to 32.
func (n *NodeInfoWrapper) Capacity(resources map[v1.ResourceName]string) *NodeInfoWrapper {
	res := v1.ResourceList{
		v1.ResourcePods: resource.MustParse("32"),
	}
	for name, value := range resources {
		res[name] = resource.MustParse(value)
	}

	node := n.GetNode()
	node.Status.Capacity, node.Status.Allocatable = res, res
	n.SetNode(node)
	return n
}

func (n *NodeInfoWrapper) CNRCapacity(resources map[v1.ResourceName]string) *NodeInfoWrapper {
	res := v1.ResourceList{
		v1.ResourcePods: resource.MustParse("32"),
	}
	for name, value := range resources {
		res[name] = resource.MustParse(value)
	}
	cnr := n.GetCNR()
	cnr.Status.Resources.Capacity, cnr.Status.Resources.Allocatable = &res, &res
	n.SetCNR(cnr)
	return n
}

// NodeWrapper wraps a Node inside.
type NodeWrapper struct {
	v1.Node                             `json:"node"`
	katalystv1alpha1.CustomNodeResource `json:"cnr"`
}

// MakeNode creates a Node wrapper.
func MakeNode() *NodeWrapper {
	w := &NodeWrapper{v1.Node{}, katalystv1alpha1.CustomNodeResource{}}
	return w.Capacity(nil)
}

// Obj returns the inner Node.
func (n *NodeWrapper) Obj() *v1.Node {
	return &n.Node
}

func (n *NodeWrapper) CNRObj() *katalystv1alpha1.CustomNodeResource {
	return &n.CustomNodeResource
}

// Name sets `s` as the name of the inner pod.
func (n *NodeWrapper) Name(s string) *NodeWrapper {
	n.Node.SetName(s)
	n.CustomNodeResource.SetName(s)
	return n
}

// NodeUID sets `s` as the UID of the inner pod.
func (n *NodeWrapper) NodeUID(s string) *NodeWrapper {
	n.Node.SetUID(types.UID(s))
	return n
}

// Label applies a {k,v} label pair to the inner node.
func (n *NodeWrapper) Label(k, v string) *NodeWrapper {
	if n.Node.Labels == nil {
		n.Node.Labels = make(map[string]string)
	}
	n.Node.Labels[k] = v
	return n
}

// Capacity sets the capacity and the allocatable resources of the inner node.
// Each entry in `resources` corresponds to a resource name and its quantity.
// By default, the capacity and allocatable number of pods are set to 32.
func (n *NodeWrapper) Capacity(resources map[v1.ResourceName]string) *NodeWrapper {
	res := v1.ResourceList{
		v1.ResourcePods: resource.MustParse("32"),
	}
	for name, value := range resources {
		res[name] = resource.MustParse(value)
	}
	n.Node.Status.Capacity, n.Node.Status.Allocatable = res, res
	return n
}

func (n *NodeWrapper) CNRCapacity(resources map[v1.ResourceName]string) *NodeWrapper {
	res := v1.ResourceList{
		v1.ResourcePods: resource.MustParse("32"),
	}
	for name, value := range resources {
		res[name] = resource.MustParse(value)
	}
	n.CustomNodeResource.Status.Resources.Capacity, n.CustomNodeResource.Status.Resources.Allocatable = &res, &res
	return n
}

// Images sets the images of the inner node. Each entry in `images` corresponds
// to an image name and its size in bytes.
func (n *NodeWrapper) Images(images map[string]int64) *NodeWrapper {
	var containerImages []v1.ContainerImage
	for name, size := range images {
		containerImages = append(containerImages, v1.ContainerImage{Names: []string{name}, SizeBytes: size})
	}
	n.Node.Status.Images = containerImages
	return n
}

// PDBWrapper wrappers a pdb inside.
type PDBWrapper struct{ policy.PodDisruptionBudget }

// MakePdb creates a pdb wrapper.
func MakePdb() *PDBWrapper {
	return &PDBWrapper{policy.PodDisruptionBudget{}}
}

// Obj returns the inner pdb.
func (p *PDBWrapper) Obj() *policy.PodDisruptionBudget {
	return &p.PodDisruptionBudget
}

func (p *PDBWrapper) Namespace(namespace string) *PDBWrapper {
	p.SetNamespace(namespace)
	return p
}

func (p *PDBWrapper) Name(name string) *PDBWrapper {
	p.SetName(name)
	return p
}

// set 'selector' as .Spec.Selector of the inner pdb.
func (p *PDBWrapper) Selector(selector *metav1.LabelSelector) *PDBWrapper {
	p.Spec.Selector = selector
	return p
}

// add 'k,v' to the selector of the inner pdb.
func (p *PDBWrapper) Label(k, v string) *PDBWrapper {
	if p.Spec.Selector == nil {
		p.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{k: v}}
	} else {
		p.Spec.Selector.MatchLabels[k] = v
	}
	return p
}

// set 'a' as .Status.DisruptionsAllowed of the inner pdb obj.
func (p *PDBWrapper) DisruptionsAllowed(n int32) *PDBWrapper {
	p.Status.DisruptionsAllowed = n
	return p
}

// PriorityClassWrapper wrappers a PriorityClass inside.
type PriorityClassWrapper struct{ scheduling.PriorityClass }

// MakePriorityClass creates a PriorityClass wrapper.
func MakePriorityClass() *PriorityClassWrapper {
	return &PriorityClassWrapper{scheduling.PriorityClass{}}
}

// Obj returns the inner pdb.
func (p *PriorityClassWrapper) Obj() *scheduling.PriorityClass {
	return &p.PriorityClass
}

// set 'a' as .Name of the inner PriorityClass obj.
func (p *PriorityClassWrapper) Name(n string) *PriorityClassWrapper {
	p.SetName(n)
	return p
}

// set 'n' as .PriorityClass.Value of the inner PriorityClass obj.
func (p *PriorityClassWrapper) Value(n int32) *PriorityClassWrapper {
	p.PriorityClass.Value = n
	return p
}

func (p *PriorityClassWrapper) SetPreemptionPolicy(policy v1.PreemptionPolicy) *PriorityClassWrapper {
	p.PreemptionPolicy = &policy
	return p
}

func (p *PriorityClassWrapper) Annotation(k, v string) *PriorityClassWrapper {
	if p.Annotations == nil {
		p.Annotations = map[string]string{k: v}
	} else {
		p.Annotations[k] = v
	}
	return p
}

// NodeWrapper wraps a Node inside.
type DeploymentWrapper struct {
	appsv1.Deployment
}

// MakeDp creates a deploy wrapper.
func MakeDeploy() *DeploymentWrapper {
	return &DeploymentWrapper{appsv1.Deployment{}}
}

// Obj returns the inner deploy.
func (dp *DeploymentWrapper) Obj() *appsv1.Deployment {
	return &dp.Deployment
}

// set 'a' as .Name of the inner Deployment obj.
func (dp *DeploymentWrapper) Name(n string) *DeploymentWrapper {
	dp.SetName(n)
	return dp
}

func (dp *DeploymentWrapper) Namespace(n string) *DeploymentWrapper {
	dp.SetNamespace(n)
	return dp
}

func (dp *DeploymentWrapper) Annotation(key, value string) *DeploymentWrapper {
	annotation := dp.GetAnnotations()
	if annotation == nil {
		annotation = map[string]string{}
	}
	annotation[key] = value
	dp.SetAnnotations(annotation)
	return dp
}

// set n as .Spec.Replicas of the inner deploy obj.
func (dp *DeploymentWrapper) Replicas(n uint) *DeploymentWrapper {
	res := int32(n)
	dp.Deployment.Spec.Replicas = &res
	return dp
}

// PodGroupWrapper wraps a PodGroup inside.
type PodGroupWrapper struct {
	schedulingv1a1.PodGroup
}

// MakeDp creates a deploy wrapper.
func MakePodGroup() *PodGroupWrapper {
	return &PodGroupWrapper{schedulingv1a1.PodGroup{}}
}

// Obj returns the inner pod group.
func (pg *PodGroupWrapper) Obj() *schedulingv1a1.PodGroup {
	return &pg.PodGroup
}

// set 'a' as .Name of the inner PodGroup obj.
func (pg *PodGroupWrapper) Name(n string) *PodGroupWrapper {
	pg.SetName(n)
	return pg
}

func (pg *PodGroupWrapper) Namespace(n string) *PodGroupWrapper {
	pg.SetNamespace(n)
	return pg
}

// set n as .Spec.MinMember of the inner PodGroup obj.
func (pg *PodGroupWrapper) MinMember(n uint) *PodGroupWrapper {
	pg.Spec.MinMember = int32(n)
	return pg
}

// set n as .Spec.PriorityClassName of the inner PodGroup obj.
func (pg *PodGroupWrapper) ProrityClassName(n string) *PodGroupWrapper {
	pg.Spec.PriorityClassName = n
	return pg
}

// ReplicaSetWrapper wraps a ReplicaSet inside.
type ReplicaSetWrapper struct {
	appsv1.ReplicaSet
}

// MakeReplicaSet creates a replicaset wrapper.
func MakeReplicaSet() *ReplicaSetWrapper {
	return &ReplicaSetWrapper{appsv1.ReplicaSet{}}
}

// Obj returns the inner replicaset.
func (rs *ReplicaSetWrapper) Obj() *appsv1.ReplicaSet {
	return &rs.ReplicaSet
}

// set 'a' as .Name of the inner ReplicaSet obj.
func (rs *ReplicaSetWrapper) Name(n string) *ReplicaSetWrapper {
	rs.SetName(n)
	return rs
}

func (rs *ReplicaSetWrapper) Namespace(n string) *ReplicaSetWrapper {
	rs.SetNamespace(n)
	return rs
}

// Label applies a {k,v} label pair to the inner replicaset.
func (rs *ReplicaSetWrapper) Label(k, v string) *ReplicaSetWrapper {
	if rs.Labels == nil {
		rs.Labels = make(map[string]string)
	}
	rs.Labels[k] = v
	return rs
}
