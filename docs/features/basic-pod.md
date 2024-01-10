# Quickstart - Basic Pod Scheduling
This Quickstart guide will walk you through how to schedule a pod using the Gödel Scheduling System.

## Local Cluster Bootstrap & Installation

Please refer to [Cluster Setup Guide](kind-cluster-setup.md) for creating a local KIND cluster installed with Gödel.

## Deploy Example Workloads
We provided an example workload which designate godel-schduler as the scheduler.
```console
$ grep schedulerName manifests/quickstart-feature-examples/basic-pod-scheduling/basic-pod-workload.yaml
      schedulerName: godel-scheduler
```

Apply the example workload yaml file and check the scheduling result.
```console
$ kubectl apply -f manifests/quickstart-feature-examples/basic-pod-scheduling/basic-pod-workload.yaml
deployment.apps/basic created

$ kubectl get pods -l app=basic
NAME                     READY   STATUS    RESTARTS   AGE
basic-7db669785b-n5mb4   1/1     Running   0          19s
basic-7db669785b-plrgf   1/1     Running   0          19s
basic-7db669785b-xgnth   1/1     Running   0          19s
```