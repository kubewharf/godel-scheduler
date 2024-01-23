# Quickstart - Preemption

Within the Gödel Scheduling System, Pod preemption emerges as a key feature designed to uphold optimal resource utilization.
If a Pod cannot be scheduled, the Gödel scheduler tries to preempt (evict) lower priority Pods to make scheduling of the pending Pod possible.

This is a Quickstart guide that will walk you through how preemption works in the Gödel Scheduling System.

## Local Cluster Bootstrap & Installation

If you do not have a local Kubernetes cluster installed with Godel yet, please refer to the [Cluster Setup Guide](kind-cluster-setup.md) for creating a local KIND cluster installed with Gödel.


## How Preemption Works

When there is no more resource for scheduling a pending Pod, preemption comes into picture.
Gödel scheduler tries to preempt (evict) lower priority Pods to make scheduling of the pending Pod possible, with a few of protection strategies being respected.

### Quickstart Scenario

To better illustrate the preemption features, let's assume there is one node with less than 8 CPU cores available for scheduling.

**Note:** The `Capacity` and `Allocatable` of worker node **depend on your own Docker resources configuration**. Thus they are not guaranteed to be exactly the same with this guide. To try out this feature locally, you should tune the resources configuration in the example yaml files based on your own setup. For example, the author configured 8 CPU for Docker resources preference, so the worker node has 8 CPU in the guide.
```bash
$ kubectl describe node godel-demo-default-worker   
  Name:               godel-demo-default-worker
  ...
  Capacity:
    cpu:                8
    ...
  Allocatable:
    cpu:                8
    ...
  ...
  Non-terminated Pods:          (2 in total)
    Namespace                   Name                CPU Requests  CPU Limits  Memory Requests  Memory Limits  Age
    ---------                   ----                ------------  ----------  ---------------  -------------  ---
    kube-system                 kindnet-cnvtd       100m (2%)     100m (2%)   50Mi (0%)        50Mi (0%)      3d23h
    kube-system                 kube-proxy-6fh4g    0 (0%)        0 (0%)      0 (0%)           0 (0%)         3d23h
  Allocated resources:
    (Total limits may be over 100 percent, i.e., overcommitted.)
    Resource           Requests   Limits
    --------           --------   ------
    cpu                100m (2%)  100m (2%)
    ...
  Events:              <none>
```

### Basic Preemption

Gödel Scheduling System provides basic preemption features that are comparable with the offering of the Kubernetes scheduler.

#### Priority-based Preemption

1. Create a pod with a lower priority, which requests 6 CPU cores.

   ```yaml
   ---
   apiVersion: scheduling.k8s.io/v1
   kind: PriorityClass
   metadata:
      name: low-priority
      annotations:
         "godel.bytedance.com/can-be-preempted": "true"
   value: 80
   description: "a priority class with low priority that can be preempted"
   ---
   apiVersion: v1
   kind: Pod
   metadata:
      name: nginx-victim
      annotations:
         "godel.bytedance.com/can-be-preempted": "true"
   spec:
      schedulerName: godel-scheduler
      priorityClassName: low-priority
      containers:
         - name: nginx-victim
           image: nginx
           imagePullPolicy: IfNotPresent
           resources:
              limits:
                 cpu: 6
              requests:
                 cpu: 6
   ```

   ```bash
   $ kubectl get pod
     NAME           READY   STATUS    RESTARTS   AGE
     nginx-victim   1/1     Running   0          2s
   ```

   2. Create a pod with a higher priority, which requests 3 CPU core. 

      ```yaml
      ---
      apiVersion: scheduling.k8s.io/v1
      kind: PriorityClass
      metadata:
         name: high-priority
      value: 100
      description: "a priority class with a high priority"
      ---
      apiVersion: v1
      kind: Pod
      metadata:
         name: nginx-preemptor
      spec:
         schedulerName: godel-scheduler
         priorityClassName: high-priority
         containers:
           - name: nginx-preemptor
             image: nginx
             imagePullPolicy: IfNotPresent
             resources:
               limits:
                 cpu: 3
               requests:
                 cpu: 3
      ```
   
       Remember, there is only 8 CPU cores available. So, preemption will be triggered when the high-priority Pod gets scheduled.

      ```bash
      $ kubectl get pod
        NAME              READY   STATUS    RESTARTS   AGE
        nginx-preemptor   1/1     Running   0          18s
   
      $ kubectl get event
        ...
        0s          Normal    PreemptForPodSuccessfully   pod/nginx-preemptor   Pod can be placed by evicting some other pods, nominated node: godel-demo-default-worker, victims: [{Name:nginx-victim Namespace:default UID:b685ef99-20b8-43bb-9576-10d2ca09e2d6}], in node group: []
        ...
      ```
   
       As we can see, the pod with a lower priority was preempted to accommodate the pod with a higher priority.
   
   3. Clean up the environment
   ```bash
   kubectl delete pod nginx-preemptor
   ```

