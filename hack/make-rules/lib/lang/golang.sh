#!/bin/bash

# options in this scripts
# LOCAL_BUILD: If set, built on localhost. Otherwise, built in docker.
# GO_BUILD_TARGETS: Incoming variable of targets to build for.
# GO_STATIC_LIBRARIES: Incoming variable of static targets to build for. If set,
#     the targets will be built with CGO_ENABLED=0.
# GO_BUILD_PLATFORMS: Incoming variable of targets platforms to build for.  If unset
#     then just the host architecture is built.
# GO_FASTBUILD: If set to "true", a few of architechtrues are built. .e.g linux/amd64.

# =========================================================
# update the following variables based on your project.
# =========================================================
# targets on cmd
readonly GO_BUILD_TARGETS=(
    ${GO_BUILD_TARGETS[@]-}
)

# static libraries
readonly GO_STATIC_LIBRARIES=(
    ${GO_STATIC_LIBRARIES[@]:-}
)

GO_ONBUILD_IMAGE="${GO_ONBUILD_IMAGE:-golang:1.12-stretch}"

# Gigabytes desired for parallel platform builds. 11 is fairly
# arbitrary, but is a reasonable splitting point for 2015
# laptops-versus-not.
readonly GO_PARALLEL_BUILD_MEMORY=${GO_PARALLEL_BUILD_MEMORY:-4}

# The golang package that we are building.
readonly GO_PACKAGE=${GO_PACKAGE:-$(util::get_go_package)}

# supported minimum go version
readonly MINIMUM_GO_VERSION=${MINIMUM_GO_VERSION:-"go1.12.0"}

# =========================================================
# set up targets binaries platforms
# =========================================================
readonly GO_BUILD_BINARIES=("${GO_BUILD_TARGETS[@]##*/}")

if [[ -n "${GO_BUILD_PLATFORMS:-}" ]]; then
    readonly GO_BUILD_PLATFORMS=(${GO_BUILD_PLATFORMS[@]})
elif [[ "${GO_FASTBUILD:-}" == "true" ]]; then
    readonly GO_BUILD_PLATFORMS=(linux/amd64)
else
    readonly GO_BUILD_PLATFORMS=(
        linux/amd64
        darwin/amd64
    )
fi

readonly GO_ALL_BUILD_TARGETS=(
    "${GO_BUILD_TARGETS[@]-}"
)

readonly GO_ALL_BUILD_BINARIES=(
    "${GO_ALL_BUILD_TARGETS[@]##*/}"
)

# the following directories will not be tested
readonly GO_NOTEST=(
    vendor
    test
    tests
    scripts
    hack
)

# =========================================================
# functions
# =========================================================
golang::is_statically_linked_library() {
    local e
    for e in "${GO_STATIC_LIBRARIES[@]-}"; do [[ "$1" == *"/$e" ]] && return 0; done
    # Allow individual overrides--e.g., so that you can get a static build
    # for inclusion in a container.
    if [ -n "${GO_STATIC_OVERRIDES:+x}" ]; then
        for e in "${GO_STATIC_OVERRIDES[@]}"; do [[ "$1" == *"/$e" ]] && return 0; done
    fi
    return 1
}

# golang::binaries_from_targets take a list of build targets and return the
# full go package to be built
golang::binaries_from_targets() {
    local target
    for target; do
        # If the target starts with what looks like a domain name, assume it has a
        # fully-qualified package name rather than one that needs the Kubernetes
        # package prepended.
        if [[ "${target}" =~ ^([[:alnum:]]+".")+[[:alnum:]]+"/" ]]; then
            echo "${target}"
        else
            echo "${GO_PACKAGE}/${target}"
        fi
    done
}

