# Quickstart - Basic Pod Scheduling
This Quickstart guide will walk you through how to schedule a pod using the Gödel Scheduling System.

## Local Cluster Bootstrap & Installation

If you do not have a local Kubernetes cluster installed with Godel yet, please refer to the [Cluster Setup Guide](kind-cluster-setup.md) for creating a local KIND cluster installed with Gödel.

## Deploy Example Workloads
We provided an example workload which designate godel-schduler as the scheduler.
```console
$ grep schedulerName manifests/quickstart-feature-examples/basic-pod-scheduling/deployment.yaml
      schedulerName: godel-scheduler
```

Apply the example workload yaml file and check the scheduling result.
```console
$ kubectl apply -f manifests/quickstart-feature-examples/basic-pod-scheduling/deployment.yaml
deployment.apps/basic created

$ kubectl get pods -l app=basic
NAME                     READY   STATUS    RESTARTS   AGE
basic-7db669785b-n5mb4   1/1     Running   0          19s
basic-7db669785b-plrgf   1/1     Running   0          19s
basic-7db669785b-xgnth   1/1     Running   0          19s
```