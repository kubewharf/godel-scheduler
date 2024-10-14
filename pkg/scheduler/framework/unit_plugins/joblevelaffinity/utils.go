package joblevelaffinity

import (
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// We precheck the allocatable resource in a topology domain to avoid uncessary scheduling process.
// We only precheck the most common resources instead of all.
type preCheckResource struct {
	MilliCPU int64
	Memory   int64
	GPU      int64
}

const maxInt64 = int64(((1 << 63) - 1))

func (r *preCheckResource) add(resource map[string]*resource.Quantity) {
	if q := resource[v1.ResourceCPU.String()]; q != nil {
		r.MilliCPU += q.MilliValue()
	}
	if q := resource[v1.ResourceMemory.String()]; q != nil {
		r.Memory += q.Value()
	}
	if q := resource[util.ResourceGPU.String()]; q != nil {
		r.GPU += q.Value()
	}
}

func (r *preCheckResource) getMinWith(resource map[string]*resource.Quantity) {
	if q := resource[v1.ResourceCPU.String()]; q != nil {

		r.MilliCPU = min(r.MilliCPU, q.MilliValue())
	}
	if q := resource[v1.ResourceMemory.String()]; q != nil {
		r.Memory = min(r.Memory, q.Value())
	}
	if q := resource[util.ResourceGPU.String()]; q != nil {
		r.GPU = min(r.GPU, q.Value())
	}
}

func (r *preCheckResource) multipliedBy(b int64) {
	r.MilliCPU = r.MilliCPU * b
	r.Memory = r.Memory * b
	r.GPU = r.GPU * b
}

func (r *preCheckResource) greater(s *framework.Resource) bool {
	if r == nil {
		return false
	}
	return r.MilliCPU > s.MilliCPU || r.Memory > s.Memory || r.GPU > s.ScalarResources[util.ResourceGPU]
}

// If the unit is `everScheduled`, the minimum request quantity for all pods is considered as the `minRequest` of the unit.
// Otherwise, we compute the `minRequest` accordingly based on whether min < all is met.
func computeUnitMinResourceRequest(unit framework.ScheduleUnit, everScheduled bool) (*preCheckResource, error) {
	if everScheduled {
		minRequest := preCheckResource{
			MilliCPU: maxInt64,
			Memory:   maxInt64,
			GPU:      maxInt64,
		}
		for _, podInfo := range unit.GetPods() {
			if podInfo == nil || podInfo.Pod == nil {
				continue
			}
			podRequests := podutil.GetPodRequests(podInfo.Pod)
			minRequest.getMinWith(podRequests)
		}
		if minRequest.MilliCPU == maxInt64 {
			minRequest.MilliCPU = 0
		}
		if minRequest.Memory == maxInt64 {
			minRequest.Memory = 0
		}
		if minRequest.GPU == maxInt64 {
			minRequest.GPU = 0
		}
		return &minRequest, nil
	}

	minRequest := preCheckResource{}
	minMember, err := unit.GetMinMember()
	if err != nil {
		return nil, err
	}
	allMember := unit.NumPods()

	if minMember == allMember {
		for _, podInfo := range unit.GetPods() {
			if podInfo == nil || podInfo.Pod == nil {
				continue
			}
			podRequests := podutil.GetPodRequests(podInfo.Pod)
			minRequest.add(podRequests)
		}
		return &minRequest, nil
	}

	// TODO: For now, the pods in the unit are considered to be the same when min < all. If different pods are allowed
	// in the future, the code below should be revisited.
	podInfo := unit.GetPods()[0]
	podRequests := podutil.GetPodRequests(podInfo.Pod)
	minRequest.add(podRequests)
	minRequest.multipliedBy(int64(minMember))
	return &minRequest, nil
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func getTopologyElems(nodeCircles []framework.NodeCircle) []*topologyElem {
	topologyElems := make([]*topologyElem, len(nodeCircles))
	for i := range nodeCircles {
		newElem := &topologyElem{
			nodeCircle: nodeCircles[i],
		}
		topologyElems[i] = newElem
	}
	return topologyElems
}
