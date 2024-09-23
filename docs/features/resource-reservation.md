# Resource Reservation User Documentation

This document uses a kind cluster as an example to introduce how to enable and use the resource reservation capability of the Godel Scheduler.

## Prerequisites

Please make sure that the Godel Controller Manager component has been deployed in the kind cluster along with other scheduler components as control plane components.

```shell
$ k get po -n godel-system
NAME                                  READY   STATUS    RESTARTS   AGE
binder-68dc8bdf6b-v7m59               1/1     Running   0          2d20h
controller-manager-5b8fcf5f48-mh2bg   1/1     Running   0          2d20h
dispatcher-5bd97db98f-vw9rv           1/1     Running   0          2d20h
scheduler-794f9b4c67-mnlln            1/1     Running   0          2d20h
```

### controller manager

Controller manager is the control plane component of the Godel Scheduler, responsible for performing the CRUD operations on the Reservation CR.

The optional startup parameter `--reservation-ttl` is used to control the Reservation CR timeout (default value: '60', unit: seconds). If the Reservation CR is not matched after the timeout, the controller manager will delete the Reservation object to notify the scheduler to release the reserved resources.

In addition, the global parameter `--reservation-ttl` can be set manually on the Pod or Deployment object annotation to override the controller manager's global parameter.

```yaml
godel.bytedance.com/reservation-ttl=7200
```

For example, if you configure the above annotation on a Pod or Deployment, the scheduler will automatically release the reserved resources after 24 hours if there is no consumer for the reserved resources.

### scheduler

When starting the scheduler component, set the `--feature-gates=ResourceReservation=true` to enable resource reservation capability.
And the optional startup parameter `--reservation-ttl` is used to set the maximum waiting time for the Reservation CR. 
(When the scheduler watches a Pod with a resource reservation request being deleted, scheduler first marks the resources that need to be reserved in memory. If it does not watch the corresponding Reservation CR being created within the `reservation-ttl` time, it releases the reserved resources.)

### binder

When starting the binder component, set the `--feature-gates=ResourceReservation=true`  to enable resource reservation capability.
And the optional startup parameter `--reservation-ttl` is used to set the maximum waiting time for the Reservation CR. 
(When the binder watches a Pod with a resource reservation request being deleted, binder first marks the resources that need to be reserved in memory. If it does not watch the corresponding Reservation CR being created within the `reservation-ttl` time, it releases the reserved resources.)

## Reserve resources for single Pod

First submit a Pod with reservation request in the annotation:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: res-pod
  labels:
    app: res-pod
  annotations:
    godel.bytedance.com/pod-resource-type: guaranteed
    godel.bytedance.com/pod-launcher: kubelet
    godel.bytedance.com/reservation: "true"                         # reservation requirement
    godel.bytedance.com/reservation-index: "res-pod"              # identifier for back pod
spec:
  schedulerName: godel-scheduler
  containers:
    - name: res-pod
      image: nginx
      imagePullPolicy: IfNotPresent
      resources:
        requests:
          cpu: "1"
          memory: "100Mi"
  restartPolicy: Always
```

- `godel.bytedance.com/reservation: "true"`: asks the scheduler to reserve node resources for the Pod when it is deleted.
- `godel.bytedance.com/reservation-index: "res-pod"`: back pod that can obtain the reserved resource must provide the same reservation index, here it is "res-pod".

Make sure that the Pod can be scheduled.

```shell
$ k get po res-pod -owide

NAME      READY   STATUS              RESTARTS   AGE   IP       NODE                        NOMINATED NODE   READINESS GATES
res-pod   0/1     ContainerCreating   0          17s   <none>   godel-demo-default-worker   <none>           <none>
```

After deleting the Pod, you can see the corresponding Reservation CR object.

```shell
$ k delete po res-pod

$ k get reservation
NAME      NODENAME                    STATUS   AGE
res-pod   godel-demo-default-worker            2024-09-20T09:59:30Z
```

The Reservation CR marks the same node `godel-demo-default-worker` as the original Pod.

Creating a back Pod within the reservation time (with the same `godel.bytedance.com/reservation-index` as the reserved Pod) will be scheduled to the reserved node `godel-demo-default-worker`:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: back-pod
  labels:
    app: back-pod
  annotations:
    godel.bytedance.com/pod-resource-type: guaranteed
    godel.bytedance.com/pod-launcher: kubelet
    godel.bytedance.com/reservation-index: "res-pod"
spec:
  schedulerName: godel-scheduler
  containers:
    - name: back-pod
      image: nginx
      imagePullPolicy: IfNotPresent
      resources:
        requests:
          cpu: "1"
          memory: "100Mi"
  restartPolicy: Always
  ```

