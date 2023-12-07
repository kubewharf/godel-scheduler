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

package scheduler

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	nodev1alpha1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/node/v1alpha1"
	katalystv1alpha1 "github.com/kubewharf/katalyst-api/pkg/apis/node/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	cachedebugger "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/debugger"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/core"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/queue"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/reconciler"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

type CtxKeyScheduleDataSetType string

const (
	CtxKeyScheduleDataSet = CtxKeyScheduleDataSetType("ScheduleDataSet")
)

type ScheduleDataSet interface {
	ClusterIndex() int
	SubCluster() string
	Type() framework.SwitchType
	Ctx() context.Context

	Run(context.Context) bool
	Close() bool
	CanBeRecycle() bool

	Snapshot() *cache.Snapshot
	SchedulingQueue() queue.SchedulingQueue
	ScheduleFunc() func(context.Context)
}

type ScheduleDataSetImpl struct {
	ctx    context.Context
	cancel context.CancelFunc
	state  int32 // Default 0, be set to 1 after `Run`.

	clusterIndex int
	subCluster   string
	switchType   framework.SwitchType

	snapshot        *cache.Snapshot
	schedulingQueue queue.SchedulingQueue
	unitScheduler   core.UnitScheduler
	reconciler      *reconciler.FailedTaskReconciler
	debugger        *cachedebugger.CacheDebugger
}

func NewScheduleDataSet(
	idx int,
	subCluster string,
	switchType framework.SwitchType,
	snapshot *cache.Snapshot,
	schedulingQueue queue.SchedulingQueue,
	unitScheduler core.UnitScheduler,
	reconciler *reconciler.FailedTaskReconciler,
	debugger *cachedebugger.CacheDebugger,
) ScheduleDataSet {
	return &ScheduleDataSetImpl{
		clusterIndex:    idx,
		subCluster:      subCluster,
		switchType:      switchType,
		snapshot:        snapshot,
		schedulingQueue: schedulingQueue,
		unitScheduler:   unitScheduler,
		reconciler:      reconciler,
		debugger:        debugger,
	}
}

var _ ScheduleDataSet = &ScheduleDataSetImpl{}

func (s *ScheduleDataSetImpl) ClusterIndex() int {
	return s.clusterIndex
}

func (s *ScheduleDataSetImpl) SubCluster() string {
	return s.subCluster
}

func (s *ScheduleDataSetImpl) Type() framework.SwitchType {
	return s.switchType
}

func (s *ScheduleDataSetImpl) Ctx() context.Context {
	return s.ctx
}

func (s *ScheduleDataSetImpl) Run(parentCtx context.Context) bool {
	// Workflows that have already been started do not need to be started again.
	if !atomic.CompareAndSwapInt32(&s.state, 0, 1) {
		return false
	}

	klog.V(4).InfoS("Started the Run workflow", "subCluster", s.subCluster, "switchType", s.switchType)
	defer klog.V(4).InfoS("Completed the Run workflow", "subCluster", s.subCluster, "switchType", s.switchType)

	s.ctx, s.cancel = context.WithCancel(parentCtx)
	s.schedulingQueue.Run()
	s.reconciler.Run()
	s.debugger.Run()
	return true
}

func (s *ScheduleDataSetImpl) Close() bool {
	// Only workflows that have been started can be recycled.
	if atomic.LoadInt32(&s.state) != 1 {
		return false
	}

	klog.V(4).InfoS("Started the Close workflow", "subCluster", s.subCluster, "switchType", s.switchType)
	defer klog.V(4).InfoS("Completed the Close workflow", "subCluster", s.subCluster, "switchType", s.switchType)

	s.schedulingQueue.Close()
	s.reconciler.Close()
	s.debugger.Close()
	s.cancel()
	return true
}

func (s *ScheduleDataSetImpl) Snapshot() *cache.Snapshot {
	return s.snapshot
}

func (s *ScheduleDataSetImpl) SchedulingQueue() queue.SchedulingQueue {
	return s.schedulingQueue
}

func (s *ScheduleDataSetImpl) ScheduleFunc() func(context.Context) {
	return s.unitScheduler.Schedule
}

// TODO: revisit this rule.
func (s *ScheduleDataSetImpl) CanBeRecycle() bool {
	return s.schedulingQueue.CanBeRecycle() && s.unitScheduler.CanBeRecycle()
}

func (s *ScheduleDataSetImpl) String() string {
	return s.switchType.String() + "_" + s.subCluster
}

type ProcessFunc func(ScheduleDataSet)

type ScheduleSwitch interface {
	Run(context.Context)
	Get(framework.SwitchType) ScheduleDataSet
	Register(switchType framework.SwitchType, dataSet ScheduleDataSet)
	Process(framework.SwitchType, ProcessFunc)
}