golang::target_from_binary() {
    echo ${1#${GO_PACKAGE}/}
}

# Asks golang what it thinks the host platform is. The go tool chain does some
# slightly different things when the target platform matches the host platform.
golang::host_platform() {
    echo "$(go env GOHOSTOS)/$(go env GOHOSTARCH)"
}

# Takes the platform name ($1) and sets the appropriate golang env variables
# for that platform.
golang::set_platform_envs() {
    [[ -n ${1-} ]] || {
        log::error_exit "!!! Internal error. No platform set in golang::set_platform_envs"
    }

    export GOOS=${platform%/*}
    export GOARCH=${platform##*/}
    export CGO_ENABLED=${CGO_ENABLED:-}

    # Do not set CC when building natively on a platform, only if cross-compiling from linux/amd64
    if [[ ${CGO_ENABLED} -eq 1 ]] && [[ -z ${CC} ]]; then
        # Dynamic CGO linking for other server architectures than linux/amd64 goes here
        # If you want to include support for more server platforms than these, add arch-specific gcc names here
        case "${platform}" in
        "linux/arm")
            export CGO_ENABLED=1
            export CC=arm-linux-gnueabihf-gcc
            ;;
        "linux/arm64")
            export CGO_ENABLED=1
            export CC=aarch64-linux-gnu-gcc
            ;;
        "linux/ppc64le")
            export CGO_ENABLED=1
            export CC=powerpc64le-linux-gnu-gcc
            ;;
        "linux/s390x")
            export CGO_ENABLED=1
            export CC=s390x-linux-gnu-gcc
            ;;
        esac
    fi
}

golang::unset_platform_envs() {
    unset GOOS
    unset GOARCH
    unset GOROOT
    unset CGO_ENABLED
    unset CC
}

# Ensure the go tool exists and is a viable version.
golang::verify_go_version() {
    if [[ -z "$(which go)" ]]; then
        log::usage_from_stdin <<EOF
Can't find 'go' in PATH, please fix and retry.
See http://golang.org/doc/install for installation instructions.
EOF
        return 2
    fi

    # get go version
    GO_VERSION=$(go version | cut -d " " -f 3)

    local minimum_go_version
    minimum_go_version=${MINIMUM_GO_VERSION}
    if [[ "${minimum_go_version}" != $(echo -e "${minimum_go_version}\n${GO_VERSION}" | sort -s -t. -k 1,1 -k 2,2n -k 3,3n | head -n1) && "${GO_VERSION}" != "devel" ]]; then
        log::usage_from_stdin <<EOF
Detected go version: ${GO_VERSION}.
This project requires ${minimum_go_version} or greater.
Please install ${minimum_go_version} or later.
EOF
        return 2
    fi
}

# golang::setup_env will check that the `go` commands is available in
# ${PATH}. It will also check that the Go version is good enough for the
# Kubernetes build.
#
# Inputs:
#   EXTRA_GOPATH - If set, this is included in created GOPATH
#
# Outputs:
#   env-var GOPATH points to our local output dir
#   env-var GOBIN is unset (we want binaries in a predictable place)
#   env-var GO15VENDOREXPERIMENT=1
#   current directory is within GOPATH
golang::setup_env() {
    golang::verify_go_version

    # Append EXTRA_GOPATH to the GOPATH if it is defined.
    if [[ -n ${EXTRA_GOPATH:-} ]]; then
        export GOPATH="${GOPATH}:${EXTRA_GOPATH}"
    fi

    # Assume that we are now within the GOPATH

    # Set GOROOT so binaries that parse code can work properly.
    export GOROOT=$(go env GOROOT)

    # Unset GOBIN in case it already exists in the current session.
    unset GOBIN

    # This seems to matter to some tools (godep, ginkgo...)
    export GO15VENDOREXPERIMENT=1

    if [[ -e ${REPO_ROOT}/go.mod ]]; then
        GO111MODULE="on"
    fi

    # If the vendor dir exists, we disable go mod
    if [[ -d ${REPO_ROOT}/vendor ]]; then
        V=2 log::status "Disable go mod because the vendor dir exists"
        GO111MODULE="auto"
    fi

    # finally we only need one GOPATH
    export GOPATH="$(golang::recognize_gopath)"
}

