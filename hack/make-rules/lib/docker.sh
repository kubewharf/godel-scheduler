#!/bin/bash

# =========================================================
# update the following variables based on your project.
# =========================================================
# multiple registries for docker push
readonly DOCKER_REGISTRIES=(
    ${DOCKER_REGISTRIES[@]-}
)

# docker build targets
readonly DOCKER_BUILD_TARGETS=(
    ${DOCKER_BUILD_TARGETS[@]-}
)

# Container image prefix and suffix added to targets.
# The final built images are:
#   $[REGISTRY]/$[DOCKER_IMAGE_PREFIX]$[TARGET]$[DOCKER_IMAGE_SUFFIX]:$[VERSION]
#   $[REGISTRY] is an item from $[DOCKER_REGISTRIES], $[TARGET] is the basename from $[DOCKER_BUILD_TARGETS[@]].
readonly DOCKER_IMAGE_PREFIX=${DOCKER_IMAGE_PREFIX:-}
readonly DOCKER_IMAGE_SUFFIX=${DOCKER_IMAGE_SUFFIX:-}

# If not true, scripts will check whether image exists in remote registry.
readonly DOCKER_FORCE_PUSH=${DOCKER_FORCE_PUSH:-true}

# =========================================================
# functions
# =========================================================

docker::normalize_targets() {
    local target
    for target; do
        if [[ ${target} =~ build/ ]]; then
            echo ${target}
        else
            echo "build/"${target}
        fi
    done
}

docker::names_from_targets() {
    local target
    for target; do
        # get base name of target
        echo "${DOCKER_IMAGE_PREFIX}${target##*/}${DOCKER_IMAGE_SUFFIX}" | tr -d " "
    done
}

docker::dockerfiles_from_targets() {
    local target
    for target; do
        echo "${REPO_ROOT}/${target}/Dockerfile"
    done
}

docker::build_images() {
    # Create a sub-shell so that we don't pollute the outer environment
    (
        version::get_version_vars
        local tag=${REPO_DOCKER_TAG:-dirty}

        util::parse_args "$@"
        local -a docker_targets=(${targets[@]-})
        if [[ ${#docker_targets[@]} -eq 0 ]]; then
            docker_targets=("${DOCKER_BUILD_TARGETS[@]}")
        fi
        docker_targets=($(util::normalize_targets build "${docker_targets[@]}"))

        length=${#docker_targets[@]}

        local -a names
        names=($(docker::names_from_targets "${docker_targets[@]}"))

        local -a dockerfiles
        dockerfiles=($(docker::dockerfiles_from_targets "${docker_targets[@]}"))

        log::status "Docker build targets: " "${docker_targets[@]}"
        for ((i = 0; i < ${length}; i++)); do
            local dockerfile="${dockerfiles[$i]}"
            local name="${names[$i]}"
            # TODO use tmp dir
            log::status "Building docker image:" "tag        - ${name}:${tag}" "dockerfile - ${dockerfile}"
            log::status "Docker build progressing: "
            docker build -f "${dockerfile}" -t "${name}:${tag}" ${REPO_ROOT}

            log::status "Tagging docker images:"
            for registry in "${DOCKER_REGISTRIES[@]}"; do
                log::progress "    ${registry}/${name}:${tag}\n"
                docker tag "${name}:${tag}" "${registry}/${name}:${tag}"
            done
            log::status "Remove temporary image:"
            # untag temporary docker image
            docker rmi "${name}:${tag}"
        done
    )
}

docker::push_images() {
    # Create a sub-shell so that we don't pollute the outer environment
    (
        version::get_version_vars
        local tag=${REPO_DOCKER_TAG:-dirty}

        util::parse_args "$@"
        local -a docker_targets=(${targets[@]-})
        if [[ ${#targets[@]} -eq 0 ]]; then
            docker_targets=("${DOCKER_BUILD_TARGETS[@]}")
        fi
        docker_targets=($(docker::normalize_targets "${docker_targets[@]}"))

        length=${#docker_targets[@]}

        local names
        names=($(docker::names_from_targets "${docker_targets[@]}"))

        for ((i = 0; i < ${length}; i++)); do
            local name="${names[$i]}"
            for registry in "${DOCKER_REGISTRIES[@]}"; do
                if [[ ${DOCKER_FORCE_PUSH-} != "true" ]]; then
                    if docker::is_image_exists_in_registry ${registry} ${name} ${tag}; then
                        if ! log::confirm "Docker image [${registry}/${name}:${tag}] already exists in the remote registry.\nDo you want to override it?"; then
                            continue
                        fi
                    fi
                fi
                log::status "Pushing docker image ${registry}/${name}:${tag}"
                docker push "${registry}/${name}:${tag}"
            done
        done
    )
}

docker::index_server() {
    local domain="${1%%/*}"
    echo "https://${domain}/v2/"
}

docker::get_auth_server() {
    local index_server=$1
    auth=$(curl -s -D - ${index_server} | grep Www-Authenticate | cut -d " " -f 3)
    # delete white space
    auth="${auth//[[:space:]]/}"
    # auth: realm="https://auth.docker.io/token",service="registry.docker.io"
    tokens=($(util::split "${auth}" ","))
    for token in "${tokens[@]}"; do
        eval ${token}
    done

}

# docker::get_bearer_token returns a bearer token of registry with scope pull
docker::get_bearer_token() {
    local index_server="$1"
    local repo="$2"

    docker::get_auth_server "${index_server}"

    local url="${realm}?scope=repository:${repo}:pull&service=${service}&client_id=make"

    V=2 log::status "get token from url ${url}"

    token="$(curl -s ${url} | jq ".token" | tr -d \")"
}

# docker::get_manifest takes three parameters,
# index_server: https://cargo.caicloudprivatest.com/v2/
# repo: caicloud/admin
# tag: v0.1.0
docker::get_manifest() {
    local index_server=$1
    local repo=$2
    local tag=$3
    local token=$4

    local url="${index_server}/${repo}/manifests/${tag}"
    errors=$(curl -s -H "Authorization: Bearer ${token}" ${url} | jq ".errors")
    echo errors
}

docker::is_image_exists_in_registry() {
    local registry=$1
    local name=$2
    local tag=$3

    local index_server="$(docker::index_server ${registry})"
    local repo="${registry#*/}/${name}"

    docker::get_bearer_token "${index_server}" "${repo}"
    local url="${index_server}${repo}/manifests/${tag}"

    error_code=$(curl -s -H "Authorization: Bearer ${token}" ${url} | jq ".errors | .[0] | .code")

    if [[ ${error_code-} == "null" ]]; then
        # found: no error
        return 0
    elif [[ "${error_code-}" == "\"MANIFEST_UNKNWN\"" ]]; then
        # not found
        return 1
    else
        log::error_exit "Unexcepted error occurs when getting manifest of image ${registry}/${name}/${tag}"
    fi
}
