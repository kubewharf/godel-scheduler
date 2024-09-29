package joblevelaffinity

import (
	"testing"

	"github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/util"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	bytesPerGi = 1024 * 1024 * 1024
)

func TestComputeUnitMinResourceRequest(t *testing.T) {
	podA1 := &framework.QueuedPodInfo{
		Pod: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				UID: types.UID("podA1"),
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("10"),
								v1.ResourceMemory: resource.MustParse("32Gi"),
							},
						},
					},
				},
			},
		},
	}
	podA2 := &framework.QueuedPodInfo{
		Pod: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				UID: types.UID("podA2"),
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("10"),
								v1.ResourceMemory: resource.MustParse("32Gi"),
							},
						},
					},
				},
			},
		},
	}
	podA3 := &framework.QueuedPodInfo{
		Pod: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				UID: types.UID("podA3"),
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("10"),
								v1.ResourceMemory: resource.MustParse("32Gi"),
							},
						},
					},
				},
			},
		},
	}
	podB1 := &framework.QueuedPodInfo{
		Pod: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				UID: types.UID("podB1"),
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("20"),
								v1.ResourceMemory: resource.MustParse("64Gi"),
								util.ResourceGPU:  resource.MustParse("20"),
							},
						},
					},
				},
			},
		},
	}
	podB2 := &framework.QueuedPodInfo{
		Pod: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				UID: types.UID("podB2"),
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("20"),
								v1.ResourceMemory: resource.MustParse("64Gi"),
								util.ResourceGPU:  resource.MustParse("20"),
							},
						},
					},
				},
			},
		},
	}
	podC1 := &framework.QueuedPodInfo{
		Pod: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				UID: types.UID("podC1"),
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("15"),
								v1.ResourceMemory: resource.MustParse("64Gi"),
								util.ResourceGPU:  resource.MustParse("30"),
							},
						},
					},
				},
			},
		},
	}

	testCases := []struct {
		name          string
		unit          framework.ScheduleUnit
		expected      preCheckResource
		everScheduled bool
	}{
		{
			name: "unit has one template, min = all, not scheduled",
			unit: makeUnit(3, podA1, podA2, podA3),
			expected: preCheckResource{
				MilliCPU: 30 * 1000,
				Memory:   96 * bytesPerGi,
				GPU:      0,
			},
			everScheduled: false,
		},
		{
			name: "unit has one template, min = all, scheduled",
			unit: makeUnit(3, podA1, podA2, podA3),
			expected: preCheckResource{
				MilliCPU: 10 * 1000,
				Memory:   32 * bytesPerGi,
				GPU:      0,
			},
			everScheduled: true,
		},
		{
			name: "unit has one template, min < all, not scheduled",
			unit: makeUnit(2, podA1, podA2, podA3),
			expected: preCheckResource{
				MilliCPU: 20 * 1000,
				Memory:   64 * bytesPerGi,
				GPU:      0,
			},
			everScheduled: false,
		},
		{
			name: "unit has one template, min < all, scheduled",
			unit: makeUnit(2, podA1, podA2, podA3),
			expected: preCheckResource{
				MilliCPU: 10 * 1000,
				Memory:   32 * bytesPerGi,
				GPU:      0,
			},
			everScheduled: true,
		},
		{
			name: "unit has multiple templates, min = all, not scheduled",
			unit: makeUnit(3, podB1, podB2, podC1),
			expected: preCheckResource{
				MilliCPU: 55 * 1000,
				Memory:   192 * bytesPerGi,
				GPU:      70,
			},
			everScheduled: false,
		},
		{
			name: "unit has multiple templates, min = all, scheduled",
			unit: makeUnit(2, podB1, podB2, podC1),
			expected: preCheckResource{
				MilliCPU: 15 * 1000,
				Memory:   64 * bytesPerGi,
				GPU:      20,
			},
			everScheduled: true,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := computeUnitMinResourceRequest(tt.unit, tt.everScheduled)
			if err != nil {
				t.Errorf("unexpected error %v", err)
			}
			if got.MilliCPU != tt.expected.MilliCPU || got.Memory != tt.expected.Memory || got.GPU != tt.expected.GPU {
				t.Errorf("expected preCheckResource: %#v, got %#v", tt.expected, got)
			}
		})
	}
}

func makeUnit(min int32, pods ...*framework.QueuedPodInfo) *framework.PodGroupUnit {
	pg := &v1alpha1.PodGroup{
		Spec: v1alpha1.PodGroupSpec{
			Affinity: &v1alpha1.Affinity{
				PodGroupAffinity: &v1alpha1.PodGroupAffinity{},
			},
			MinMember: min,
		},
	}
	unit := framework.NewPodGroupUnit(pg, 0)
	for _, pod := range pods {
		unit.AddPod(pod)
	}
	return unit
}
