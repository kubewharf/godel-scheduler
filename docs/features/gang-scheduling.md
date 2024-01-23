# Quickstart - Gang Scheduling

## Introduction

In this quickstart guide, we'll explore Gang Scheduling, a feature that ensures an "all or nothing" approach to scheduling pods.
Gödel scheduler treats all pods under a job (PodGroup) as a unified entity during scheduling attempts.
This approach eliminates scenarios where a job has "partially reserved resources", effectively mitigating resource deadlocks between multiple jobs and making it a valuable tool for managing complex scheduling scenarios in your cluster.
This guide will walk you through setting up and using Gang Scheduling.

## Local Cluster Bootstrap & Installation

If you do not have a local Kubernetes cluster installed with Godel yet, please refer to the [Cluster Setup Guide](kind-cluster-setup.md).

## Related Configurations

Below are the YAML contents and descriptions for the related configuration used in this guide:

### Pod Group Configuration

The Pod Group configuration specifies the minimum number of members (`minMember`) required and the scheduling timeout in seconds (`scheduleTimeoutSeconds`).

```yaml
apiVersion: scheduling.godel.kubewharf.io/v1alpha1
kind: PodGroup
metadata:
  generation: 1
  name: test-podgroup
spec:
  minMember: 2
  scheduleTimeoutSeconds: 300
```

### Pod Configuration

This YAML configuration defines the first child pod (pod-1) within the Pod Group. It includes labels and annotations required for Gang Scheduling.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: pod-1
  labels:
    name: nginx
    # Pods must have this label set
    godel.bytedance.com/pod-group-name: "test-podgroup"
  annotations:
    # Pods must have this annotation set
    godel.bytedance.com/pod-group-name: "test-podgroup"
spec:
  schedulerName: godel-scheduler
  containers:
  - name: test
    image: nginx
    imagePullPolicy: IfNotPresent
```

The second child pod only varies in name.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: pod-2
  labels:
    name: nginx
    # Pods must have this label set
    godel.bytedance.com/pod-group-name: "test-podgroup"
  annotations:
    # Pods must have this annotation set
    godel.bytedance.com/pod-group-name: "test-podgroup"
spec:
  schedulerName: godel-scheduler
  containers:
  - name: test
    image: nginx
    imagePullPolicy: IfNotPresent
```

## Using Gang Scheduling

1. **Create a Pod Group:**

To start using Gang Scheduling, create a Pod Group using the following command:

```console
$ kubectl apply -f manifests/quickstart-feature-examples/gang-scheduling/podgroup.yaml
podgroup.scheduling.godel.kubewharf.io/test-podgroup created

$ kubectl get podgroups
NAME            AGE
test-podgroup   11s
```

2. **Create Child Pod 1 of the Pod Group:**

Now, let's create the first child pod within the Pod Group. 
Keep in mind that due to Gang Scheduling, this pod will initially be in a "Pending" state until the `minMember` requirement of the Pod Group is satisfied. 
Gang Scheduling ensures that pods are not scheduled until the specified number of pods (in this case, 2) is ready to be scheduled together.

Use the following command to create the first child pod:

```console
$ kubectl apply -f manifests/quickstart-feature-examples/gang-scheduling/pod-1.yaml
pod/pod-1 created

$ kubectl get pods
NAME    READY   STATUS    RESTARTS   AGE
pod-1   0/1     Pending   0          18s
```

3. **Create Child Pod 2 of the Pod Group:**

 Now that we have created the first child pod and it's in a "Pending" state, let's proceed to create the second child pod. 
 Both pods will become "Running" simultaneously once the `minMember` requirement of the Pod Group is fulfilled.

Similarly, create the second child pod within the same Pod Group:

```console
$ kubectl apply -f manifests/quickstart-feature-examples/gang-scheduling/pod-2.yaml
pod/pod-2 created

$ kubectl get pods
NAME    READY   STATUS    RESTARTS   AGE
pod-1   1/1     Running   0          24s
pod-2   1/1     Running   0          2s
```

4. **View Pod Group Status:**

You can check the status of the Pod Group using the following command:

```console
$ kubectl get podgroup test-podgroup -o yaml
```

```yaml
apiVersion: scheduling.godel.kubewharf.io/v1alpha1
kind: PodGroup
metadata:
  annotations:
    godel.bytedance.com/podgroup-final-op-lock: binder
    kubectl.kubernetes.io/last-applied-configuration: |
      {"apiVersion":"scheduling.godel.kubewharf.io/v1alpha1","kind":"PodGroup","metadata":{"annotations":{},"generation":1,"name":"test-podgroup","namespace":"default"},"spec":{"minMember":2,"scheduleTimeoutSeconds":300}}
  creationTimestamp: "2024-01-11T00:28:17Z"
  generation: 1
  name: test-podgroup
  namespace: default
  resourceVersion: "722"
  selfLink: /apis/scheduling.godel.kubewharf.io/v1alpha1/namespaces/default/podgroups/test-podgroup
  uid: fe2931c4-fa6b-4770-8ef2-589966fce3f7
spec:
  minMember: 2
  scheduleTimeoutSeconds: 300
status:
  conditions:
  - lastTransitionTime: "2024-01-11T00:28:22Z"
    phase: Pending
    reason: Pending
    status: "True"
  - lastTransitionTime: "2024-01-11T00:28:36Z"
    message: More than 2 pods has been created but not fully scheduled
    phase: PreScheduling
    reason: PreScheduling
    status: "True"
  - lastTransitionTime: "2024-01-11T00:28:36Z"
    message: More than 2 pods has been scheduled
    phase: Scheduled
    reason: Scheduled
    status: "True"
  phase: Scheduled
  scheduleStartTime: "2024-01-11T00:28:37Z"
```
