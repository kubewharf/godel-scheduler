/*
Copyright 2017 The Kubernetes Authors.

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
	"math/rand"
	"time"

	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	"github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	katalystinformers "github.com/kubewharf/katalyst-api/pkg/client/informers/externalversions"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/apimachinery/pkg/util/wait"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	commoncache "github.com/kubewharf/godel-scheduler/pkg/common/cache"
	"github.com/kubewharf/godel-scheduler/pkg/features"
	framework "github.com/kubewharf/godel-scheduler/pkg/framework/api"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
	preemptionstore "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/commonstores/preemption_store"
	cachedebugger "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache/debugger"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/controller"
	podscheduler "github.com/kubewharf/godel-scheduler/pkg/scheduler/core/pod_scheduler"
	unitscheduler "github.com/kubewharf/godel-scheduler/pkg/scheduler/core/unit_scheduler"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/metrics"
	godelqueue "github.com/kubewharf/godel-scheduler/pkg/scheduler/queue"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/reconciler"
	schedulerutil "github.com/kubewharf/godel-scheduler/pkg/scheduler/util"
)

// Scheduler watches for new unscheduled pods. It attempts to find
// nodes that they fit on and writes bindings back to the api server. Scheduler is the wrapper that watches for all
// pod and node event, the actual scheduling process is handled by schedule framework.
type Scheduler struct {
	// Name identifies the Godel Scheduler
	Name string
	// SchedulerName here is the higher level scheduler name, which is used to select pods
	// that godel schedulers should be responsible for and filter out irrelevant pods.
	SchedulerName *string

	// Close this to shut down the scheduler.
	StopEverything         <-chan struct{}
	scheduledPodsHasSynced func() bool
	clock                  clock.Clock

	// client syncs K8S object
	client clientset.Interface
	// crdClient syncs custom resources
	crdClient          godelclient.Interface
	informerFactory    informers.SharedInformerFactory
	crdInformerFactory crdinformers.SharedInformerFactory
	options            schedulerOptions

	podLister corelisters.PodLister
	pgLister  v1alpha1.PodGroupLister
	pvcLister corelisters.PersistentVolumeClaimLister

	commonCache    godelcache.SchedulerCache
	ScheduleSwitch ScheduleSwitch

	mayHasPreemption        bool
	defaultSubClusterConfig *subClusterConfig

	schedulerMaintainer StatusMaintainer
	recorder            events.EventRecorder
	metricsRecorder     *godelcache.ClusterCollectable

	movementController controller.CommonController
}

// New returns a Scheduler
func New(
	godelSchedulerName string,
	schedulerName *string,
	client clientset.Interface,
	crdClient godelclient.Interface,
	informerFactory informers.SharedInformerFactory,
	crdInformerFactory crdinformers.SharedInformerFactory,
	katalystCrdInformerFactory katalystinformers.SharedInformerFactory,
	stopCh <-chan struct{},
	recorder events.EventRecorder,
	reservationTTL time.Duration,
	opts ...Option) (*Scheduler, error,
) {
	// 1. Prepare Dependencies
	// register metrics before everything
	// see https://github.com/kubewharf/godel-scheduler/issues/79 for more details
	metrics.Register(godelSchedulerName)
	stopEverything := stopCh
	if stopEverything == nil {
		stopEverything = wait.NeverStop
	}
	options := renderOptions(opts...)
	globalClock := clock.RealClock{}

	podLister := informerFactory.Core().V1().Pods().Lister()
	podInformer := informerFactory.Core().V1().Pods()
	pvcLister := informerFactory.Core().V1().PersistentVolumeClaims().Lister()
	pgLister := crdInformerFactory.Scheduling().V1alpha1().PodGroups().Lister()

	mayHasPreemption := parseProfilesBoolConfiguration(options, profileNeedPreemption)

	handlerWrapper := commoncache.MakeCacheHandlerWrapper().
		ComponentName(godelSchedulerName).SchedulerType(*schedulerName).SubCluster(framework.DefaultSubCluster).
		PodAssumedTTL(15 * time.Minute).Period(10 * time.Second).ReservationTTL(reservationTTL).StopCh(stopEverything).
		PodLister(podLister).PodInformer(podInformer).PVCLister(pvcLister)
	if mayHasPreemption {
		handlerWrapper.EnableStore(string(preemptionstore.Name))
	}

	// 2. Make Scheduler
	sched := &Scheduler{
		Name:                   godelSchedulerName,
		SchedulerName:          schedulerName,
		StopEverything:         stopEverything,
		scheduledPodsHasSynced: informerFactory.Core().V1().Pods().Informer().HasSynced,
		clock:                  globalClock,
		client:                 client,
		crdClient:              crdClient,
		informerFactory:        informerFactory,
		crdInformerFactory:     crdInformerFactory,
		options:                options,

		podLister: podLister,
		pgLister:  pgLister,
		pvcLister: pvcLister,

		commonCache: godelcache.New(handlerWrapper.Obj()),

		mayHasPreemption:        mayHasPreemption,
		defaultSubClusterConfig: newDefaultSubClusterConfig(options.defaultProfile),

		schedulerMaintainer: NewSchedulerStatusMaintainer(globalClock, crdClient, godelSchedulerName, options.renewInterval),
		recorder:            recorder,
		metricsRecorder:     godelcache.NewEmptyClusterCollectable(godelSchedulerName),
	}

	if utilfeature.DefaultFeatureGate.Enabled(features.SupportRescheduling) {
		rateLimiter := workqueue.NewMaxOfRateLimiter(
			workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 500*time.Second),
			// 10 qps, 100 bucket size.  This is only for retry speed and its only the overall factor (not per item)
			&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
		)
		movementQueue := workqueue.NewNamedRateLimitingQueue(rateLimiter, "Movement")
		movementLister := crdInformerFactory.Scheduling().V1alpha1().Movements().Lister()
		sched.movementController = controller.NewMovementController(movementQueue, stopEverything, movementLister, godelSchedulerName, crdClient)
	}

	// 3. Create sub-cluster workflows.
	framework.SetGlobalSubClusterKey(options.subClusterKey)
	framework.CleanClusterIndex()
	sched.ScheduleSwitch = NewScheduleSwitch()
	if !utilfeature.DefaultFeatureGate.Enabled(features.SchedulerConcurrentScheduling) {
		globalDataSet := sched.createDataSet(framework.DefaultSubClusterIndex, framework.DefaultSubCluster, framework.DisableScheduleSwitch)
		sched.ScheduleSwitch.Register(framework.DisableScheduleSwitch, globalDataSet)
	} else {
		subClusters := []string{framework.DefaultSubCluster}
		if utilfeature.DefaultFeatureGate.Enabled(features.SchedulerSubClusterConcurrentScheduling) {
			for i := range options.subClusterProfiles {
				subClusters = append(subClusters, options.subClusterProfiles[i].SubClusterName)
			}
		}
		for i := range subClusters {
			subCluster := subClusters[i]
			idx := framework.AllocClusterIndex(subCluster)
			sched.createSubClusterWorkflow(idx, subCluster)
		}
	}

	// 4. Add event handlers
	addAllEventHandlers(sched, informerFactory, crdInformerFactory, katalystCrdInformerFactory)

	return sched, nil
}

// Run begins watching and scheduling. It waits for cache to be synced, then starts scheduling and blocked until the context is done.
func (sched *Scheduler) Run(ctx context.Context) {
	// run scheduler maintainer to maintain scheduler status in CRD
	go sched.schedulerMaintainer.Run(sched.StopEverything)
	if !cache.WaitForCacheSync(ctx.Done(), sched.scheduledPodsHasSynced) {
		return
	}

	if utilfeature.DefaultFeatureGate.Enabled(features.SchedulerCacheScrape) {
		// The metrics agent scrape endpoint every 5s and flush them to the metrics server every 30s. To
		// be more precise, scrape cache metrics every 5s.
		go wait.Until(func() {
			sched.commonCache.ScrapeCollectable(sched.metricsRecorder)
			sched.metricsRecorder.UpdateMetrics()
		}, 5*time.Second, sched.StopEverything)
	}

	rand.Seed(time.Now().Unix())

	if utilfeature.DefaultFeatureGate.Enabled(features.SupportRescheduling) {
		sched.movementController.Run()
		defer sched.movementController.Close()
	}

	sched.ScheduleSwitch.Run(ctx)
}

func (sched *Scheduler) createDataSet(idx int, subCluster string, switchType framework.SwitchType) ScheduleDataSet {
	var subClusterConfig *subClusterConfig
	if profile, ok := sched.options.subClusterProfiles[subCluster]; ok {
		subClusterConfig = newSubClusterConfigFromDefaultConfig(&profile, sched.defaultSubClusterConfig)
	} else {
		subClusterConfig = sched.defaultSubClusterConfig
	}
	klog.InfoS("CreateSubClusterWorkflow DataSet", "subCluster", subCluster, "clusterIndex", idx, "subClusterConfig", subClusterConfig)

	pluginArgs := make(map[string]*config.PluginConfig)
	for index := range subClusterConfig.PluginConfigs {
		pluginArg := subClusterConfig.PluginConfigs[index]
		pluginArgs[pluginArg.Name] = &pluginArg
	}
	unitQueueSortPlugin, err := godelqueue.InitUnitQueueSortPlugin(subClusterConfig.UnitQueueSortPlugin, pluginArgs)
	if err != nil {
		panic(err)
	}
	preemptionPluginArgs := make(map[string]*config.PluginConfig)
	for index := range subClusterConfig.PreemptionPluginConfigs {
		pluginArgs := subClusterConfig.PreemptionPluginConfigs[index]
		preemptionPluginArgs[pluginArgs.Name] = &pluginArgs
	}

	handler := commoncache.MakeCacheHandlerWrapper().
		SubCluster(subCluster).SwitchType(switchType).
		EnableStore(schedulerutil.FilterTrueKeys(subClusterConfig.EnableStore)...).
		PodLister(sched.podLister).
		PVCLister(sched.pvcLister).
		Obj()
	snapshot := godelcache.NewEmptySnapshot(handler)
	podScheduler := podscheduler.NewPodScheduler(
		sched.Name,
		switchType,
		subCluster,
		sched.client,
		sched.crdClient,
		sched.informerFactory,
		sched.crdInformerFactory,
		snapshot,
		sched.clock,
		subClusterConfig.DisablePreemption,
		subClusterConfig.CandidatesSelectPolicy,
		subClusterConfig.BetterSelectPolicies,
		subClusterConfig.PercentageOfNodesToScore,
		subClusterConfig.IncreasedPercentageOfNodesToScore,
		subClusterConfig.BasePlugins,
		pluginArgs,
		preemptionPluginArgs,
	)
	schedulingQueue := godelqueue.NewSchedulingQueue(
		sched.commonCache,
		sched.informerFactory.Scheduling().V1().PriorityClasses().Lister(),
		sched.crdInformerFactory.Scheduling().V1alpha1().PodGroups().Lister(),
		unitQueueSortPlugin.Less,
		subClusterConfig.UseBlockQueue,
		godelqueue.WithUnitInitialBackoffDuration(time.Duration(subClusterConfig.UnitInitialBackoffSeconds)*time.Second),
		godelqueue.WithPodMaxBackoffDuration(time.Duration(subClusterConfig.UnitMaxBackoffSeconds)*time.Second),
		godelqueue.WithOwner(sched.Name),
		godelqueue.WithSwitchType(switchType),
		godelqueue.WithSubCluster(subCluster),
		godelqueue.WithClock(sched.clock),
	)
	reconciler := reconciler.NewFailedTaskReconciler(sched.client, sched.informerFactory.Core().V1().Pods().Lister(), sched.commonCache, *sched.SchedulerName)
	unitScheduler := unitscheduler.NewUnitScheduler(
		sched.Name,
		switchType,
		subCluster,
		subClusterConfig.DisablePreemption,
		sched.client,
		sched.crdClient,
		sched.podLister,
		sched.pvcLister,
		sched.pgLister,
		sched.commonCache,
		snapshot,
		schedulingQueue,
		reconciler,
		podScheduler,
		sched.clock,
		sched.recorder,
		time.Duration(subClusterConfig.MaxWaitingDeletionDuration)*time.Second,
	)
	debugger := cachedebugger.New(
		sched.informerFactory.Core().V1().Nodes().Lister(),
		sched.informerFactory.Core().V1().Pods().Lister(),
		sched.commonCache,
		schedulingQueue,
	)
	return NewScheduleDataSet(
		idx,
		subCluster,
		switchType,
		snapshot,
		schedulingQueue,
		unitScheduler,
		reconciler,
		debugger,
	)
}

func (sched *Scheduler) createSubClusterWorkflow(idx int, subCluster string) (ScheduleDataSet, ScheduleDataSet) {
	klog.V(4).InfoS("Entered createSubClusterWorkflow", "subCluster", subCluster, "clusterIndex", idx)
	gt, be := framework.ClusterIndexToSwitchType(idx)
	for _, st := range []framework.SwitchType{gt, be} {
		dataSet := sched.createDataSet(idx, subCluster, st)
		sched.ScheduleSwitch.Register(st, dataSet)
	}
	return sched.ScheduleSwitch.Get(gt), sched.ScheduleSwitch.Get(be)
}
