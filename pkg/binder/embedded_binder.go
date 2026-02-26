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

package binder

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	bindermetrics "github.com/kubewharf/godel-scheduler/pkg/binder/metrics"
	binderutils "github.com/kubewharf/godel-scheduler/pkg/binder/utils"
	godelcache "github.com/kubewharf/godel-scheduler/pkg/scheduler/cache"
)

// EmbeddedBinder implements BinderInterface for cases where the Binder is
// co-located within the Scheduler process. It shares the Scheduler's cache
// (via CacheAdapter) and binds Pods directly through the API server, avoiding
// the Informer-based communication channel used by the standalone Binder.
type EmbeddedBinder struct {
	client    clientset.Interface
	crdClient godelclient.Interface

	schedulerCache godelcache.SchedulerCache
	cacheAdapter   *CacheAdapter

	nodeValidator *NodeValidator
	reconciler    *BinderTasksReconciler

	schedulerName string
	config        *EmbeddedBinderConfig

	running atomic.Bool
	cancel  context.CancelFunc
}

// Ensure EmbeddedBinder implements BinderInterface at compile time.
var _ BinderInterface = &EmbeddedBinder{}

// NewEmbeddedBinder creates a new EmbeddedBinder. It panics if schedulerCache
// is nil. The config parameter may be nil, in which case defaults are used.
// nodeGetter may be nil to disable node-ownership validation (not recommended
// in production).
func NewEmbeddedBinder(
	client clientset.Interface,
	crdClient godelclient.Interface,
	schedulerCache godelcache.SchedulerCache,
	schedulerName string,
	config *EmbeddedBinderConfig,
	nodeGetter NodeGetter,
) *EmbeddedBinder {
	if schedulerCache == nil {
		panic("schedulerCache must not be nil for EmbeddedBinder")
	}
	if config == nil {
		config = DefaultEmbeddedBinderConfig()
	}

	var nv *NodeValidator
	if nodeGetter != nil {
		nv = NewNodeValidator(schedulerName, nodeGetter)
	}

	return &EmbeddedBinder{
		client:         client,
		crdClient:      crdClient,
		schedulerCache: schedulerCache,
		cacheAdapter:   NewCacheAdapter(schedulerCache),
		nodeValidator:  nv,
		schedulerName:  schedulerName,
		config:         config,
	}
}

// Start initialises the EmbeddedBinder. After Start returns, BindUnit may be
// called. Calling Start on an already-running binder returns an error.
func (eb *EmbeddedBinder) Start(ctx context.Context) error {
	if !eb.running.CompareAndSwap(false, true) {
		return fmt.Errorf("embedded binder is already running")
	}
	_, cancel := context.WithCancel(ctx)
	eb.cancel = cancel
	// Create a fresh reconciler on each Start so that a restart after Stop
	// gets a new stop channel (the previous one was closed by Stop).
	eb.reconciler = NewBinderTaskReconcilerWithRetry(eb.client, eb.schedulerName, eb.config.MaxLocalRetries)
	eb.reconciler.Run()
	klog.V(2).InfoS("Embedded Binder started", "scheduler", eb.schedulerName)
	return nil
}

// Stop gracefully shuts down the EmbeddedBinder. It is safe to call Stop
// multiple times.
func (eb *EmbeddedBinder) Stop() {
	if eb.running.CompareAndSwap(true, false) {
		if eb.reconciler != nil {
			eb.reconciler.Close()
		}
		if eb.cancel != nil {
			eb.cancel()
		}
		klog.V(2).InfoS("Embedded Binder stopped", "scheduler", eb.schedulerName)
	}
}

