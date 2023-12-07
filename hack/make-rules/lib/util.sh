#!/bin/bash

util::ensure_temp_dir() {
    if [[ -z ${REPO_TEMP:-} ]]; then
        REPO_TEMP="$(mktemp -d 2>/dev/null || mktemp -d -t godel.XXXXXX)"
    fi
}

util::command_exists() {
    command -v "$@" >/dev/null 2>&1
}

util::normalize_targets() {
    prefix=${1}
    shift 1
    local target
    for target; do
        if [[ ${target} =~ ${prefix}/ ]]; then
            echo ${target}
        else
            echo ${prefix}"/"${target}
        fi
    done
}

util::parse_args() {
    local arg
    for arg in $@; do
        if [[ "${arg}" == -* ]]; then
            # Assume arguments starting with a dash are flags to pass to go.
            flags+=("${arg}")
        else
            targets+=("${arg}")
        fi
    done
}

# util::split slices input string into all substrings separated by sep
util::split() {
    local IN=$1
    local IFS=$2

    # $IN and $IFS should not be empty
    [[ -n ${IN} && -n ${IFS} ]] || {
        echo ${IN}
        return
    }

    IFS=${IFS} read -ra tokens <<<"$IN"
    echo ${tokens[@]}
}

# Returns go package of this project
# the project must be palced in GOPATH
util::get_go_package() {
    local pkg
    if [[ "${GO111MODULE:-}" == "on" ]] && [[ -f ${REPO_ROOT}/go.mod ]]; then
        # read pkg from mod
        pkg=$(head -1 ${REPO_ROOT}/go.mod | cut -d " " -f 2)
        echo ${pkg}
        return
    fi

    local gopaths=($(util::split "${GOPATH:-}" ":"))
    [[ -n ${gopaths:-} ]] || {
        log::error_exit "!!! No GOPATH set in env"
    }

    pkg=${REPO_ROOT}

    local inside_gopath="false"
    for gopath in ${gopaths[@]}; do
        # delete gopath in pkg
        local gosrc="${gopath%/}/src/"
        if [[ ${pkg} =~ ${gosrc} ]]; then
            inside_gopath=true
            pkg=${pkg#${gosrc}}
        fi
    done

    if [[ ${inside_gopath} == "true" ]]; then
        # delete last "/" in path
        pkg=${pkg%/}
        echo ${pkg}
    elif [[ "${GO111MODULE:-}" == "auto" ]] && [[ -f ${REPO_ROOT}/go.mod ]]; then
        # outside gopath, read pkg from mod
        pkg=$(head -1 ${REPO_ROOT}/go.mod | cut -d " " -f 2)
        echo ${pkg}
    else
        log::error_exit "!!! can not recognize go package"
    fi
}

# This figures out the host platform without relying on golang.  We need this as
# we don't want a golang install to be a prerequisite to building yet we need
# this info to figure out where the final binaries are placed.
util::host_platform() {
    local host_os
    local host_arch
    case "$(uname -s)" in
    Darwin)
        host_os=darwin
        ;;
    Linux)
        host_os=linux
        ;;
    *)
        log::error "Unsupported host OS.  Must be Linux or Mac OS X."
        exit 1
        ;;
    esac

    case "$(uname -m)" in
    x86_64*)
        host_arch=amd64
        ;;
    i?86_64*)
        host_arch=amd64
        ;;
    amd64*)
        host_arch=amd64
        ;;
    aarch64*)
        host_arch=arm64
        ;;
    arm64*)
        host_arch=arm64
        ;;
    arm*)
        host_arch=arm
        ;;
    i?86*)
        host_arch=x86
        ;;
    s390x*)
        host_arch=s390x
        ;;
    ppc64le*)
        host_arch=ppc64le
        ;;
    *)
        log::error "Unsupported host arch. Must be x86_64, 386, arm, arm64, s390x or ppc64le."
        exit 1
        ;;
    esac
    echo "${host_os}/${host_arch}"
}
