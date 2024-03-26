#!/bin/bash

# error on exit
set -e

# Main of the script
DOCKERFILE_DIR="docker/"
LOCAL_DOCKERFILE="godel-local.Dockerfile"

echo "Building docker image(s) for ${1}..."

docker container prune -f || true;
docker image prune -f || true

function cleanup_godel_images() {
  for i in $(docker images | grep "${1}" | awk "{print \$3}");
  do
    docker rmi "$i" -f;
  done
}

build_image() {
  local file=${1}
  REPO=$(basename "$file");
  REPO=${REPO%.*};
  REV=$(git log --pretty=format:'%h' -n 1);
  TAG=${REPO}:${REV}
  cleanup_godel_images "${REPO}"
  docker build -t "${TAG}"  -f "$file" ./;
  docker tag "${TAG}" "${REPO}:latest"
}

for file in $(find "$DOCKERFILE_DIR" -name *.Dockerfile);
do
  build_image "${file}"
done;
