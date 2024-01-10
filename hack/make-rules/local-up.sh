#!/bin/bash

# error on exit
set -e

# verbose for debugging
set -x

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"

# create_cluster creates a kind cluster.
# Parameters:
#  - $1: cluster_config, path to a kind config file
function create_cluster() {
  local cluster_config=${1}

  nohup kind delete cluster --name="kind" >> /dev/null 2>&1
  kind create cluster --config="${cluster_config}"
}


# 1. Create the local kind cluster
create_cluster "${REPO_ROOT}"/manifests/quickstart-feature-examples/kind-config.yaml

# 2. Load godel docker images into the cluster
kind load docker-image godel-local:latest

# 3. Use kustomize to generate related CRDs, ClusterRole & Deployments
kustomize build "${REPO_ROOT}"/manifests/base | kubectl apply -f -