#### Protection with PodDisruptionBudget

1. Create a 3-replica deployment with a lower priority, which requests 6 CPU cores in total. Meanwhile, a PodDisruptionBudget object with minAvailable being set to 3 is also created.
    
   ```yaml
   ---
   apiVersion: scheduling.k8s.io/v1
   kind: PriorityClass
   metadata:
      name: low-priority
      annotations:
         "godel.bytedance.com/can-be-preempted": "true"
   value: 80
   description: "a priority class with low priority that can be preempted"
   ---
   apiVersion: policy/v1
   kind: PodDisruptionBudget
   metadata:
      name: nginx-pdb
   spec:
      minAvailable: 3
      selector:
         matchLabels:
            name: nginx
   ---
   apiVersion: apps/v1
   kind: Deployment
   metadata:
      name: nginx-victim
   spec:
      replicas: 3
      selector:
         matchLabels:
            name: nginx
      template:
         metadata:
            name: nginx-victim
            labels:
               name: nginx
            annotations:
               "godel.bytedance.com/can-be-preempted": "true"
         spec:
            schedulerName: godel-scheduler
            priorityClassName: low-priority
            containers:
               - name: test
                 image: nginx
                 imagePullPolicy: IfNotPresent
                 resources:
                    limits:
                       cpu: 2
                    requests:
                       cpu: 2 
   ```
   
   ```bash
   $ kubectl get pod,deploy
     NAME                                READY   STATUS    RESTARTS   AGE
     pod/nginx-victim-588f6db4bd-r99bq   1/1     Running   0          6s
     pod/nginx-victim-588f6db4bd-vv24v   1/1     Running   0          6s
     pod/nginx-victim-588f6db4bd-xdsht   1/1     Running   0          6s
     
     NAME                           READY   UP-TO-DATE   AVAILABLE   AGE
     deployment.apps/nginx-victim   3/3     3            3           6s
   ```

2. Create a pod with a higher priority, which requests 3 CPU core.

   ```yaml
   ---
   apiVersion: scheduling.k8s.io/v1
   kind: PriorityClass
   metadata:
      name: high-priority
   value: 100
   description: "a priority class with a high priority"
   ---
   apiVersion: v1
   kind: Pod
   metadata:
      name: nginx-preemptor
   spec:
      schedulerName: godel-scheduler
      priorityClassName: high-priority
      containers:
         - name: nginx-preemptor
           image: nginx
           imagePullPolicy: IfNotPresent
           resources:
              limits:
                 cpu: 3
              requests:
                 cpu: 3
   ```
   
   In this case, preemption will not be triggered for scheduling the high-priority Pod above, due to the protection provided by the PodDisruptionBudget object.

   ```bash
   $ kubectl get pod,deploy
     NAME                                READY   STATUS    RESTARTS   AGE
     pod/nginx-preemptor                 0/1     Pending   0          34s
     pod/nginx-victim-588f6db4bd-r99bq   1/1     Running   0          109s
     pod/nginx-victim-588f6db4bd-vv24v   1/1     Running   0          109s
     pod/nginx-victim-588f6db4bd-xdsht   1/1     Running   0          109s
      
     NAME                           READY   UP-TO-DATE   AVAILABLE   AGE
     deployment.apps/nginx-victim   3/3     3            3           109s
   ```
   
    But, if we update the PodDisruptionBudget object by setting the minAvailable field to 2. The preemption will be triggered in the next scheduling cycle for the Pod with high priority.

    ```bash
    $ kubectl get pdb     
      NAME        MIN AVAILABLE   MAX UNAVAILABLE   ALLOWED DISRUPTIONS   AGE
      nginx-pdb   2               N/A               0                     5m22s
    
    $ kubectl get pod,deploy    
      NAME                                READY   STATUS    RESTARTS   AGE
      pod/nginx-preemptor                 1/1     Running   0          6m25s
      pod/nginx-victim-588f6db4bd-p49vg   0/1     Pending   0          3m19s
      pod/nginx-victim-588f6db4bd-r99bq   1/1     Running   0          7m40s
      pod/nginx-victim-588f6db4bd-xdsht   1/1     Running   0          7m40s
      
      NAME                           READY   UP-TO-DATE   AVAILABLE   AGE
      deployment.apps/nginx-victim   2/3     3            2           7m40s
    ```

    Based on the observation above, one replica of the low-priority deployment was preempted.

3. Clean up the environment
   ```bash
   kubectl delete pod nginx-preemptor && kubectl delete deploy nginx-victim && kubectl delete pdb nginx-pdb
   ```

### Gödel-specific Preemption

Apart from the basic preemption functionalities shown above, extra protection behaviors are also honored in Gödel Scheduling System.

