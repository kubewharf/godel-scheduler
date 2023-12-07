/*
Copyright 2019 The Kubernetes Authors.

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

package utils

import (
	"context"
	"fmt"

	v1apps "k8s.io/api/apps/v1"
	v1batch "k8s.io/api/batch/v1"
	v1core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientset "k8s.io/client-go/kubernetes"
)

func deleteResource(c clientset.Interface, kind schema.GroupKind, namespace, name string, options metav1.DeleteOptions) error {
	switch kind {
	case Kind(v1core.SchemeGroupVersion, "Pod"):
		return c.CoreV1().Pods(namespace).Delete(context.TODO(), name, options)
	case Kind(v1core.SchemeGroupVersion, "ReplicationController"):
		return c.CoreV1().ReplicationControllers(namespace).Delete(context.TODO(), name, options)
	case Kind(v1apps.SchemeGroupVersion, "ReplicaSet"):
		return c.AppsV1().ReplicaSets(namespace).Delete(context.TODO(), name, options)
	case Kind(v1apps.SchemeGroupVersion, "Deployment"):
		return c.AppsV1().Deployments(namespace).Delete(context.TODO(), name, options)
	case Kind(v1apps.SchemeGroupVersion, "DaemonSet"):
		return c.AppsV1().DaemonSets(namespace).Delete(context.TODO(), name, options)
	case Kind(v1batch.SchemeGroupVersion, "Job"):
		return c.BatchV1().Jobs(namespace).Delete(context.TODO(), name, options)
	case Kind(v1core.SchemeGroupVersion, "Secret"):
		return c.CoreV1().Secrets(namespace).Delete(context.TODO(), name, options)
	case Kind(v1core.SchemeGroupVersion, "ConfigMap"):
		return c.CoreV1().ConfigMaps(namespace).Delete(context.TODO(), name, options)
	case Kind(v1core.SchemeGroupVersion, "Service"):
		return c.CoreV1().Services(namespace).Delete(context.TODO(), name, options)
	default:
		return fmt.Errorf("Unsupported kind when deleting: %v", kind)
	}
}

func DeleteResourceWithRetries(c clientset.Interface, kind schema.GroupKind, namespace, name string, options metav1.DeleteOptions) error {
	deleteFunc := func() (bool, error) {
		err := deleteResource(c, kind, namespace, name, options)
		if err == nil || apierrors.IsNotFound(err) {
			return true, nil
		}
		if IsRetryableAPIError(err) {
			return false, nil
		}
		return false, fmt.Errorf("Failed to delete object with non-retriable error: %v", err)
	}
	return RetryWithExponentialBackOff(deleteFunc)
}
