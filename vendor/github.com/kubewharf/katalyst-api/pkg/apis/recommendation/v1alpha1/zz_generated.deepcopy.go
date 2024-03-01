//go:build !ignore_autogenerated
// +build !ignore_autogenerated

/*
Copyright 2022 The Katalyst Authors.

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

// Code generated by deepcopy-gen. DO NOT EDIT.

package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AlgorithmPolicy) DeepCopyInto(out *AlgorithmPolicy) {
	*out = *in
	if in.Extensions != nil {
		in, out := &in.Extensions, &out.Extensions
		*out = new(runtime.RawExtension)
		(*in).DeepCopyInto(*out)
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AlgorithmPolicy.
func (in *AlgorithmPolicy) DeepCopy() *AlgorithmPolicy {
	if in == nil {
		return nil
	}
	out := new(AlgorithmPolicy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ContainerControlledResourcesPolicy) DeepCopyInto(out *ContainerControlledResourcesPolicy) {
	*out = *in
	if in.MinAllowed != nil {
		in, out := &in.MinAllowed, &out.MinAllowed
		x := (*in).DeepCopy()
		*out = &x
	}
	if in.MaxAllowed != nil {
		in, out := &in.MaxAllowed, &out.MaxAllowed
		x := (*in).DeepCopy()
		*out = &x
	}
	if in.BufferPercent != nil {
		in, out := &in.BufferPercent, &out.BufferPercent
		*out = new(int32)
		**out = **in
	}
	if in.ControlledValues != nil {
		in, out := &in.ControlledValues, &out.ControlledValues
		*out = new(ContainerControlledValues)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ContainerControlledResourcesPolicy.
func (in *ContainerControlledResourcesPolicy) DeepCopy() *ContainerControlledResourcesPolicy {
	if in == nil {
		return nil
	}
	out := new(ContainerControlledResourcesPolicy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ContainerResourceList) DeepCopyInto(out *ContainerResourceList) {
	*out = *in
	if in.Current != nil {
		in, out := &in.Current, &out.Current
		*out = make(v1.ResourceList, len(*in))
		for key, val := range *in {
			(*out)[key] = val.DeepCopy()
		}
	}
	if in.Target != nil {
		in, out := &in.Target, &out.Target
		*out = make(v1.ResourceList, len(*in))
		for key, val := range *in {
			(*out)[key] = val.DeepCopy()
		}
	}
	if in.UncappedTarget != nil {
		in, out := &in.UncappedTarget, &out.UncappedTarget
		*out = make(v1.ResourceList, len(*in))
		for key, val := range *in {
			(*out)[key] = val.DeepCopy()
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ContainerResourceList.
func (in *ContainerResourceList) DeepCopy() *ContainerResourceList {
	if in == nil {
		return nil
	}
	out := new(ContainerResourceList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ContainerResourcePolicy) DeepCopyInto(out *ContainerResourcePolicy) {
	*out = *in
	if in.ControlledResourcesPolicies != nil {
		in, out := &in.ControlledResourcesPolicies, &out.ControlledResourcesPolicies
		*out = make([]ContainerControlledResourcesPolicy, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ContainerResourcePolicy.
func (in *ContainerResourcePolicy) DeepCopy() *ContainerResourcePolicy {
	if in == nil {
		return nil
	}
	out := new(ContainerResourcePolicy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ContainerResources) DeepCopyInto(out *ContainerResources) {
	*out = *in
	if in.Requests != nil {
		in, out := &in.Requests, &out.Requests
		*out = new(ContainerResourceList)
		(*in).DeepCopyInto(*out)
	}
	if in.Limits != nil {
		in, out := &in.Limits, &out.Limits
		*out = new(ContainerResourceList)
		(*in).DeepCopyInto(*out)
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ContainerResources.
func (in *ContainerResources) DeepCopy() *ContainerResources {
	if in == nil {
		return nil
	}
	out := new(ContainerResources)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CrossVersionObjectReference) DeepCopyInto(out *CrossVersionObjectReference) {
	*out = *in
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CrossVersionObjectReference.
func (in *CrossVersionObjectReference) DeepCopy() *CrossVersionObjectReference {
	if in == nil {
		return nil
	}
	out := new(CrossVersionObjectReference)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PodResources) DeepCopyInto(out *PodResources) {
	*out = *in
	if in.ContainerRecommendations != nil {
		in, out := &in.ContainerRecommendations, &out.ContainerRecommendations
		*out = make([]ContainerResources, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PodResources.
func (in *PodResources) DeepCopy() *PodResources {
	if in == nil {
		return nil
	}
	out := new(PodResources)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RecommendResources) DeepCopyInto(out *RecommendResources) {
	*out = *in
	if in.PodRecommendations != nil {
		in, out := &in.PodRecommendations, &out.PodRecommendations
		*out = make([]PodResources, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.ContainerRecommendations != nil {
		in, out := &in.ContainerRecommendations, &out.ContainerRecommendations
		*out = make([]ContainerResources, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RecommendResources.
func (in *RecommendResources) DeepCopy() *RecommendResources {
	if in == nil {
		return nil
	}
	out := new(RecommendResources)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourcePolicy) DeepCopyInto(out *ResourcePolicy) {
	*out = *in
	in.AlgorithmPolicy.DeepCopyInto(&out.AlgorithmPolicy)
	if in.ContainerPolicies != nil {
		in, out := &in.ContainerPolicies, &out.ContainerPolicies
		*out = make([]ContainerResourcePolicy, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourcePolicy.
func (in *ResourcePolicy) DeepCopy() *ResourcePolicy {
	if in == nil {
		return nil
	}
	out := new(ResourcePolicy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceRecommend) DeepCopyInto(out *ResourceRecommend) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceRecommend.
func (in *ResourceRecommend) DeepCopy() *ResourceRecommend {
	if in == nil {
		return nil
	}
	out := new(ResourceRecommend)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ResourceRecommend) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceRecommendCondition) DeepCopyInto(out *ResourceRecommendCondition) {
	*out = *in
	in.LastTransitionTime.DeepCopyInto(&out.LastTransitionTime)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceRecommendCondition.
func (in *ResourceRecommendCondition) DeepCopy() *ResourceRecommendCondition {
	if in == nil {
		return nil
	}
	out := new(ResourceRecommendCondition)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceRecommendList) DeepCopyInto(out *ResourceRecommendList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ResourceRecommend, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceRecommendList.
func (in *ResourceRecommendList) DeepCopy() *ResourceRecommendList {
	if in == nil {
		return nil
	}
	out := new(ResourceRecommendList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ResourceRecommendList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceRecommendSpec) DeepCopyInto(out *ResourceRecommendSpec) {
	*out = *in
	out.TargetRef = in.TargetRef
	in.ResourcePolicy.DeepCopyInto(&out.ResourcePolicy)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceRecommendSpec.
func (in *ResourceRecommendSpec) DeepCopy() *ResourceRecommendSpec {
	if in == nil {
		return nil
	}
	out := new(ResourceRecommendSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceRecommendStatus) DeepCopyInto(out *ResourceRecommendStatus) {
	*out = *in
	if in.LastRecommendationTime != nil {
		in, out := &in.LastRecommendationTime, &out.LastRecommendationTime
		*out = (*in).DeepCopy()
	}
	if in.RecommendResources != nil {
		in, out := &in.RecommendResources, &out.RecommendResources
		*out = new(RecommendResources)
		(*in).DeepCopyInto(*out)
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]ResourceRecommendCondition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceRecommendStatus.
func (in *ResourceRecommendStatus) DeepCopy() *ResourceRecommendStatus {
	if in == nil {
		return nil
	}
	out := new(ResourceRecommendStatus)
	in.DeepCopyInto(out)
	return out
}