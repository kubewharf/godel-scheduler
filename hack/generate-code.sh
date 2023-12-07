#!/bin/bash

set -e
set -o pipefail

GO111MODULE=on go install -mod=vendor k8s.io/code-generator/cmd/{deepcopy-gen,conversion-gen,defaulter-gen}

GOPATH=$(go env GOPATH)

function generateSchedulerConfig() {
echo "Generating scheduler config deepcopy funcs"
"${GOPATH}"/bin/deepcopy-gen --input-dirs \
github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config/v1beta1,\
github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config \
 -O zz_generated.deepcopy \
 --output-base=./ \
 -h "$PWD"/hack/boilerplate.go.txt

echo "Generating scheduler config defaulters"
"${GOPATH}"/bin/defaulter-gen --input-dirs \
github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config/v1beta1,\
github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config \
 -O zz_generated.defaults \
 --output-base=./ \
 -h "$PWD"/hack/boilerplate.go.txt

echo "Generating scheduler config conversions"
"${GOPATH}"/bin/conversion-gen --input-dirs \
github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config/v1beta1 \
 -O zz_generated.conversion \
 --output-base=./ \
 -h "$PWD"/hack/boilerplate.go.txt
}

function generateBinderConfig() {
echo "Generating binder config deepcopy funcs"
"${GOPATH}"/bin/deepcopy-gen --input-dirs \
 github.com/kubewharf/godel-scheduler/pkg/binder/apis/config/v1beta1,\
github.com/kubewharf/godel-scheduler/pkg/binder/apis/config \
 -O zz_generated.deepcopy \
 --output-base=./ \
 -h "$PWD"/hack/boilerplate.go.txt

echo "Generating binder config defaulters"
"${GOPATH}"/bin/defaulter-gen --input-dirs \
 github.com/kubewharf/godel-scheduler/pkg/binder/apis/config/v1beta1,\
github.com/kubewharf/godel-scheduler/pkg/binder/apis/config \
 -O zz_generated.defaults \
 --output-base=./ \
 -h "$PWD"/hack/boilerplate.go.txt

echo "Generating binder config conversions"
"${GOPATH}"/bin/conversion-gen --input-dirs \
 github.com/kubewharf/godel-scheduler/pkg/binder/apis/config/v1beta1 \
 -O zz_generated.conversion \
 --output-base=./ \
 -h "$PWD"/hack/boilerplate.go.txt
}

if [[ "$1" == "scheduler"  || "$1" == "" ]] ; then
    generateSchedulerConfig
fi

if [[ "$1" == "binder"  || "$1" == "" ]] ; then
    generateBinderConfig
fi

echo "
!!!Attention!!!
Code generation finished, you need to copy the generated files to the
pkg/godel-scheduler/apis/config directory. There are some manual changes
in the generated conversion code, be careful to merge the changes.
Don't forget to delete the directory github.com/kubewharf/godel-scheduler before committing.
"
