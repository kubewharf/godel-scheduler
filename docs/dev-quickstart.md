- [Godel Development QuickStart](#godel-development-quickstart)
  - [Prerequisites](#prerequisites)
  - [Create the Kubernetes](#create-the-kubernetes)
  - [Clone Orchestraion-crds](#clone-orchestraion-crds)
  - [Generate the required CRDs](#generate-the-required-crds)
  - [Install CRDs](#install-crds)
  - [Clone Godel](#clone-godel)
  - [Build Components](#build-components)
  - [Start Godel](#start-godel)
  - [Try it out](#try-it-out)

# Godel Development QuickStart

This tutorial introduces how to create a Godel from the source code for development. For dev purposes, in this tutorial, we will use a kind/minikube cluster.

## Prerequisites
Please install the latest version of kind/minikube and kubectl 

## Create the Kubernetes

```bash
# For Kind
kind create cluster --name=capn

# For Minikube
minikube start
```

## Clone necessary godel-scheduler CRDs
Godel requires some CRDs. Let's install them. 
```bash
CRD_DIR=$(mktemp -d)
git clone git@github.com:kubewharf/godel-scheduler-api.git $CRD_DIR
cd $CRD_DIR
```

## Generate the required CRDs
```bash
make generate-manifests
```

## Install CRDs
```bash
kubectl apply -f manifests/base/crds/node.katalyst.kubewharf.io_customnoderesources.yaml
kubectl apply -f manifests/base/crds/scheduling.godel.kubewharf.io_podgroups.yaml
kubectl apply -f manifests/base/crds/scheduling.godel.kubewharf.io_schedulers.yaml
kubectl apply -f manifests/base/crds/node.godel.kubewharf.io_nmnodes.yaml
```

## Clone Godel
```bash
GODEL_DIR=$(mktemp -d)
git clone git@github.com:kubewharf/godel-scheduler.git $GODEL_DIR
cd $GODEL_DIR
```

## Build Components
```bash
TARGETS=(dispatcher scheduler binder) && for target in ${TARGETS[@]}; do go build -o bin/$target cmd/$target/main.go; done
```

## Start Godel
Run the three components in separate shells
```bash
# run the dispatcher
$GODEL_DIR/bin/dispatcher --leader-elect=false --v=5 --kubeconfig ~/.kube/config

# run the scheduler
$GODEL_DIR/bin/scheduler --leader-elect=false --v=5 --kubeconfig ~/.kube/config

# run the binder
$GODEL_DIR/bin/binder --leader-elect=false --v=5 --kubeconfig ~/.kube/config
```

If everything goes smoothly, you should see a similar output as the following for all three components
```bash
...
I0917 15:28:38.146292   25126 eventhandler.go:322] add scheduler scheduler
I0917 15:28:38.226602   25126 shared_informer.go:253] caches populated
I0917 15:28:38.226627   25126 shared_informer.go:253] caches populated
I0917 15:28:38.226633   25126 shared_informer.go:253] caches populated
...
```
Since we only test locally, the "Warn" message can be safely ignored. 

## Try it out
Let's create a pod and let the godel schedule it
```
kubectl apply -f -<<EOF
apiVersion: v1
kind: Pod
metadata:
  name: nginx1
  labels:
    app: nginx1
  annotations:
    katalyst.kubewharf.io/qos_level: dedicated_cores
    godel.bytedance.com/pod-state: pending
    godel.bytedance.com/pod-launcher: kubelet
spec:
  schedulerName: godel-scheduler
  priorityClassName: system-cluster-critical
  containers:
  - name: nginx1
    image: nginx:alpine
    ports:
    - containerPort: 80
    resources:
      requests:
        cpu: "50m"
EOF
```

After one or two minutes, you should see the pod up and running 🥳
```
$ kubectl get po
NAME     READY   STATUS    RESTARTS   AGE
nginx1   1/1     Running   0          40m
```