- `godel.bytedance.com/reservation-index: "res-pod"` requests the scheduler to prioritize matching the reserved resources of "res-pod". (Note that this rule is not mandatory; if the reserved resources are unavailable or unusable, the scheduler will still allocate other available resources.)

```shell
$ k get po back-pod -owide

NAME       READY   STATUS    RESTARTS   AGE     IP           NODE                        NOMINATED NODE   READINESS GATES
back-pod   1/1     Running   0          2m32s   10.244.2.3   godel-demo-default-worker   <none>           <none>
```


## Reserve Resources for Deployment
Godel Scheduler supports creating resource reservation for deployment. Below is an example of a deployment YAML file with a resource reservation request:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: res-dp
  annotations:
    godel.bytedance.com/reservation: "true"  # reservation requirement
spec:
  replicas: 3
  selector:
    matchLabels:
      name: res-dp
  template:
    metadata:
      labels:
        name: res-dp
      annotations:
        godel.bytedance.com/pod-resource-type: guaranteed
        godel.bytedance.com/pod-launcher: kubelet
    spec:
      terminationGracePeriodSeconds: 0
      schedulerName: godel-scheduler
      containers:
        - name: nginx
          image: nginx:1.14.2
          resources:
            requests:
              cpu: 1
              memory: 500Mi
            limits:
              cpu: 1
              memory: 500Mi
```

- `godel.bytedance.com/reservation: "true"`: Request scheduler to create resource reservation when Pods are deleted (removing the annotation can cancel the resource reservation requirement).

After scaling down the deployment, scheduler will create corresponding Reservation CRs for the deleted Pods, indicating that resource reservation is successful.

```shell
$ k create -f reservation-dp.yaml

$ k get po -o wide
NAME                      READY   STATUS    RESTARTS   AGE    IP           NODE                         NOMINATED NODE   READINESS GATES
res-dp-86cc776f54-h5nlf   1/1     Running   0          2m1s   10.244.2.4   godel-demo-default-worker    <none>           <none>
res-dp-86cc776f54-ndbpg   1/1     Running   0          2m1s   10.244.1.9   godel-demo-default-worker2   <none>           <none>
res-dp-86cc776f54-t5szm   1/1     Running   0          2m1s   10.244.2.5   godel-demo-default-worker    <none>           <none>

$ k scale deploy --replicas=0 res-dp
deployment.apps/res-dp scaled

$ k get reservation  -o wide
NAME                      NODENAME                     STATUS   AGE
res-dp-86cc776f54-h5nlf   godel-demo-default-worker             2024-09-23T03:36:21Z
res-dp-86cc776f54-ndbpg   godel-demo-default-worker2            2024-09-23T03:36:21Z
res-dp-86cc776f54-t5szm   godel-demo-default-worker             2024-09-23T03:36:21Z

```

During the resource reservation period, newly created Pods by scaling up the deployment will be preferentially matched.

```shell
$ k scale deploy --replicas=5 res-dp                                                                                                       
deployment.apps/res-dp scaled

$ k get po
NAME                      READY   STATUS    RESTARTS   AGE
res-dp-86cc776f54-4xhr8   1/1     Running   0          3s
res-dp-86cc776f54-l2rh7   1/1     Running   0          3s
res-dp-86cc776f54-mch4n   1/1     Running   0          3s
res-dp-86cc776f54-s44wb   1/1     Running   0          3s
res-dp-86cc776f54-vpfv5   1/1     Running   0          3s


$ k get po -oyaml | grep placeholder                                                                                                       
godel.bytedance.com/matched-reservation-placeholder: default/res-dp-86cc776f54-h5nlf-placeholder
godel.bytedance.com/matched-reservation-placeholder: default/res-dp-86cc776f54-t5szm-placeholder
godel.bytedance.com/matched-reservation-placeholder: default/res-dp-86cc776f54-ndbpg-placeholder
```

Since the scheduler only reserved resources for the three Pods `res-dp-86cc776f54-h5nlf`, `res-dp-86cc776f54-ndbpg`, and `res-dp-86cc776f54-t5szm`, so there are only 3 Pods can match the reserved resources after scaling up.

If you do not want to reserve resources when scaling down the deployment, you can remove the annotation `godel.bytedance.com/reservation: "true"`.

```shell
# remove annotation
$ k annotate deployment res-dp godel.bytedance.com/reservation-

# scale down
$ k scale deploy --replicas=0 res-dp

$ k get po
No resources found in default namespace.

$ k get reservation
No resources found in default namespace.
```
