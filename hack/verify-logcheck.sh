#!/usr/bin/env bash

REPO_ROOT=${REPO_ROOT:-"$(cd "$(dirname "${BASH_SOURCE}")/.." && pwd -P)"}

if ! command -v logcheck &> /dev/null
then
    echo "logcheck could not be found on your machine, installing sigs.k8s.io/logtools/logcheck@v0.7.0"
    go install sigs.k8s.io/logtools/logcheck@v0.7.0
fi

output=$(logcheck -config ${REPO_ROOT}/hack/logcheck.conf ./... 2>&1 | wc -l)

if [ "${output}" -ne 0 ]; then
    echo "logcheck failed! Please run 'logcheck -config ${REPO_ROOT}/hack/logcheck.conf ./...' for more detail"
    logcheck -config ${REPO_ROOT}/hack/logcheck.conf ./... 2>&1
    exit 1
else
    echo "logcheck passed!"
fi