// BindUnit executes the bind pipeline for a single scheduling unit.
// In the embedded model, the Scheduler has already performed the full
// scheduling decision (filtering, scoring, reserving). The EmbeddedBinder
// is responsible for:
//  1. Validating the request
//  2. Validating node ownership (preventing stale binds during reshuffle)
//  3. Binding each Pod to its target node via the API server
//  4. Finishing the reservation in the shared cache
//  5. Handling failures (incrementing retry count, cleaning up)
func (eb *EmbeddedBinder) BindUnit(ctx context.Context, req *BindRequest) (*BindResult, error) {
	if !eb.running.Load() {
		return nil, ErrBinderNotRunning
	}

	if err := req.Validate(); err != nil {
		return nil, err
	}

	// Validate that every target node still belongs to this Scheduler's partition.
	// During node reshuffles the node may have been reassigned, which would
	// cause a stale bind.
	if eb.nodeValidator != nil {
		for _, nodeName := range req.UniqueNodeNames() {
			if err := eb.nodeValidator.Validate(nodeName); err != nil {
				klog.V(2).InfoS("Node validation failed, rejecting bind request",
					"scheduler", eb.schedulerName,
					"node", nodeName,
					"err", err)
				bindermetrics.ObserveNodeValidationFailure(eb.schedulerName, "ownership")
				return nil, fmt.Errorf("node validation failed for %q: %w", nodeName, err)
			}
		}
	}

	bindTimeout := eb.config.BindTimeout
	if bindTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, bindTimeout)
		defer cancel()
	}

	result := &BindResult{
		FailedPods: make(map[types.UID]error),
	}

	for _, qpi := range req.Pods {
		pod := qpi.Pod
		if pod == nil {
			continue
		}

		select {
		case <-ctx.Done():
			// Timeout – mark all remaining pods as failed.
			result.FailedPods[pod.UID] = ctx.Err()
			continue
		default:
		}

		targetNode := req.NodeNameFor(pod.UID)
		err := eb.bindPodToNode(ctx, pod, targetNode)
		if err != nil {
			klog.V(3).InfoS("Failed to bind pod in embedded binder",
				"scheduler", eb.schedulerName,
				"pod", klog.KObj(pod),
				"node", targetNode,
				"err", err)

			result.FailedPods[pod.UID] = err

			// Increment bind failure count for retry tracking.
			binderutils.IncrementBindFailureCount(pod)
			binderutils.SetLastBindFailureReason(pod, err.Error())
		} else {
			result.SuccessfulPods = append(result.SuccessfulPods, pod.UID)

			// Mark reservation as complete in shared cache.
			if finishErr := eb.schedulerCache.FinishReserving(pod); finishErr != nil {
				klog.V(3).InfoS("Failed to finish reserving after successful bind",
					"scheduler", eb.schedulerName,
					"pod", klog.KObj(pod),
					"err", finishErr)
			}

			klog.V(3).InfoS("Successfully bound pod via embedded binder",
				"scheduler", eb.schedulerName,
				"pod", klog.KObj(pod),
				"node", targetNode)
		}
	}

	return result, nil
}

// bindPodToNode creates a Binding object for the Pod on the target node.
// It retries on conflict errors up to MaxBindRetries.
func (eb *EmbeddedBinder) bindPodToNode(ctx context.Context, pod *v1.Pod, nodeName string) error {
	binding := &v1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: pod.Namespace,
			Name:      pod.Name,
			UID:       pod.UID,
		},
		Target: v1.ObjectReference{
			Kind: "Node",
			Name: nodeName,
		},
	}

	var lastErr error
	maxRetries := eb.config.MaxBindRetries
	if maxRetries <= 0 {
		maxRetries = 1
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		err := eb.client.CoreV1().Pods(binding.Namespace).Bind(ctx, binding, metav1.CreateOptions{})
		if err == nil {
			return nil
		}
		lastErr = err

		if errors.IsConflict(err) || errors.IsServerTimeout(err) || errors.IsTooManyRequests(err) {
			klog.V(4).InfoS("Transient error binding pod, will retry",
				"scheduler", eb.schedulerName,
				"pod", klog.KObj(pod),
				"attempt", attempt+1,
				"maxRetries", maxRetries,
				"err", err)

			// Brief backoff before retry.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
			}
			continue
		}

		// Non-retriable error – stop immediately.
		return err
	}

	return fmt.Errorf("failed to bind pod %s/%s after %d attempts: %w",
		pod.Namespace, pod.Name, maxRetries, lastErr)
}

// IsRunning returns true if the EmbeddedBinder has been started and not stopped.
func (eb *EmbeddedBinder) IsRunning() bool {
	return eb.running.Load()
}

// Config returns the binder's configuration. Primarily for testing.
func (eb *EmbeddedBinder) Config() *EmbeddedBinderConfig {
	return eb.config
}

// SchedulerName returns the name of the associated Scheduler.
func (eb *EmbeddedBinder) SchedulerName() string {
	return eb.schedulerName
}
