package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	schedulingv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/apis/scheduling/v1alpha1"
	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	schedulinglisterv1a1 "github.com/kubewharf/godel-scheduler-api/pkg/client/listers/scheduling/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

type CommonController interface {
	// Run starts the goroutines managing the queue.
	Run()
	// Close closes the queue so that the goroutine which is
	// waiting to pop items can exit gracefully.
	Close()
	// add movement into queue
	AddMovement(movement *schedulingv1a1.Movement)
}

var _ CommonController = &MovementController{}

type MovementController struct {
	workqueue.RateLimitingInterface
	stopCh         <-chan struct{}
	movementLister schedulinglisterv1a1.MovementLister
	schedulerName  string
	crdClient      godelclient.Interface
}

func NewMovementController(queue workqueue.RateLimitingInterface, stopCh <-chan struct{},
	movementLister schedulinglisterv1a1.MovementLister, schedulerName string,
	crdClient godelclient.Interface,
) CommonController {
	return &MovementController{queue, stopCh, movementLister, schedulerName, crdClient}
}

// Close closes the priority queue.
func (mc *MovementController) Close() {
	mc.ShutDown()
}

func (mc *MovementController) AddMovement(movement *schedulingv1a1.Movement) {
	if len(movement.Status.Owners) == 0 {
		return
	}
	mc.Add(movement.Name)
}

func (mc *MovementController) Run() {
	go wait.Until(mc.runMovementWorker, time.Second, mc.stopCh)
}

func (mc *MovementController) runMovementWorker() {
	for mc.processNextMovement() {
	}
}

func (mc *MovementController) processNextMovement() bool {
	obj, shutdown := mc.Get()
	if shutdown {
		return false
	}
	var (
		key string
		ok  bool
	)
	err := func(obj interface{}) error {
		defer mc.Done(obj)
		if key, ok = obj.(string); !ok {
			mc.Forget(obj)
			return fmt.Errorf("expected string in workqueue but got %#v", obj)
		}
		if err := mc.syncMovement(key); err != nil {
			mc.AddRateLimited(obj)
			return fmt.Errorf("error syncing movement %q: %s", key, err.Error())
		}
		mc.Forget(obj)
		return nil
	}(obj)
	if err != nil {
		klog.InfoS("Failed to process movement", "movementObject", obj, "err", err)
	}
	return true
}

func (mq *MovementController) syncMovement(movementName string) error {
	klog.V(4).InfoS("Syncing movement", "movementName", movementName)
	movement, err := mq.movementLister.Get(movementName)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	if errors.IsNotFound(err) {
		// ignore
		return nil
	}
	if len(movement.Status.Owners) == 0 {
		return nil
	}
	for _, notifiedScheduler := range movement.Status.NotifiedSchedulers {
		if notifiedScheduler == mq.schedulerName {
			return nil
		}
	}
	movementCopy := movement.DeepCopy()
	movementCopy.Status.NotifiedSchedulers = append(movementCopy.Status.NotifiedSchedulers, mq.schedulerName)

	oldData, err := json.Marshal(movement)
	if err != nil {
		return err
	}
	newData, err := json.Marshal(movementCopy)
	if err != nil {
		return err
	}
	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, &schedulingv1a1.Movement{})
	if err != nil {
		return err
	}

	if mq.crdClient == nil {
		return fmt.Errorf("failed to patch movement status for %s because crd client is nil", movementName)
	}
	_, err = mq.crdClient.SchedulingV1alpha1().Movements().Patch(context.Background(), movement.Name, types.MergePatchType, patchBytes, v1.PatchOptions{}, "status")
	return err
}
