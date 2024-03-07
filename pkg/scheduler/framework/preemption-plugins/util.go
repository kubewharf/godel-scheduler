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

package preemptionplugins

import (
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	appv1listers "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	policylisters "k8s.io/client-go/listers/policy/v1"
	schedulingv1listers "k8s.io/client-go/listers/scheduling/v1"
	storagelisters "k8s.io/client-go/listers/storage/v1"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

func GetPDBLister(informerFactory informers.SharedInformerFactory) policylisters.PodDisruptionBudgetLister {
	return informerFactory.Policy().V1().PodDisruptionBudgets().Lister()
}

func GetDeployLister(informerFactory informers.SharedInformerFactory) appv1listers.DeploymentLister {
	return informerFactory.Apps().V1().Deployments().Lister()
}

func GetPodDisruptionBudgets(pdbLister policylisters.PodDisruptionBudgetLister) ([]*policy.PodDisruptionBudget, error) {
	if pdbLister != nil {
		return pdbLister.List(labels.Everything())
	}
	return nil, nil
}

func GetPcLister(informerFactory informers.SharedInformerFactory) schedulingv1listers.PriorityClassLister {
	return informerFactory.Scheduling().V1().PriorityClasses().Lister()
}

func GetPodGroupLister(crdInformerFactory crdinformers.SharedInformerFactory) alpha1.PodGroupLister {
	return crdInformerFactory.Scheduling().V1alpha1().PodGroups().Lister()
}

func GetPgLister(crdInformerFactory crdinformers.SharedInformerFactory) alpha1.PodGroupLister {
	return crdInformerFactory.Scheduling().V1alpha1().PodGroups().Lister()
}

func GetPVCLister(informerFactory informers.SharedInformerFactory) corelisters.PersistentVolumeClaimLister {
	return informerFactory.Core().V1().PersistentVolumeClaims().Lister()
}

func GetSCLister(informerFactory informers.SharedInformerFactory) storagelisters.StorageClassLister {
	return informerFactory.Storage().V1().StorageClasses().Lister()
}

func GetPodLister(informerFactory informers.SharedInformerFactory) corelisters.PodLister {
	return informerFactory.Core().V1().Pods().Lister()
}

// PodEligibleToPreemptOthers determines whether this pod should be considered
// for preempting other pods or not. If this pod has already preempted other
// pods and those are in their graceful termination period, it shouldn't be
// considered for preemption.
// We look at the node that is nominated for this pod and as long as there are
// terminating pods on the node, we don't consider this for preempting more pods.
func PodEligibleToPreemptOthers(pod *v1.Pod, pcLister schedulingv1listers.PriorityClassLister) bool {
	// get preemption policy from pod spec
	if pod.Spec.PreemptionPolicy != nil {
		if *pod.Spec.PreemptionPolicy == v1.PreemptNever {
			return false
		}
		return true
	}

	// get preemption policy from pc if not set in pod spec
	if pod.Spec.PriorityClassName != "" {
		pc, err := pcLister.Get(pod.Spec.PriorityClassName)
		if err != nil {
			klog.ErrorS(err, "Failed to get priorityclass for pod", "priorityclass", pod.Spec.PriorityClassName, "pod", podutil.GetPodKey(pod))
			return false
		} else {
			if pc.PreemptionPolicy != nil {
				if *pc.PreemptionPolicy == v1.PreemptNever {
					return false
				}
				return true
			}
		}
	}

	// get preemption policy from pod annotation if not set in pod spec and pc
	// TODO: remove it
	if policy, ok := pod.Annotations[util.PreemptionPolicyKey]; ok {
		if policy == string(v1.PreemptNever) {
			return false
		}
		return true
	}

	// pods in preemption process shouldn't have nominatedNode field.
	return true
}
