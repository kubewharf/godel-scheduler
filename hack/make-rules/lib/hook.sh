#!/bin/bash

hook::pre-build() {
    target=${1:-}
    if [[ -n "${target}" ]]; then
        hook::__run pre-build "${target}"
    fi
}

hook::post-build() {
    target=${1:-}
    if [[ -n "${target}" ]]; then
        hook::__run post-build "${target}"
    fi
}

hook::__run() {
    phase=${1}
    target=${2}

    if [[ -f "${target}/${phase}" ]]; then
        log::status "Invoke ${phase} hook - ${target}/${phase}"
        # shellcheck source=/dev/null
        source "${target}/${phase}"
    fi
}
