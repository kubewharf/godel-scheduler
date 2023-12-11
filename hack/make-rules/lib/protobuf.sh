#!/bin/bash

# =========================================================
# update the following variables based on your project.
# =========================================================
# supported protoc lib version
readonly PROTOC_VERSION=${PROTOC_VERSION:-3.10.0}

readonly GOGO_PROTOCBUF_LIB_VERSION=${GOGO_PROTOCBUF_LIB_VERSION:-v1.2.1}

# =========================================================
# functions
# =========================================================

protoc::check::protoc() {
    if ! util::command_exists protoc; then
        echo "1"
        return
    fi

    protoc_version="$(protoc --version | cut -d ' ' -f 2)"
    compare=$(semver::compare_version "${protoc_version}" "${PROTOC_VERSION}")
    if [[ "${compare}" == "0" ]] || [[ "${compare}" == "1" ]]; then
        echo "0"
        return
    fi
    if [[ "${compare}" == "-1" ]]; then
        echo "1"
        return
    fi
    # error
    exit 1

}

protoc::install::protoc() {
    if [[ $(protoc::check::protoc) == "0" ]]; then
        return
    fi

    return
    temp="$(mktemp -d)"
    cd "${temp}" || exit 1
    # install
    PROTOC_ZIP=protoc-${PROTOC_VERSION}-linux-x86_64.zip
    curl -OL "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/$PROTOC_ZIP"
    sudo unzip -o "$PROTOC_ZIP" -d /usr/local bin/protoc
    sudo unzip -o "$PROTOC_ZIP" -d /usr/local 'include/*'
    rm -f "$PROTOC_ZIP"
}