type ScheduleSwitchImpl struct {
	registry map[framework.SwitchType]ScheduleDataSet
	mutex    sync.RWMutex
}

var _ ScheduleSwitch = &ScheduleSwitchImpl{}

func NewScheduleSwitch() ScheduleSwitch {
	return &ScheduleSwitchImpl{map[framework.SwitchType]ScheduleDataSet{}, sync.RWMutex{}}
}

func (s *ScheduleSwitchImpl) Run(ctx context.Context) {
	if !utilfeature.DefaultFeatureGate.Enabled(features.SchedulerConcurrentScheduling) {
		if globalDataSet, ok := s.registry[framework.DisableScheduleSwitch]; ok && globalDataSet != nil {
			globalDataSet.Run(ctx)
			wait.UntilWithContext(context.WithValue(globalDataSet.Ctx(), CtxKeyScheduleDataSet, globalDataSet), globalDataSet.ScheduleFunc(), 0)
		} else {
			klog.ErrorS(nil, "SchedulerConcurrentScheduling was disabled while the DataSet couldn't be found")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
		return
	}

	if !utilfeature.DefaultFeatureGate.Enabled(features.SchedulerSubClusterConcurrentScheduling) {
		// We don't have to execute `workflowStartup` periodically when the featuregate is not enabled.
		s.workflowStartup(ctx)
		<-ctx.Done()
		return
	}

	// Then both SchedulerConcurrentScheduling and SchedulerSubClusterConcurrentScheduling are enabled.

	// 1. Run scheduling workflows that may be created at any time.
	{
		go wait.UntilWithContext(ctx, s.workflowStartup, 1*time.Second)
	}

	// 2. Recycle all scheduling workflows that have not been used for a certain period of time.
	{
		// TODO: Should recycling be avoided during the beta phase?

		// TODO: revisit the trick number.
		interval := 1 * 24 * time.Hour

		wait.Until(func() {
			// TODO: revisit the trick number.
			if runtime.NumGoroutine() > 2000 {
				return
			}
			s.workflowRecycle(0)
		}, interval, ctx.Done())
		s.workflowRecycle(1)
	}
}

func (s *ScheduleSwitchImpl) workflowStartup(ctx context.Context) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	startup := func(dataSet ScheduleDataSet) bool {
		if dataSet != nil && dataSet.Run(ctx) {
			klog.V(4).InfoS("Detected WorkflowStartup", "subCluster", dataSet.SubCluster(), "switchType", dataSet.Type())
			go wait.UntilWithContext(context.WithValue(dataSet.Ctx(), CtxKeyScheduleDataSet, dataSet), dataSet.ScheduleFunc(), 0)
			return true
		}
		return false
	}

	for i := 0; i < framework.MaxSwitchNum; i++ {
		gtDataSet, beDataSet := s.registry[framework.ClusterIndexToGTSwitchType(i)], s.registry[framework.ClusterIndexToBESwitchType(i)]
		if startup(gtDataSet) != startup(beDataSet) {
			// TODO: revisit this message.
			klog.ErrorS(nil, "WorkflowStartup was invalid, the workflows of the same subcluster could not start running at the same time, which should not happen", "subCluster", gtDataSet.SubCluster(), "index", i)
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
	}
}

func (s *ScheduleSwitchImpl) workflowRecycle(force int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	canBeRecycle := func(gtDataSet, beDataSet ScheduleDataSet) bool {
		if gtDataSet == nil || beDataSet == nil {
			return false
		}
		return force == 1 || (gtDataSet.CanBeRecycle() && beDataSet.CanBeRecycle())
	}

	recycle := func(dataSet ScheduleDataSet) bool {
		if dataSet != nil && dataSet.Close() {
			klog.V(4).InfoS("Detected WorkflowRecycle", "subCluster", dataSet.SubCluster(), "switchType", dataSet.Type(), "force", force)
			delete(s.registry, dataSet.Type())
			return true
		}
		return false
	}

	// ATTENTION: we won't recycle the index 0 unless force is 1.
	for i := 1 - force; i < framework.MaxSwitchNum; i++ {
		gtDataSet, beDataSet := s.registry[framework.ClusterIndexToGTSwitchType(i)], s.registry[framework.ClusterIndexToBESwitchType(i)]
		if canBeRecycle(gtDataSet, beDataSet) {
			if recycle(gtDataSet) != recycle(beDataSet) {
				// TODO: revisit this message.
				klog.ErrorS(nil, "WorkflowRecycle was invalid, the workflows of same subcluster could not stop running at the same time, which should not happen", "subCluster", gtDataSet.SubCluster(), "index", i)
				klog.FlushAndExit(klog.ExitFlushTimeout, 1)
			}
			freedClusterIndex := framework.FreeClusterIndex(gtDataSet.SubCluster())
			if freedClusterIndex != i {
				// TODO: revisit this message.
				klog.ErrorS(nil, "WorkflowRecycle was invalid, freed cluster index did not match the subcluster, which should not happen", "freedClusterIndex", freedClusterIndex, "index", i, "subCluster", gtDataSet.SubCluster())
				klog.FlushAndExit(klog.ExitFlushTimeout, 1)
			}
		}
	}
}

func (s *ScheduleSwitchImpl) Register(switchType framework.SwitchType, dataSet ScheduleDataSet) {
	if dataSet == nil {
		return
	}
	state := dataSet.Type()
	if switchType != state {
		panic("SwitchType doesn't match")
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	klog.V(4).InfoS("Registered workflow", "subCluster", dataSet.SubCluster(), "switchType", dataSet.Type())
	if _, ok := s.registry[state]; ok {
		panic("Duplicate ScheduleDataSet")
	}
	s.registry[state] = dataSet
}

func (s *ScheduleSwitchImpl) Get(state framework.SwitchType) ScheduleDataSet {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	var dataSet ScheduleDataSet
	for i := 0; 1<<i <= state; i++ {
		if (state >> i & 1) != 0 {
			if dataSet != nil {
				// This should not be happen.
				klog.ErrorS(nil, "Invalid SwitchType State", "state", state)
				klog.FlushAndExit(klog.ExitFlushTimeout, 1)
			}
			dataSet = s.registry[1<<i]
		}
	}
	return dataSet
}

func (s *ScheduleSwitchImpl) Process(state framework.SwitchType, f ProcessFunc) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	if !utilfeature.DefaultFeatureGate.Enabled(features.SchedulerConcurrentScheduling) {
		if dataSet, ok := s.registry[framework.DisableScheduleSwitch]; ok && dataSet != nil {
			f(dataSet)
		} else {
			klog.ErrorS(nil, "SchedulerConcurrentScheduling was disabled while the DataSet couldn't be found")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
		return
	}

	var wg sync.WaitGroup
	for i := 0; 1<<i < framework.SwitchTypeAll; i++ {
		if (state >> i & 1) != 0 {
			if dataSet, ok := s.registry[1<<i]; ok {
				wg.Add(1)
				go func() {
					defer func() {
						if err := recover(); err != nil {
							panic(err)
						}
						wg.Done()
					}()
					f(dataSet)
				}()
			}
		}
	}
	wg.Wait()
}

func ParseSwitchTypeForNode(node *v1.Node) framework.SwitchType {
	st := framework.DefaultSubClusterSwitchType
	if utilfeature.DefaultFeatureGate.Enabled(features.SchedulerSubClusterConcurrentScheduling) {
		return st |
			framework.ParseSwitchTypeFromSubCluster(node.Labels[framework.GetGlobalSubClusterKey()])
	}
	return st
}

func ParseSwitchTypeForNMNode(nmNode *nodev1alpha1.NMNode) framework.SwitchType {
	st := framework.DefaultSubClusterSwitchType
	if utilfeature.DefaultFeatureGate.Enabled(features.SchedulerSubClusterConcurrentScheduling) {
		return st |
			framework.ParseSwitchTypeFromSubCluster(nmNode.Labels[framework.GetGlobalSubClusterKey()])
	}
	return st
}

func ParseSwitchTypeForCNR(cnr *katalystv1alpha1.CustomNodeResource) framework.SwitchType {
	st := framework.DefaultSubClusterSwitchType
	if utilfeature.DefaultFeatureGate.Enabled(features.SchedulerSubClusterConcurrentScheduling) {
		return st |
			framework.ParseSwitchTypeFromSubCluster(cnr.Labels[framework.GetGlobalSubClusterKey()])
	}
	return st
}

func ParseSwitchTypeForPod(pod *v1.Pod) framework.SwitchType {
	var st framework.SwitchType
	if utilfeature.DefaultFeatureGate.Enabled(features.SchedulerSubClusterConcurrentScheduling) {
		st = framework.ParseSwitchTypeFromSubCluster(pod.Spec.NodeSelector[framework.GetGlobalSubClusterKey()])
	} else {
		st = framework.DefaultSubClusterSwitchType
	}
	resourceType, _ := podutil.GetPodResourceType(pod)
	if resourceType == podutil.BestEffortPod {
		return st & framework.BEBitMask
	}
	return st & framework.GTBitMask
}
