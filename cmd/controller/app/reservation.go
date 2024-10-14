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

package app

import (
	"context"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/kubewharf/godel-scheduler/pkg/controller"
	"github.com/kubewharf/godel-scheduler/pkg/controller/reservation"
	"github.com/kubewharf/godel-scheduler/pkg/controller/reservation/utils"
)

func startReservationController(ctx context.Context, controllerContext ControllerContext) (controller.Interface, bool, error) {
	godelClient := controllerContext.GodelClientBuilder.ClientOrDie("reservation-controller")
	kubeClient := controllerContext.ClientBuilder.ClientOrDie("reservation-controller")

	ignored := controllerContext.ComponentConfig.ReservationController.IgnoredNamespace
	if len(ignored) > 0 {
		klog.V(4).InfoS("warning: objects will be ignored under these namespaces", "namespaces", strings.Join(ignored, ","))
	}

	podInformer := utils.NewFilteredPodInformer(kubeClient, v1.NamespaceAll, controllerContext.ResyncPeriod(),
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
		utils.WithIgnoredNamespaceList(controllerContext.ComponentConfig.ReservationController.IgnoredNamespace))

	deployInformer := controllerContext.InformerFactory.Apps().V1().Deployments()
	reservationInformer := controllerContext.GodelInformerFactory.Scheduling().V1alpha1().Reservations()
	reservationCheckPeriod := controllerContext.ComponentConfig.ReservationController.ReservationCheckPeriod
	reservationTTL := controllerContext.ComponentConfig.ReservationController.ReservationTTL
	matchedRequestCleanUpTTL := controllerContext.ComponentConfig.ReservationController.MatchedRequestExtraTTL

	go podInformer.Informer().Run(ctx.Done())
	go reservation.NewReservationController(ctx, godelClient, podInformer, deployInformer, reservationInformer,
		reservationCheckPeriod, reservationTTL, matchedRequestCleanUpTTL).Run(ctx, controllerContext.ControllerManagerMetrics)
	return nil, true, nil
}
