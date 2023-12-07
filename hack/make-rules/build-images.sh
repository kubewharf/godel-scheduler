#!/bin/bash

# error on exit
set -e

# verbose for debugging
set -x

# Main of the script
DOCKERFILE_DIR="docker/"
LOCAL_DOCKERFILE="godel-local.Dockerfile"

echo "Building docker image(s) for ${1}..."

docker container prune -f || true;
docker image prune -f || true

build_image() {
  local file=${1}
  REPO=$(basename "$file");
  REPO=${REPO%.*};
  REV=$(git log --pretty=format:'%h' -n 1);
  TAG=${REPO}:${REV}
  docker rmi "${TAG}" || true;
  docker rmi "${REPO}:latest" || true
  docker build -t "${TAG}"  -f "$file" ./;
  docker tag "${TAG}" "${REPO}:latest"
}

for file in $(find "$DOCKERFILE_DIR" -name *.Dockerfile);
do
  build_image "${file}"
done;
