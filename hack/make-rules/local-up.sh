#!/bin/bash

# error on exit
set -e

# verbose for debugging
set -x

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
CLUSTER_NAME="${1}"

# create_cluster creates a kind cluster.
# Parameters:
#  - $1: cluster_config, path to a kind config file
function create_cluster() {
  local cluster_config=${1}

  nohup kind delete cluster --name=${CLUSTER_NAME} >> /dev/null 2>&1
  kind create cluster --config="${cluster_config}"
}


# 1. Create the local kind cluster
create_cluster "${REPO_ROOT}"/manifests/quickstart-feature-examples/${CLUSTER_NAME}.yaml

# 2. Load godel docker images into the cluster
kind load docker-image --nodes ${CLUSTER_NAME}-control-plane godel-local:latest --name ${CLUSTER_NAME}

# 3. Use kustomize to generate related CRDs, ClusterRole & Deployments
# KUSTOMIZE_PATH can be overridden to apply a different overlay (e.g. embedded-binder).
KUSTOMIZE_PATH="${KUSTOMIZE_PATH:-${REPO_ROOT}/manifests/base}"
echo "Applying kustomize from: ${KUSTOMIZE_PATH}"
kustomize build "${KUSTOMIZE_PATH}" | kubectl apply -f -

# 4. Deploy Prometheus monitoring stack (if manifests exist).
MONITORING_PATH="${REPO_ROOT}/manifests/monitoring"
if [ -d "${MONITORING_PATH}" ]; then
  echo "Deploying Prometheus monitoring stack..."

  # Pre-pull the Prometheus image and load it into kind to avoid ImagePullBackOff.
  PROM_IMAGE="prom/prometheus:v2.51.0"
  docker pull "${PROM_IMAGE}" 2>/dev/null || true
  kind load docker-image --nodes ${CLUSTER_NAME}-control-plane "${PROM_IMAGE}" --name ${CLUSTER_NAME}

  kustomize build "${MONITORING_PATH}" | kubectl apply -f -
  echo "Prometheus is available at http://localhost:30090 (via kind node port)"
fi
