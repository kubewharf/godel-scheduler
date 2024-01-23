# Quickstart - Job Level Affinity
This Quickstart guide provides a step-by-step tutorial on how to effectively use the job-level affinity feature for podgroups, with a focus on both preferred and required affinity types. For comprehensive information about this feature, please consult the Job Level Affinity Design Document.
<TODO Add the design doc when it's ready>

## Local Cluster Bootstrap & Installation

To try out this feature, we would need to set up a few labels for the Kubernetes cluster nodes. We provided a make command for you to boostrap such a cluster locally using KIND.
```
make local-up-labels
```

## Affinity-Related Configurations
### Node
In our sample YAML for KIND, we defined 'mainnet' and 'micronet' as custom node labels. These labels are employed to simulate real-world production environments, specifically regarding the network configurations of nodes.

```
- role: worker
  image: kindest/node:v1.21.1
  labels:
    micronet: 10.76.65.0
    mainnet: 10.76.0.0
```

### PodGroup
In our sample YAML for podgroup, we specified 'podGroupAffinity'. This configuration stipulates that pods belonging to this podgroup should be scheduled on nodes within the same 'mainnet'. Additionally, there's a preference to schedule them on nodes sharing the same 'micronet'.

```
apiVersion: scheduling.godel.kubewharf.io/v1alpha1
kind: PodGroup
metadata:
  generation: 1
  name: nginx
spec:
  affinity:
    podGroupAffinity:
      preferred:
        - topologyKey: micronet
      required:
        - topologyKey: mainnet
  minMember: 10
  scheduleTimeoutSeconds: 3000
```

### Deployment
Specify the podgroup name in pod spec.
```
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx
spec:
  replicas: 10
  selector:
    matchLabels:
      name: nginx
  template:
    metadata:
      name: nginx
      labels:
        name: nginx
        godel.bytedance.com/pod-group-name: "nginx"
      annotations:
        godel.bytedance.com/pod-group-name: "nginx"
    spec:
      schedulerName: godel-scheduler
      containers:
        - name: nginx
          image: nginx
          imagePullPolicy: IfNotPresent
          resources:
            limits:
              cpu: 1
            requests:
              cpu: 1
```

## Use Job Level Affinity in Scheduling
First, let's check out the labels for nodes in the cluster.
```
$ kubectl get nodes --show-labels
NAME                       STATUS   ROLES                  AGE   VERSION   LABELS
godel-demo-labels-control-plane   Ready    control-plane,master   37m   v1.21.1   beta.kubernetes.io/arch=amd64,beta.kubernetes.io/os=linux,kubernetes.io/arch=amd64,kubernetes.io/hostname=godel-demo-labels-control-plane,kubernetes.io/os=linux,node-role.kubernetes.io/control-plane=,node-role.kubernetes.io/master=,node.kubernetes.io/exclude-from-external-load-balancers=
godel-demo-labels-worker          Ready    <none>                 36m   v1.21.1   beta.kubernetes.io/arch=amd64,beta.kubernetes.io/os=linux,kubernetes.io/arch=amd64,kubernetes.io/hostname=godel-demo-labels-worker,kubernetes.io/os=linux,mainnet=10.76.0.0,micronet=10.76.64.0,subCluster=subCluster-a
godel-demo-labels-worker2         Ready    <none>                 36m   v1.21.1   beta.kubernetes.io/arch=amd64,beta.kubernetes.io/os=linux,kubernetes.io/arch=amd64,kubernetes.io/hostname=godel-demo-labels-worker2,kubernetes.io/os=linux,mainnet=10.25.0.0,micronet=10.25.162.0,subCluster=subCluster-b
godel-demo-labels-worker3         Ready    <none>                 36m   v1.21.1   beta.kubernetes.io/arch=amd64,beta.kubernetes.io/os=linux,kubernetes.io/arch=amd64,kubernetes.io/hostname=godel-demo-labels-worker3,kubernetes.io/os=linux,mainnet=10.76.0.0,micronet=10.76.65.0,subCluster=subCluster-a
godel-demo-labels-worker4         Ready    <none>                 36m   v1.21.1   beta.kubernetes.io/arch=amd64,beta.kubernetes.io/os=linux,kubernetes.io/arch=amd64,kubernetes.io/hostname=godel-demo-labels-worker4,kubernetes.io/os=linux,mainnet=10.76.0.0,micronet=10.76.64.0,subCluster=subCluster-a
godel-demo-labels-worker5         Ready    <none>                 36m   v1.21.1   beta.kubernetes.io/arch=amd64,beta.kubernetes.io/os=linux,kubernetes.io/arch=amd64,kubernetes.io/hostname=godel-demo-labels-worker5,kubernetes.io/os=linux,mainnet=10.53.0.0,micronet=10.53.16.0,subCluster=subCluster-a
godel-demo-labels-worker6         Ready    <none>                 36m   v1.21.1   beta.kubernetes.io/arch=amd64,beta.kubernetes.io/os=linux,kubernetes.io/arch=amd64,kubernetes.io/hostname=godel-demo-labels-worker6,kubernetes.io/os=linux,mainnet=10.57.0.0,micronet=10.57.111.0,subCluster=subCluster-b
```
- **godel-demo-labels-worker** and **godel-demo-labels-worker4** share the same 'micronet';
- **godel-demo-labels-worker**, **godel-demo-labels-worker-3**, and **godel-demo-labels-worker4** share the same 'mainnet'.