golang::recognize_gopath() {
    local gopaths=($(util::split "${GOPATH:-}" ":"))
    [[ -n ${gopaths:-} ]] || {
        log::error_exit "!!! No GOPATH set in env"
    }

    local pkg=${REPO_ROOT}

    for gopath in ${gopaths[@]}; do
        # delete gopath in pkg
        local gosrc="${gopath%/}/src/"
        if [[ "${pkg}" =~ ${gosrc} ]]; then
            echo ${gopath%/}
            return
        fi
    done

    # No GOPATH matches this project, return the first gopath
    echo ${gopaths[0]}
}

# This will take binaries from $GOPATH/bin and copy them to the appropriate
# place in ${GO_OUTPUT_BINDIR}
#
# Ideally this wouldn't be necessary and we could just set GOBIN to
# GO_OUTPUT_BINDIR but that won't work in the face of cross compilation.  'go
# install' will place binaries that match the host platform directly in $GOBIN
# while placing cross compiled binaries into `platform_arch` subdirs.  This
# complicates pretty much everything else we do around packaging and such.
golang::place_bins() {
    local host_platform
    host_platform=$(golang::host_platform)

    # local gopath="$(golang::recognize_gopath)"
    local platform
    for platform in "${GO_BUILD_PLATFORMS[@]}"; do
        # The substitution on platform_src below will replace all slashes with
        # underscores.  It'll transform darwin/amd64 -> darwin_amd64.
        local platform_src="${platform//\//_}"
        if [[ $platform == $host_platform ]]; then
            # rm -f "${THIS_PLATFORM_BIN}"
            log::status "Linking" "${REPO_OUTPUT_BINPATH}/${platform_src}/*" "->" "${REPO_OUTPUT_BINPATH}/*"
            ln -sf ${REPO_OUTPUT_BINPATH}/${platform_src}/* "${REPO_OUTPUT_BINPATH}"
            # optional
            # log::status "Copying" "${REPO_OUTPUT_BINPATH}/${platform_src}/*" "->" "${gopath}/bin/"
            # cp ${REPO_OUTPUT_BINPATH}/${platform_src}/* ${gopath}/bin/
        fi

        # optional: place binaries on gopath/bin/GOOS/GOARCH
        # local full_binpath_src="${gopath}/bin/${platform_src}"
        # if [[ -d "${full_binpath_src}" ]]; then
        # 	mkdir -p "${REPO_OUTPUT_BINPATH}/${platform}"
        # 	find "${full_binpath_src}" -maxdepth 1 -type f -exec \
        # 		rsync -pc {} "${REPO_OUTPUT_BINPATH}/${platform}" \;
        # fi
    done
}

golang::fallback_if_stdlib_not_installable() {
    local go_root_dir=$(go env GOROOT)
    local go_host_os=$(go env GOHOSTOS)
    local go_host_arch=$(go env GOHOSTARCH)
    local cgo_pkg_dir=${go_root_dir}/pkg/${go_host_os}_${go_host_arch}_cgo

    if [ -e ${cgo_pkg_dir} ]; then
        return 0
    fi

    if [ -w ${go_root_dir}/pkg ]; then
        return 0
    fi

    log::status "+++ Warning: stdlib pkg with cgo flag not found."
    log::status "+++ Warning: stdlib pkg cannot be rebuilt since ${go_root_dir}/pkg is not writable by $(whoami)"
    log::status "+++ Warning: Make ${go_root_dir}/pkg writable for $(whoami) for a one-time stdlib install, Or"
    log::status "+++ Warning: Rebuild stdlib using the command 'CGO_ENABLED=0 go install -a -installsuffix cgo std'"
    log::status "+++ Falling back to go build, which is slower"

    local_build=true
}

# Try and replicate the native binary placement of go install without
# calling go install.
golang::output_filename_for_binary() {
    local binary=$1
    local platform=$2
    local output_path="${REPO_OUTPUT_BINPATH}"
    # place binary in place like project_root/bin/darwin_amd64
    output_path="${output_path}/${platform//\//_}"

    local bin=$(basename "${binary}")
    if [[ ${GOOS} == "windows" ]]; then
        bin="${bin}.exe"
    fi
    echo "${output_path}/${bin}"
}

golang::build_binaries_for_platform() {
    local platform=$1
    local local_build=${2-}

    local -a statics=()
    local -a nonstatics=()
    local -a tests=()

    log::status "Go compiling environment variables:" \
        "GOOS=\"${GOOS-}\"" \
        "GOARCH=\"${GOARCH-}\"" \
        "GOROOT=\"${GOROOT-}\"" \
        "GOPATH=\"${GOPATH}\"" \
        "CGO_ENABLED=\"${CGO_ENABLED-}\"" \
        "CC=\"${CC-}\"" \
        "GO111MODULE=\"${GO111MODULE:-}\"" \
        "WORK_DIR=\"${REPO_ROOT}\"" \
        "VERSION=\"${REPO_GIT_VERSION}\""

    # V=3 log::status "Go compiling flags:" \
    #     "goflags: \"${goflags[*]}\"" \
    #     "gogcflags: \"${gogcflags[*]}\"" \
    #     "goldflags: \"${goldflags[*]}\""

    for binary in "${binaries[@]}"; do
        reg='.test$'
        if [[ "${binary}" =~ $reg ]]; then
            tests+=($binary)
        elif golang::is_statically_linked_library "${binary}"; then
            statics+=($binary)
        else
            nonstatics+=($binary)
        fi
    done

    if [[ "${#statics[@]}" != 0 ]]; then
        golang::fallback_if_stdlib_not_installable
    fi

    # compiling
    log::status "Go compile progressing:"
    if [[ "${local_build:-}" == "true" ]]; then
        # use local go build
        for test in "${tests[@]:+${tests[@]}}"; do
            local outfile=$(golang::output_filename_for_binary "${test}" "${platform}")
            local testpkg="$(dirname ${test})"
            go test \
                "${goflags[@]:+${goflags[@]}}" \
                -gcflags "${gogcflags}" \
                -ldflags "${goldflags}" \
                -o "${outfile}" \
                "${testpkg}"
        done

        log::progress "    "
        for binary in "${statics[@]:+${statics[@]}}"; do
            local outfile=$(golang::output_filename_for_binary "${binary}" "${platform}")
            CGO_ENABLED=0 go build -o "${outfile}" \
                "${goflags[@]:+${goflags[@]}}" \
                -gcflags "${gogcflags}" \
                -ldflags "${goldflags}" \
                "${binary}"
            log::progress "*"
        done
        for binary in "${nonstatics[@]:+${nonstatics[@]}}"; do
            local outfile=$(golang::output_filename_for_binary "${binary}" "${platform}")
            go build -o "${outfile}" \
                "${goflags[@]:+${goflags[@]}}" \
                -gcflags "${gogcflags}" \
                -ldflags "${goldflags}" \
                "${binary}"
            log::progress "*"
        done
        log::progress "\n"
    else
        # use docker build
        for test in "${tests[@]:+${tests[@]}}"; do
            local outfile=$(golang::output_filename_for_binary "${test}" "${platform}")
            local testpkg="$(dirname ${test})"
            docker run --rm \
                -v ${GOPATH}/pkg:${GOPATH}/pkg \
                -v ${REPO_ROOT}:${REPO_ROOT} \
                -w ${REPO_ROOT} \
                -e GOOS=${GOOS} \
                -e GOARCH=${GOARCH} \
                -e GOPATH=${GOPATH} \
                -e CGO_ENABLED=${CGO_ENABLED} \
                -e GO111MODULE=${GO111MODULE} \
                ${GO_ONBUILD_IMAGE} \
                go test -c -o "${outfile}" \
                "${goflags[@]:+${goflags[@]}}" \
                -gcflags "${gogcflags}" \
                -ldflags "${goldflags}" \
                "${testpkg}"
        done

        log::progress "    "
        for binary in "${statics[@]:+${statics[@]}}"; do
            local outfile=$(golang::output_filename_for_binary "${binary}" "${platform}")
            docker run --rm \
                -v ${GOPATH}/pkg:${GOPATH}/pkg \
                -v ${REPO_ROOT}:${REPO_ROOT} \
                -w ${REPO_ROOT} \
                -e GOOS=${GOOS} \
                -e GOARCH=${GOARCH} \
                -e GOPATH=${GOPATH} \
                -e CGO_ENABLED=0 \
                -e GO111MODULE=${GO111MODULE} \
                ${GO_ONBUILD_IMAGE} \
                go build -o "${outfile}" \
                "${goflags[@]:+${goflags[@]}}" \
                -gcflags "${gogcflags}" \
                -ldflags "${goldflags}" \
                "${binary}"
            log::progress "*"
        done
        for binary in "${nonstatics[@]:+${nonstatics[@]}}"; do
            local outfile=$(golang::output_filename_for_binary "${binary}" "${platform}")
            docker run --rm \
                -v ${GOPATH}/pkg:${GOPATH}/pkg \
                -v ${REPO_ROOT}:${REPO_ROOT} \
                -w ${REPO_ROOT} \
                -e GOOS=${GOOS} \
                -e GOARCH=${GOARCH} \
                -e GOPATH=${GOPATH} \
                -e CGO_ENABLED=${CGO_ENABLED} \
                -e GO111MODULE=${GO111MODULE} \
                ${GO_ONBUILD_IMAGE} \
                go build -o "${outfile}" \
                "${goflags[@]:+${goflags[@]}}" \
                -gcflags "${gogcflags}" \
                -ldflags "${goldflags}" \
                "${binary}"
            log::progress "*"
        done
        log::progress "\n"
    fi
}

# Return approximate physical memory available in gigabytes.
golang::get_physmem() {
    local mem

    case "$(go env GOHOSTOS)" in
    "linux")
        # Linux kernel version >=3.14, in kb
        if mem=$(grep MemAvailable /proc/meminfo | awk '{ print $2 }'); then
            echo $((${mem} / 1048576))
            return
        fi

        # Linux, in kb
        if mem=$(grep MemTotal /proc/meminfo | awk '{ print $2 }'); then
            echo $((${mem} / 1048576))
            return
        fi
        ;;
    "darwin")
        # OS X, in bytes. Note that get_physmem, as used, should only ever
        # run in a Linux container (because it's only used in the multiple
        # platform case, which is a Dockerized build), but this is provided
        # for completeness.
        if mem=$(sysctl -n hw.memsize 2>/dev/null); then
            echo $((${mem} / 1073741824))
            return
        fi
        ;;
    esac

    # If we can't infer it, just give up and assume a low memory system
    echo 1
}

# Build binaries targets specified
#
# Input:
#   $@ - targets and go flags.  If no targets are set then all binaries targets
#     are built.
#   GO_BUILD_PLATFORMS - Incoming variable of targets to build for.  If unset
#     then just the host architecture is built.
golang::build_binaries() {
    # Create a sub-shell so that we don't pollute the outer environment
    (
        # Check for `go` binary and set ${GOPATH}.
        golang::setup_env
        version::get_version_vars

        local local_build=${LOCAL_BUILD-}
        local host_platform
        host_platform=$(golang::host_platform)
        V=2 log::status "Local host go version:" "${GO_VERSION} - ${host_platform}"

        # Use eval to preserve embedded quoted strings.
        local goflags goldflags gogcflags
        eval "goflags=(${GOFLAGS:-})"
        goldflags="${GOLDFLAGS:-} $(version::ldflags)"
        gogcflags="${GOGCFLAGS:-}"

        util::parse_args "$@"
        local -a gotargets=(${targets[@]-})
        goflags+=(${flags[@]-})

        if [[ ${#gotargets[@]} -eq 0 ]]; then
            gotargets=("${GO_ALL_BUILD_TARGETS[@]}")
        fi
        gotargets=($(util::normalize_targets cmd "${gotargets[@]}"))

        local -a platforms=(${GO_BUILD_PLATFORMS[@]-})
        if [[ ${#platforms[@]} -eq 0 ]]; then
            platforms=("${host_platform}")
        fi

        local binaries
        binaries=($(golang::binaries_from_targets "${gotargets[@]}"))

        local parallel=false
        if [[ ${#platforms[@]} -gt 1 ]]; then
            local gigs
            gigs=$(golang::get_physmem)

            if [[ ${gigs} -ge ${GO_PARALLEL_BUILD_MEMORY} ]]; then
                V=2 log::status "Multiple platforms requested and available Memory ${gigs}G >= threshold ${GO_PARALLEL_BUILD_MEMORY}G, building platforms in parallel"
                parallel=true
            else
                V=2 log::status "Multiple platforms requested, but available Memory ${gigs}G < threshold ${GO_PARALLEL_BUILD_MEMORY}G, building platforms in serial"
                parallel=false
            fi
        fi

        local build_on="in container - ${GO_ONBUILD_IMAGE}"
        if [[ ${local_build:-} == "true" ]]; then
            build_on="on localhost - ${host_platform}"
        fi

        # log::status "Hooks pre-build progressing:"
        for target in "${gotargets[@]}"; do
            hook::pre-build ${target}
        done

        log::status "Go compiling environment:" "${build_on}"
        log::status "Go cross compiling target platforms:" "${platforms[@]}"
        log::status "Go compiling modules:" "${binaries[@]}"
        if [[ "${parallel}" == "true" ]]; then
            log::status "Building go modules in parallel (output will appear in a burst when complete)"
            local platform
            for platform in "${platforms[@]}"; do
                (
                    golang::set_platform_envs "${platform}"
                    log::status "Go compiling started: ${platform}"
                    golang::build_binaries_for_platform ${platform} ${local_build:-}
                    log::status "Go compiling finished: ${platform}"
                ) &>"/tmp//${platform//\//_}.build" &
            done

            local fails=0
            for job in $(jobs -p); do
                wait ${job} || let "fails+=1"
            done

            for platform in "${platforms[@]}"; do
                cat "/tmp//${platform//\//_}.build"
            done

            [[ ${fails} -gt 0 ]] && exit ${fails}
        else
            for platform in "${platforms[@]}"; do
                (
                    golang::set_platform_envs "${platform}"
                    golang::build_binaries_for_platform ${platform} ${local_build:-}
                )
            done
        fi

        # log::status "Hooks post-build progressing:"
        for target in "${gotargets[@]}"; do
            hook::post-build ${target}
        done

    )
}

golang::filter_tests() {
    local -a IN=($1)
    local -a exceptions=($2)
    for pkg in "${IN[@]}"; do
        local skip=
        for exception in "${exceptions[@]}"; do
            if [[ "${pkg}" =~ /${exception}(/?|$) ]]; then
                skip="true"
                break
            fi
        done

        if [[ ${skip} != "true" ]]; then
            echo "${pkg}"
        fi
    done

}

# Test all pkg except vendor, test, tests, scripts, hack
#
# Input:
#   $@ - go flags.
#   GO_BUILD_PLATFORMS - Incoming variable of targets to build for.  If unset
#     then just the host architecture is built.
#   GO_TEST_EXCEPTIONS - Incoming variable of pkgs to test for.  If unset
#     then all pkgs are tested.
golang::unittest() {
    # Create a sub-shell so that we don't pollute the outer environment
    (
        # Check for `go` binary and set ${GOPATH}.
        golang::setup_env
        version::get_version_vars

        local local_build=${LOCAL_BUILD-}
        local host_platform
        host_platform=$(golang::host_platform)

        V=2 log::status "Local host go version:" "${GO_VERSION} - ${host_platform}"

        # Use eval to preserve embedded quoted strings.
        local goflags goldflags gogcflags
        eval "goflags=(${GOFLAGS:-})"
        goldflags="${GOLDFLAGS:-} $(version::ldflags)"
        gogcflags="${GOGCFLAGS:-}"

        util::parse_args "$@"
        goflags+=(${flags[@]-})

        local -a targets=($(go list ./...))
        local -a exceptions=(${GO_NOTEST[@]-})
        exceptions+=(${GO_TEST_EXCEPTIONS[@]-})

        # NOTE: Using "${array[*]}" here is correct.  [@] becomes distinct words (in
        # bash parlance).
        targets=($(golang::filter_tests "${targets[*]}" "${exceptions[*]}"))
        # add .test suffix
        targets=(${targets[@]/%/\/unittest.test})

        local binaries
        binaries=($(golang::binaries_from_targets "${targets[@]}"))

        local -a platform=${host_platform}

        local build_on="in Docker [${GO_ONBUILD_IMAGE}]"
        if [[ ${local_build:-} == "true" ]]; then
            build_on="on localhost [${host_platform}]"
        fi

          log::status "Testing environment:" "${build_on}"
          log::status "Testing go targets for ${platform}:" "${targets[@]}"
          (
              golang::set_platform_envs "${platform}"
              golang::build_binaries_for_platform ${platform} ${local_build:-}
          )
    )
}

golang::install::protoc_gen() {
    GO111MODULE="on"
    go mod tidy
    dir=$(go list -f '{{ .Dir }}' -m github.com/gogo/protobuf)
    cd "${dir}/protoc-gen-gogo" && go install .

    cd "${REPO_ROOT}" || exit 1

    go get -v github.com/mwitkow/go-proto-validators/protoc-gen-govalidators
    go get -v github.com/grpc-ecosystem/grpc-gateway/protoc-gen-grpc-gateway
    go get -v github.com/grpc-ecosystem/grpc-gateway/protoc-gen-swagger

    go mod tidy
}

readonly PROTOC_GOOUT_PARAMETERS="Mgoogle/protobuf/timestamp.proto=github.com/gogo/protobuf/types,\
Mgoogle/protobuf/duration.proto=github.com/gogo/protobuf/types,\
Mgoogle/protobuf/empty.proto=github.com/gogo/protobuf/types,\
Mgoogle/api/annotations.proto=github.com/gogo/googleapis/google/api,\
Mgoogle/protobuf/field_mask.proto=github.com/gogo/protobuf/types"

golang::protobuf::generate() {
    # Create a sub-shell so that we don't pollute the outer environment
    (
        # Check for `go` binary and set ${GOPATH}.
        golang::setup_env
        version::get_version_vars

        protoc::install::protoc
        golang::install::protoc_gen

        # make vendored copy of dependencies
        go mod vendor

        local protoflags="-I ./ -I vendor/ -I vendor/github.com/gogo/googleapis -I vendor/github.com/grpc-ecosystem/grpc-gateway"

        outputs=($(util::split "${1}" ","))
        proto_dir="$2"

        for out in "${outputs[@]}"; do
            if [[ ${out} == "gogo" ]]; then
                protoflags="${protoflags} \
                --gogo_out=plugins=grpc,paths=source_relative,${PROTOC_GOOUT_PARAMETERS}:."
            fi
            if [[ ${out} == "govalidators" ]]; then
                protoflags="${protoflags} \
                --govalidators_out=gogoimport=true,paths=source_relative,${PROTOC_GOOUT_PARAMETERS}:."
            fi
            if [[ ${out} == "grpc-gateway" ]]; then
                protoflags="${protoflags} \
                --grpc-gateway_out=paths=source_relative,${PROTOC_GOOUT_PARAMETERS}:."
            fi
            if [[ ${out} == "swagger" ]]; then
                protoflags="${protoflags} \
                --swagger_out=."
            fi
        done

        protoc ${protoflags} ${proto_dir}/*.proto

        # Workaround for https://github.com/grpc-ecosystem/grpc-gateway/issues/229.
        find "${proto_dir}" -type f -name "*.pb.gw.go" -exec sed -i.bak 's/empty.Empty/types.Empty/g' {} \;
        find "${proto_dir}" -type f -name "*.pb.gw.go.bak" -exec rm -rf {} \;

        rm -rf vendor
    )
}
