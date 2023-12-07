#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

. "$(dirname "${BASH_SOURCE[0]}")/make-rules/lib/init.sh"

cd "${REPO_ROOT}/hack" || exit 1

if ! command -v goimports &> /dev/null
then
    echo "goimports could not be found on your machine, please install it first"
    exit
fi

cd "${REPO_ROOT}" || exit 1

IFS=$'\n' read -r -d '' -a files < <( find . -type f -name '*.go' -not -path "./vendor/*" -not -path "./pkg/client/*" && printf '\0' )

"goimports" -w -local github.com/kubewharf/godel-scheduler/ "${files[@]}"