Second, create the podgroup and deployment.
```
$ kubectl apply -f manifests/quickstart-feature-examples/job-level-affinity/podGroup.yaml
podgroup.scheduling.godel.kubewharf.io/nginx created

$ kubectl apply -f manifests/quickstart-feature-examples/job-level-affinity/deployment.yaml
deployment.apps/nginx created
```

Third, check the scheduling result.
```
$ kubectl get pods -l name=nginx -o wide
NAME                     READY   STATUS    RESTARTS   AGE   IP            NODE                 NOMINATED NODE   READINESS GATES
nginx-68fc9649cc-5pdb7   1/1     Running   0          8s    10.244.2.21   godel-demo-labels-worker    <none>           <none>
nginx-68fc9649cc-b26tk   1/1     Running   0          8s    10.244.2.19   godel-demo-labels-worker    <none>           <none>
nginx-68fc9649cc-bvvx6   1/1     Running   0          8s    10.244.2.18   godel-demo-labels-worker    <none>           <none>
nginx-68fc9649cc-dtxqn   1/1     Running   0          8s    10.244.2.23   godel-demo-labels-worker    <none>           <none>
nginx-68fc9649cc-hh5pr   1/1     Running   0          8s    10.244.3.34   godel-demo-labels-worker4   <none>           <none>
nginx-68fc9649cc-jt8q9   1/1     Running   0          8s    10.244.3.35   godel-demo-labels-worker4   <none>           <none>
nginx-68fc9649cc-l8j2s   1/1     Running   0          8s    10.244.2.20   godel-demo-labels-worker    <none>           <none>
nginx-68fc9649cc-t9fb8   1/1     Running   0          8s    10.244.3.33   godel-demo-labels-worker4   <none>           <none>
nginx-68fc9649cc-vcjm7   1/1     Running   0          8s    10.244.2.17   godel-demo-labels-worker    <none>           <none>
nginx-68fc9649cc-wplt7   1/1     Running   0          8s    10.244.2.22   godel-demo-labels-worker    <none>           <none>
```
The pods have been scheduled to **godel-demo-labels-worker** and **godel-demo-labels-worker4**, which share the same 'micronet'. We achieved this result because the resources are sufficient.

Next, let's try with scheduling a podgroup with minMember equal to 15, with the else of the configuration remains the same.
- In `manifests/quickstart-feature-examples/job-level-affinity/podGroup-2.yaml`, notice the minMember is 15.
```
  minMember: 15
```
- In `manifests/quickstart-feature-examples/job-level-affinity/deployment-2.yaml`, notice the replicas is 15.
```
spec:
  replicas: 15
```

