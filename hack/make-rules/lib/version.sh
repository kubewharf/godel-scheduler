#!/bin/bash

# -----------------------------------------------------------------------------
# Version management helpers.  These functions help to set, save and load the
# following variables:
#
#    REPO_GIT_COMMIT - The git commit id corresponding to this
#          source code.
#    REPO_GIT_TREE_STATE - "clean" indicates no changes since the git commit id
#        "dirty" indicates source code changes after the git commit id
#        "archive" indicates the tree was produced by 'git archive'
#    REPO_GIT_VERSION - "vX.Y" used to indicate the last release version.
#    REPO_GIT_REMOTE - The git remote origin url.

# Grovels through git to set a set of env variables.
#
# If REPO_GIT_VERSION_FILE, this function will load from that file instead of
# querying git.
version::get_version_vars() {
    if ! version::is_git_repo; then
        REPO_GIT_COMMIT='unknown'
        REPO_GIT_TREE_STATE='unknown'
        REPO_GIT_VERSION="${VERSION:-v0.0.0}"
        REPO_GIT_REMOTE='unknown'
        return
    fi

    if [[ -n ${REPO_GIT_VERSION_FILE-} ]]; then
        version::load_version_vars "${REPO_GIT_VERSION_FILE}"
        return
    fi

    # If the project source was exported through git archive, then
    # we likely don't have a git tree, but these magic values may be filled in.
    # if [[ '$Format:%%$' == "%" ]]; then
    #     REPO_GIT_COMMIT='$Format:%H$'
    #     REPO_GIT_TREE_STATE="archive"
    #     # When a 'git archive' is exported, the '$Format:%D$' below will look
    #     # something like 'HEAD -> release-1.8, tag: v1.8.3' where then 'tag: '
    #     # can be extracted from it.
    #     if [[ '$Format:%D$' =~ "tag:\ (v[^ ]+)" ]]; then
    #         REPO_GIT_VERSION="${BASH_REMATCH[1]}"
    #     fi
    # fi

    local git=(git --work-tree "${REPO_ROOT}")

    if [[ -z ${REPO_GIT_REMOTE-} ]]; then
        REPO_GIT_REMOTE="$("${git[@]}" remote get-url origin 2>/dev/null)"
    fi

    if [[ -n ${REPO_GIT_COMMIT-} ]] || REPO_GIT_COMMIT=$("${git[@]}" rev-parse "HEAD^{commit}" 2>/dev/null); then
        if [[ -z ${REPO_GIT_TREE_STATE-} ]]; then
            # Check if the tree is dirty.  default to dirty
            if git_status=$("${git[@]}" status --porcelain 2>/dev/null) && [[ -z ${git_status} ]]; then
                REPO_GIT_TREE_STATE="clean"
            else
                REPO_GIT_TREE_STATE="dirty"
            fi
        fi

        REPO_GIT_COMMIT_SHORT=$("${git[@]}" rev-parse --short=7 "HEAD^{commit}" 2>/dev/null)

        # Use git describe to find the version based on annotated tags.
        if [[ -n ${REPO_GIT_VERSION-} ]] || REPO_GIT_VERSION=$("${git[@]}" describe --tags --abbrev=7 "${REPO_GIT_COMMIT}^{commit}" 2>/dev/null); then
            # '+' can not be used in docker tag
            REPO_DOCKER_TAG=${REPO_GIT_VERSION}
            # This translates the "git describe" to an actual semver.org
            # compatible semantic version that looks something like this:
            #   v1.1.0-alpha.0.6+84c76d1142ea4d
            #
            # TODO: We continue calling this "git version" because so many
            # downstream consumers are expecting it there.
            DASHES_IN_VERSION=$(echo "${REPO_GIT_VERSION}" | sed "s/[^-]//g")
            if [[ "${DASHES_IN_VERSION}" == "---" ]]; then
                # We have distance to subversion (v1.1.0-subversion-1-gCommitHash)
                REPO_GIT_VERSION=$(echo "${REPO_GIT_VERSION}" | sed "s/-\([0-9]\{1,\}\)-g\([0-9a-f]\{14\}\)$/.\1\+\2/")
                REPO_DOCKER_TAG=$(echo "${REPO_DOCKER_TAG}" | sed "s/-\([0-9]\{1,\}\)-g\([0-9a-f]\{14\}\)$/.\1\-\2/")
            elif [[ "${DASHES_IN_VERSION}" == "--" ]]; then
                # We have distance to base tag (v1.1.0-1-gCommitHash)
                REPO_GIT_VERSION=$(echo "${REPO_GIT_VERSION}" | sed "s/-g\([0-9a-f]\{14\}\)$/+\1/")
                REPO_DOCKER_TAG=$(echo "${REPO_DOCKER_TAG}" | sed "s/-g\([0-9a-f]\{14\}\)$/-\1/")
            fi

        fi

        if [[ -n ${VERSION-} ]]; then
            # alias VERSION to REPO_GIT_VERSION
            # version is set by user
            REPO_GIT_VERSION=${VERSION}
            REPO_DOCKER_TAG=${VERSION}
        elif [[ -z ${REPO_GIT_VERSION-} ]]; then
            # no tags found, generate version from commit
            REPO_GIT_VERSION="v0.0.0-${REPO_GIT_COMMIT_SHORT}"
            REPO_DOCKER_TAG="v0.0.0-${REPO_GIT_COMMIT_SHORT}"
        fi

        # append -dirty if if necessary
        if [[ "${REPO_GIT_TREE_STATE}" == "dirty" ]] && [[ ! "${REPO_GIT_VERSION}" =~ -dirty$ ]]; then
            # git describe --dirty only considers changes to existing files, but
            # that is problematic since new untracked .go files affect the build,
            # so use our idea of "dirty" from git status instead.
            REPO_GIT_VERSION+="-dirty"
            REPO_DOCKER_TAG+="-dirty"
        fi
    fi
}

