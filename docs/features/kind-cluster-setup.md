# Local Gödel Environment Setup with KIND
This guide will walk you through how to set up the Gödel Unified Scheduling system.

## One-Step Cluster Bootstrap & Installation

We provided a quick way to help you try Gödel on your local machine, which will set up a kind cluster locally and deploy necessary crds, clusterrole and rolebindings

### Prerequisites

Please make sure the following dependencies are installed.

- kubectl >= v1.19
- docker >= 19.03
- kind >= v0.17.0
- go >= v1.19.4
- kustomize >= v4.5.7

### 1. Clone the Gödel repo to your machine

```console
$ git clone https://github.com/kubewharf/godel-scheduler
```

### 2. Change to the Gödel directory

```console
$ cd godel
```

### 3. Bootstrap the cluster and install Gödel components

```console
$ make local-up
```

This command will complete the following steps:

1. Build Gödel image locally;
2. Start a Kubernetes cluster using Kind;
3. Installs the Gödel control-plane components on the cluster.


## Manual Installation
If you have an existing Kubernetes cluster, please follow the steps below to install Gödel.

### 1. Build Gödel image
```console
make docker-images
```

### 2. Load Gödel image to your cluster
For example, if you are using Kind
```console
kind load docker-image godel-local:latest --name <cluster-name>
```

### 3. Create Gödel components in the cluster
```console
kustomize build manifests/base/ | kubectl apply -f -
```