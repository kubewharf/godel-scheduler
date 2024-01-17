# Quickstart - SubCluster Concurrent Scheduling
This Quickstart guide demonstrates how to implement concurrent scheduling at the SubCluster level. Each SubCluster, defined by node labels, will possess its own distinct scheduling workflow. These workflows will execute simultaneously, ensuring efficient task management across the system.

## Local Cluster Bootstrap & Installation
Please refer to [Cluster Setup Guide](kind-cluster-setup.md) for creating a local KIND cluster installed with Gödel.

## Related Configurations
### Node
In our sample YAML for KIND, we defined 'subCluster' as custom node labels. These labels are employed to simulate real-world production environments, where nodes can be classified by different business scenarios.
```
- role: worker
  image: kindest/node:v1.21.1
  labels:
    subCluster: subCluster-a
- role: worker
  image: kindest/node:v1.21.1
  labels:
    subCluster: subCluster-b
```

### Godel Scheduler Configuration
We defined 'subCluster' as the subClusterKey in the Godel Scheduler Configuration. This is corresponding to the node label key above.
```
apiVersion: godelscheduler.config.kubewharf.io/v1beta1
kind: GodelSchedulerConfiguration
subClusterKey: subCluster
```

### Deployment
In the sample deployment file, one deployment specifies 'subCluster: subCluster-a' in its nodeSelector, while the other deployment specifies 'subCluster: subCluster-b'.
```
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-a
spec:
  ...
    spec:
      schedulerName: godel-scheduler
      nodeSelector:
        subCluster: subCluster-a
      ...
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-b
spec:
  ...
    spec:
      schedulerName: godel-scheduler
      nodeSelector:
        subCluster: subCluster-b
      ...
```

## Concurrent Scheduling Quick Demo
Apply the deployment YAML files. 
```
$ kubectl apply -f manifests/quickstart-feature-examples/concurrent-scheduling/deployment.yaml
deployment.apps/nginx-a created
deployment.apps/nginx-b created
```

Check the scheduler logs.
```
$ kubectl get pods -n godel-system
NAME                          READY   STATUS    RESTARTS   AGE
binder-858c974d4c-bhbtv       1/1     Running   0          123m
dispatcher-5b8f5cf5c6-fcm8d   1/1     Running   0          123m
scheduler-85f4556799-6ltv5    1/1     Running   0          123m

$ kubectl logs scheduler-85f4556799-6ltv5 -n godel-system
...
I0108 22:38:36.183175       1 util.go:90] "Ready to try and schedule the next unit" numberOfPods=1 unitKey="SinglePodUnit/default/nginx-b-69ccdbff54-fvpg6"
I0108 22:38:36.183190       1 unit_scheduler.go:280] "Attempting to schedule unit" switchType="GTSchedule" subCluster="subCluster-b" unitKey="SinglePodUnit/default/nginx-b-69ccdbff54-fvpg6"
I0108 22:38:36.183229       1 util.go:90] "Ready to try and schedule the next unit" numberOfPods=1 unitKey="SinglePodUnit/default/nginx-a-649b85664f-2tjvp"
I0108 22:38:36.183246       1 unit_scheduler.go:280] "Attempting to schedule unit" switchType="GTSchedule" subCluster="subCluster-a" unitKey="SinglePodUnit/default/nginx-a-649b85664f-2tjvp"
I0108 22:38:36.183390       1 unit_scheduler.go:327] "Attempting to schedule unit in this node group" switchType="GTSchedule" subCluster="subCluster-b" unitKey="SinglePodUnit/default/nginx-b-69ccdbff54-fvpg6" nodeGroup="[]"
...
```

From the log snippet above, it's clear that both pods, nginx-a (subCluster: subCluster-a) and nginx-b (subCluster: subCluster-b), are overlapping.