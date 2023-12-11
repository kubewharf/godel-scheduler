#!/bin/bash
REPO_ROOT=${REPO_ROOT:-"$(cd "$(dirname "${BASH_SOURCE}")/.." && pwd -P)"}

local_pkg=${LOCAL_PKG_PREFIX:-"github.com/kubewharf/godel-scheduler"}

# the first arg is relative path in repo root
# so we can format only for the file or files the directory
path=${1:-${REPO_ROOT}}

find_files() {
    find ${1:-.} -not \( \( \
        -wholename '*/output' \
        -o -wholename '*/.git/*' \
        -o -wholename '*/vendor/*' \
        -o -name 'bindata.go' \
        -o -name 'datafile.go' \
        -o -name '*.pb.go' \
        -o -name '*.go.bak' \
        \) -prune \) \
        -name '*.go'
}

for go_file in $(find_files ${path}); do
    # sort imports
    diff="$(goimports -format-only -d -local ${local_pkg} ${go_file})"
    if [[ -n "${diff}" ]]; then
        echo "${diff}" >&2
        echo >&2
        echo "Failed to verify format. Please run ./hack/update-format.sh" >&2
        exit 1
    fi
done
