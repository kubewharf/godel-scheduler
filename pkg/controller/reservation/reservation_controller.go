/*
Copyright 2024 The Godel Scheduler Authors.

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

package reservation

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	appsinformers "k8s.io/client-go/informers/apps/v1"
	coreinformers "k8s.io/client-go/informers/core/v1"
	appslister "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	reservationinformer "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions/scheduling/v1alpha1"
	reservationlister "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	controllersmetrics "github.com/kubewharf/godel-scheduler/pkg/controller/metrics"
	reservationmetrics "github.com/kubewharf/godel-scheduler/pkg/controller/reservation/metrics"
	"github.com/kubewharf/godel-scheduler/pkg/controller/reservation/utils"
	"github.com/kubewharf/godel-scheduler/pkg/util"
	deployutil "github.com/kubewharf/godel-scheduler/pkg/util/deployment"
	"github.com/kubewharf/godel-scheduler/pkg/util/helper"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"
)

const (
	MaxRetryAttempts = 3
)

type ReservationController struct {
	godelClient godelclient.Interface
	// eventRecorder   record.EventRecorder
	podReservationLister       reservationlister.ReservationLister
	podReservationListerSynced cache.InformerSynced
	podReservationQueue        workqueue.DelayingInterface
	deploymentLister           appslister.DeploymentLister
	reservationCheckPeriod     int64
	reservationTTL             int64
	matchedPodExtraTTL         int64
	// TODO: deal with fault node, remove reservation CRD on the node.
}

func init() {
	// Register prometheus metrics
	// RegisterMetrics()
}

func NewReservationController(
	ctx context.Context,
	godelClient godelclient.Interface,
	podInformer coreinformers.PodInformer,
	deployInformer appsinformers.DeploymentInformer,
	podReservationInformer reservationinformer.ReservationInformer,
	reservationCheckPeriod int64,
	reservationTTL int64,
	matchedPodExtraTTL int64,
) *ReservationController {
	rc := &ReservationController{
		godelClient:                godelClient,
		podReservationLister:       podReservationInformer.Lister(),
		podReservationListerSynced: podReservationInformer.Informer().HasSynced,
		podReservationQueue:        workqueue.NewNamedDelayingQueue("pod_reservation_request"),
		deploymentLister:           deployInformer.Lister(),
		reservationCheckPeriod:     reservationCheckPeriod,
		reservationTTL:             reservationTTL,
		matchedPodExtraTTL:         matchedPodExtraTTL,
	}

	podInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			DeleteFunc: rc.createReservationCrdOnPodDeletion,
		})

	return rc
}

func (rc *ReservationController) Run(ctx context.Context, controllerManagerMetrics *controllersmetrics.ControllerManagerMetrics) {
	defer utilruntime.HandleCrash()
	controllerManagerMetrics.ControllerStarted("reservation-controller")
	defer controllerManagerMetrics.ControllerStopped("reservation-controller")

	klog.V(3).InfoS("Starting Reservation Controller")
	defer rc.podReservationQueue.ShutDown()
	defer klog.V(3).InfoS("Shutting down Reservation Controller")

	if !cache.WaitForNamedCacheSync("Reservation", ctx.Done(), rc.podReservationListerSynced) {
		return
	}

	checkPeriod := time.Duration(rc.reservationCheckPeriod) * time.Second

	go wait.UntilWithContext(ctx, rc.handleReservation, checkPeriod)

	<-ctx.Done()
}

func (rc *ReservationController) createReservationCrdOnPodDeletion(obj interface{}) {
	pod, err := podutil.ConvertToPod(obj)
	if err != nil {
		klog.ErrorS(err, "Failed to create reservation")
		return
	}

	ttl := rc.reservationTTL
	if !podutil.BoundPod(pod) {
		return
	}
	if !podutil.HasReservationRequirement(pod) {
		deployName := util.GetDeployNameFromPod(pod)
		if len(deployName) == 0 {
			return
		}

		deploy, err := rc.deploymentLister.Deployments(pod.Namespace).Get(deployName)
		if err != nil || !deployutil.DeployHasReservationRequirement(deploy) {
			return
		}
		if t := deployutil.GetReservationTtlFromDeploy(deploy); t != 0 {
			ttl = t
		}
	}

	// build CRD
	reservation, err := podutil.ConstructReservationAccordingToPod(pod, ttl)
	if err != nil {
		klog.ErrorS(err, "Failed to create reservation")
		return
	}

	// call API to create CRD
	result := reservationmetrics.SuccessResult
	if err := util.Retry(MaxRetryAttempts, time.Second, func() error {
		return rc.createReservation(reservation)
	}); err != nil {
		klog.ErrorS(err, "Failed to create reservation", "reservation", klog.KObj(reservation), "index", podutil.GetReservationPlaceholder(pod))
		result = reservationmetrics.FailureResult
		reservationmetrics.IncreaseReservationAPICall("create", result)
		return
	}

	reservationmetrics.IncreaseReservationAPICall("create", result)
	klog.V(4).InfoS("Succeed to create reservation", "reservation", klog.KObj(reservation), "index", podutil.GetReservationPlaceholder(pod))
}

func (rc *ReservationController) handleReservation(ctx context.Context) {
	reservations, err := rc.podReservationLister.List(labels.Everything())
	if err != nil {
		klog.ErrorS(err, "Error while listing all pod reservation requests")
		return
	}
	rc.gcReservations(ctx, reservations)
}

func (rc *ReservationController) gcReservations(
	ctx context.Context,
	reservations []*schedulingv1a1.Reservation,
) {
	// TODO: timing wheel.
	klog.V(5).InfoS("Start reservations gc process")

	type gcObject struct {
		status string
		obj    *schedulingv1a1.Reservation
	}

	var (
		active    = 0
		waitForGC = make([]gcObject, 0)
	)

	for _, prr := range reservations {
		if rc.isReservationMatched(prr) {
			waitForGC = append(waitForGC, gcObject{
				status: reservationmetrics.MatchStatus,
				obj:    prr,
			})
		} else if rc.isReservationTimeout(prr) {
			waitForGC = append(waitForGC, gcObject{
				status: reservationmetrics.TimeoutStatus,
				obj:    prr,
			})
		} else {
			active += 1
		}
	}

	for _, o := range waitForGC {
		prr, status := o.obj, o.status
		if err := rc.deleteReservation(prr); err != nil {
			klog.ErrorS(err, "Failed to delete reservation", "status", status, "reservation", klog.KObj(prr), "index", utils.GetReservationIndex(prr), "owner", utils.GetReservationOwner(prr))
			continue
		}

		klog.V(4).InfoS("Succeed to delete reservation", "status", status, "reservation", klog.KObj(prr), "index", utils.GetReservationIndex(prr), "owner", utils.GetReservationOwner(prr))
		reservationmetrics.ObserveReservationRecycleLatency(status, helper.SinceInSeconds(prr.CreationTimestamp.Time))
	}

	reservationmetrics.SetExistingReservationCount(active)
}

func (rc *ReservationController) isReservationTimeout(prr *schedulingv1a1.Reservation) bool {
	if prr.Status.Phase != schedulingv1a1.ReservationMatched {
		timeoutDuration := time.Duration(*prr.Spec.TimeToLive) * time.Second
		return time.Since(prr.CreationTimestamp.Time) > timeoutDuration
	}
	return false
}

func (rc *ReservationController) isReservationMatched(prr *schedulingv1a1.Reservation) bool {
	return prr.Status.Phase == schedulingv1a1.ReservationMatched
}

func (rc *ReservationController) createReservation(prr *schedulingv1a1.Reservation) error {
	_, err := rc.godelClient.SchedulingV1alpha1().Reservations(prr.Namespace).Create(context.TODO(), prr, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	return err
}

func (rc *ReservationController) deleteReservation(prr *schedulingv1a1.Reservation) error {
	result := reservationmetrics.SuccessResult

	err := rc.godelClient.SchedulingV1alpha1().Reservations(prr.Namespace).Delete(context.TODO(), prr.Name, metav1.DeleteOptions{})
	if err != nil {
		result = reservationmetrics.FailureResult
	}

	reservationmetrics.IncreaseReservationAPICall("delete", result)
	return err
}
