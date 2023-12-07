#!/usr/bin/env bash

# This script checks commonly misspelled English words in all files in the
# working directory by client9/misspell package.
# Usage: `hack/verify-spelling.sh`.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
export REPO_ROOT
source "${REPO_ROOT}/hack/make-rules/lib/init.sh"

# Ensure that we find the binaries we build before anything else.
export GOBIN="${GOPATH}/bin/"
PATH="${GOBIN}:${PATH}"

# Install tools we need
pushd "${REPO_ROOT}/hack/tools" >/dev/null
  # As GOFLAGS may equal to `-mod=vendor`, we must download the modules to vendor or go install will fail.
  go mod vendor
  GO111MODULE=on go install github.com/client9/misspell/cmd/misspell
popd >/dev/null

# Spell checking
# All the skipping files are defined in hack/.spelling_failures
skipping_file="${REPO_ROOT}/hack/.spelling_failures"
failing_packages=$(sed "s| | -e |g" "${skipping_file}")
git ls-files | grep -v -e "${failing_packages}" | xargs misspell -i "Creater,creater,ect" -error -o stderr