version::is_git_repo() {
    # [[ -d ${REPO_ROOT}/.git ]]
    git rev-parse --is-inside-work-tree >/dev/null 2>&1
}

# Saves the environment flags to $1
version::save_version_vars() {
    local version_file=${1-}
    [[ -n ${version_file} ]] || {
        echo "!!! Internal error. No file specified in version::save_version_vars"
        return 1
    }

    cat <<EOF >"${version_file}"
REPO_GIT_COMMIT='${REPO_GIT_COMMIT-}'
REPO_GIT_TREE_STATE='${REPO_GIT_TREE_STATE-}'
REPO_GIT_VERSION='${REPO_GIT_VERSION-}'
REPO_GIT_REMOTE='${REPO_GIT_REMOTE}'
EOF
}

# Loads up the version variables from file $1
version::load_version_vars() {
    local version_file=${1-}
    [[ -n ${version_file} ]] || {
        echo "!!! Internal error. No file specified in version::load_version_vars"
        return 1
    }

    source "${version_file}"
}

version::ldflag() {
    local key=${1}
    local val=${2}

    echo "-X github.com/kubewharf/godel-scheduler/pkg/version.${key}=${val}"
}

# Prints the value that needs to be passed to the -ldflags parameter of go build
# in order to set the project based on the git tree status.
# IMPORTANT: if you update any of these, also update the lists in
# pkg/version/def.bzl and hack/print-workspace-status.sh.
version::ldflags() {
    version::get_version_vars
    local buildDate=
    [[ -z ${SOURCE_DATE_EPOCH-} ]] || buildDate="--date=@${SOURCE_DATE_EPOCH}"
    local -a ldflags=($(version::ldflag "buildDate" "$(date ${buildDate} -u +'%Y-%m-%dT%H:%M:%SZ')"))
    if [[ -n ${REPO_GIT_COMMIT-} ]]; then
        ldflags+=($(version::ldflag "gitCommit" "${REPO_GIT_COMMIT}"))
        ldflags+=($(version::ldflag "gitTreeState" "${REPO_GIT_TREE_STATE}"))
    fi

    if [[ -n ${REPO_GIT_VERSION-} ]]; then
        ldflags+=($(version::ldflag "gitVersion" "${REPO_GIT_VERSION}"))
    fi

    if [[ -n ${REPO_GIT_REMOTE-} ]]; then
        ldflags+=($(version::ldflag "gitRemote" "${REPO_GIT_REMOTE}"))
    fi

    # The -ldflags parameter takes a single string, so join the output.
    echo "${ldflags[*]-}"
}