Apply the two yaml files and check the scheduling result
```
# Clean up the env first
$ kubectl delete -f manifests/quickstart-feature-examples/job-level-affinity/deployment.yaml && kubectl delete -f manifests/quickstart-feature-examples/job-level-affinity/podGroup.yaml
deployment.apps "nginx" deleted
podgroup.scheduling.godel.kubewharf.io "nginx" deleted

$ kubectl apply -f manifests/quickstart-feature-examples/job-level-affinity/podGroup-2.yaml
podgroup.scheduling.godel.kubewharf.io/nginx-2 created

$ kubectl apply -f manifests/quickstart-feature-examples/job-level-affinity/deployment-2.yaml
deployment.apps/nginx-2 created

$ kubectl get pods -l name=nginx-2 -o wide
NAME                       READY   STATUS    RESTARTS   AGE   IP            NODE                 NOMINATED NODE   READINESS GATES
nginx-2-68fc9649cc-2l2v7   1/1     Running   0          6s    10.244.6.11   godel-demo-labels-worker3   <none>           <none>
nginx-2-68fc9649cc-6mz78   1/1     Running   0          6s    10.244.2.13   godel-demo-labels-worker    <none>           <none>
nginx-2-68fc9649cc-6nm92   1/1     Running   0          6s    10.244.6.12   godel-demo-labels-worker3   <none>           <none>
nginx-2-68fc9649cc-6qmmx   1/1     Running   0          6s    10.244.2.14   godel-demo-labels-worker    <none>           <none>
nginx-2-68fc9649cc-cfd75   1/1     Running   0          6s    10.244.2.11   godel-demo-labels-worker    <none>           <none>
nginx-2-68fc9649cc-fg87r   1/1     Running   0          6s    10.244.3.28   godel-demo-labels-worker4   <none>           <none>
nginx-2-68fc9649cc-gss27   1/1     Running   0          6s    10.244.3.26   godel-demo-labels-worker4   <none>           <none>
nginx-2-68fc9649cc-hbpwt   1/1     Running   0          6s    10.244.6.15   godel-demo-labels-worker3   <none>           <none>
nginx-2-68fc9649cc-jkdqx   1/1     Running   0          6s    10.244.3.27   godel-demo-labels-worker4   <none>           <none>
nginx-2-68fc9649cc-n498k   1/1     Running   0          6s    10.244.6.9    godel-demo-labels-worker3   <none>           <none>
nginx-2-68fc9649cc-q5h5r   1/1     Running   0          6s    10.244.2.12   godel-demo-labels-worker    <none>           <none>
nginx-2-68fc9649cc-qjsgk   1/1     Running   0          6s    10.244.6.14   godel-demo-labels-worker3   <none>           <none>
nginx-2-68fc9649cc-vdp2v   1/1     Running   0          6s    10.244.6.13   godel-demo-labels-worker3   <none>           <none>
nginx-2-68fc9649cc-vpzlj   1/1     Running   0          6s    10.244.6.10   godel-demo-labels-worker3   <none>           <none>
nginx-2-68fc9649cc-z2ffg   1/1     Running   0          6s    10.244.3.29   godel-demo-labels-worker4   <none>           <none>
```
The pods have been scheduled to **godel-demo-labels-worker**, **godel-demo-labels-worker3** and **godel-demo-labels-worker4**. **godel-demo-labels-worker3** was used here because the resources on worker and worker4 were not sufficient. And worker3 met the requirement of the same mainnet.

Clean up the environment.
```
$ kubectl delete -f manifests/quickstart-feature-examples/job-level-affinity/podGroup-2.yaml && kubectl delete -f manifests/quickstart-feature-examples/job-level-affinity/deployment-2.yaml
podgroup.scheduling.godel.kubewharf.io "nginx-2" deleted
deployment.apps "nginx-2" deleted
```