#### Preemptibility Annotation

Gödel Scheduling System introduces a customized annotation, `"godel.bytedance.com/can-be-preempted": "true"`,
to enable the preemptibility of Pods. The preemptibility annotation specified either in the Pod object or the PriorityClass object will be honored.
Specifically, only Pods with the preemptibility being enabled can preempted in any cases. Otherwise, no preemption will happen.

1. Create a Pod without the preemptibility being enabled.

   ```yaml
   ---
   apiVersion: scheduling.k8s.io/v1
   kind: PriorityClass
   metadata:
     name: low-priority
   value: 80
   description: "a priority class with low priority"
   ---
   apiVersion: v1
   kind: Pod
   metadata:
     name: nginx-victim
   spec:
     schedulerName: godel-scheduler
     priorityClassName: low-priority
     containers:
       - name: nginx-victim
         image: nginx
         imagePullPolicy: IfNotPresent
         resources:
           limits:
             cpu: 6
           requests:
             cpu: 6
   ```

   ```bash
   $ kubectl get pod
     NAME           READY   STATUS    RESTARTS   AGE
     nginx-victim   1/1     Running   0          2s
   ```
2. Create a Pod with a higher priority.

   ```yaml
   ---
   apiVersion: scheduling.k8s.io/v1
   kind: PriorityClass
   metadata:
     name: high-priority
   value: 100
   description: "a priority class with a high priority"
   ---
   apiVersion: v1
   kind: Pod
   metadata:
     name: nginx-preemptor
   spec:
     schedulerName: godel-scheduler
     priorityClassName: high-priority
     containers:
       - name: nginx-preemptor
         image: nginx
         imagePullPolicy: IfNotPresent
         resources:
           limits:
             cpu: 3
           requests:
             cpu: 3
   ```

   ```bash
   $ kubectl get pod
     NAME              READY   STATUS    RESTARTS   AGE
     nginx-preemptor   0/1     Pending   0          11s
     nginx-victim      1/1     Running   0          2m32s
   ```
   
   In this case, preemption will not happen because the preemptibility is not enabled.   
3. Clean up the environment
   ```bash
   kubectl delete pod nginx-preemptor nginx-victim
   ```

#### Protection Duration

Gödel Scheduling System also supports protecting preemptible Pods from preemption for at least a specified amount of time after it's started up. 
By leveraging the Pod annotation `godel.bytedance.com/protection-duration-from-preemption`, 
users will be able to specify the protection duration in seconds.

1. Create a Pod with a 30-second protection duration. 

   ```yaml
   ---
   apiVersion: scheduling.k8s.io/v1
   kind: PriorityClass
   metadata:
     name: low-priority
     annotations:
       "godel.bytedance.com/can-be-preempted": "true"
   value: 80
   description: "a priority class with low priority that can be preempted"
   ---
   apiVersion: v1
   kind: Pod
   metadata:
     name: nginx-victim
     annotations:
       "godel.bytedance.com/protection-duration-from-preemption": "30"
   spec:
     schedulerName: godel-scheduler
     priorityClassName: low-priority
     containers:
       - name: nginx-victim
         image: nginx
         imagePullPolicy: IfNotPresent
         resources:
           limits:
             cpu: 6
           requests:
             cpu: 6
   ```
   
   ```bash
   $ kubectl get pod
     NAME           READY   STATUS    RESTARTS   AGE
     nginx-victim   1/1     Running   0          3s
   ```

2. Create a Pod with a higher priority.

   ```yaml
   ---
   apiVersion: scheduling.k8s.io/v1
   kind: PriorityClass
   metadata:
     name: high-priority
   value: 100
   description: "a priority class with a high priority"
   ---
   apiVersion: v1
   kind: Pod
   metadata:
     name: nginx-preemptor
   spec:
     schedulerName: godel-scheduler
     priorityClassName: high-priority
     containers:
       - name: nginx-preemptor
         image: nginx
         imagePullPolicy: IfNotPresent
         resources:
           limits:
             cpu: 3
           requests:
             cpu: 3
   ```
   Within the protection duration, preemption will not be triggered. 
   ```bash
   $ kubectl get pod
     NAME              READY   STATUS    RESTARTS   AGE
     nginx-preemptor   0/1     Pending   0          14s
     nginx-victim      1/1     Running   0          26s
   ```
   Beyond that, preemption takes place eventually.
   ```bash
   $ kubectl get pod
     NAME           READY   STATUS    RESTARTS   AGE
     nginx-preemptor   1/1     Running   0          78s
   ```
3. Clean up the environment
   ```bash
   kubectl delete pod nginx-preemptor
   ```
## Wrap-up
In this doc, we share a Quickstart guide about selected functionalities of preemption in Gödel Scheduling System. 
More advanced preemption features/strategies as well as the corresponding technical deep-dives can be expected.  