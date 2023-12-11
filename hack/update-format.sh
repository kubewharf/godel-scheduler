#!/bin/bash
REPO_ROOT=${REPO_ROOT:-"$(cd "$(dirname "${BASH_SOURCE}")/.." && pwd -P)"}

GOFUMPT=$(go env GOPATH)/bin/gofumpt;
if [ ! -e ${GOFUMPT} ];
then
  go install mvdan.cc/gofumpt@latest;
fi;

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
        -o -name '*.go.bak' \
        \) -prune \) \
        -name '*.go'
}

for go_file in $(find_files ${path}); do
    echo "==> gofumt: ${go_file}"
    # delete empty line between import ( and )
    sed -i.bak '/import ($/,/)$/{/^$/d}' ${go_file} && rm -rf ${go_file}.bak
    # sort imports
    ${GOFUMPT} -w ${go_file}
done
