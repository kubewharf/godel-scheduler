#!/bin/bash

# Controls verbosity of the script output and logging.
VERBOSE="${VERBOSE:-5}"

# If set true, the log will be colorized
readonly COLOR_LOG=${COLOR_LOG:-true}

if [[ ${COLOR_LOG} == "true" ]]; then
    readonly blue="\033[34m"
    readonly green="\033[32m"
    readonly red="\033[31m"
    readonly yellow="\033[36m"
    readonly strong="\033[1m"
    readonly reset="\033[0m"
else
    readonly blue=""
    readonly green=""
    readonly red=""
    readonly yellow=""
    readonly strong=""
    readonly reset=""
fi

# Handler for when we exit automatically on an error.
# Borrowed from https://gist.github.com/ahendrix/7030300
log::errexit() {
    local err="${PIPESTATUS[@]}"

    # If the shell we are in doesn't have errexit set (common in subshells) then
    # don't dump stacks.
    set +o | grep -qe "-o errexit" || return

    set +o xtrace
    local code="${1:-1}"
    # Print out the stack trace described by $function_stack
    if [ ${#FUNCNAME[@]} -gt 2 ]; then
        log::error "Call tree:"
        for ((i = 1; i < ${#FUNCNAME[@]} - 1; i++)); do
            log::error " $i: ${BASH_SOURCE[$i + 1]}:${BASH_LINENO[$i]} ${FUNCNAME[$i]}(...)"
        done
    fi
    log::error_exit "Error in ${BASH_SOURCE[1]}:${BASH_LINENO[0]}. '${BASH_COMMAND}' exited with status $err" "${1:-1}" 1
}

log::install_errexit() {
    # trap ERR to provide an error handler whenever a command exits nonzero  this
    # is a more verbose version of set -o errexit
    trap 'log::errexit' ERR

    # setting errtrace allows our ERR trap handler to be propagated to functions,
    # expansions and subshells
    set -o errtrace
}

# Print out the stack trace
#
# Args:
#   $1 The number of stack frames to skip when printing.
log::stack() {
    local stack_skip=${1:-0}
    stack_skip=$((stack_skip + 1))
    if [[ ${#FUNCNAME[@]} -gt $stack_skip ]]; then
        echo -e "${strong}Call stack:${reset}" >&2
        local i
        for ((i = 1; i <= ${#FUNCNAME[@]} - $stack_skip; i++)); do
            local frame_no=$((i - 1 + stack_skip))
            local source_file=${BASH_SOURCE[$frame_no]}
            local source_lineno=${BASH_LINENO[$((frame_no - 1))]}
            local funcname=${FUNCNAME[$frame_no]}
            echo -e "${strong}  $i: ${source_file}:${source_lineno} ${funcname}(...)${reset}" >&2
        done
    fi
}

# Log an error and exit.
# Args:
#   $1 Message to log with the error
#   $2 The error code to return
#   $3 The number of stack frames to skip when printing.
log::error_exit() {
    local message="${1:-}"
    local code="${2:-1}"
    local stack_skip="${3:-0}"
    stack_skip=$((stack_skip + 1))

    if [[ ${VERBOSE} -ge 4 ]]; then
        local source_file=${BASH_SOURCE[$stack_skip]}
        local source_line=${BASH_LINENO[$((stack_skip - 1))]}
        echo -e "${red}!!!${reset} ${strong}Error in ${source_file}:${source_line}${reset}" >&2
        [[ -z ${1-} ]] || {
            echo -e "${strong}  ${1}${reset}" >&2
        }

        log::stack $stack_skip

        echo "Exiting with status ${code}" >&2
    fi

    exit "${code}"
}

# Log an error but keep going.  Don't dump the stack or exit.
log::error() {
    timestamp=$(date +"[%m%d %H:%M:%S]")
    echo -e "${red}!!! $timestamp${reset} ${strong}${1-}${reset}" >&2
    shift
    for message; do
        echo "    $message" >&2
    done
}

# Print an usage message to stderr.  The arguments are printed directly.
log::usage() {
    echo >&2
    local message
    for message; do
        echo "$message" >&2
    done
    echo >&2
}

log::usage_from_stdin() {
    local messages=()
    while read -r line; do
        messages+=("$line")
    done

    log::usage "${messages[@]}"
}

# Print out some info that isn't a top level status line
log::info() {
    local V="${V:-0}"
    if [[ $VERBOSE < $V ]]; then
        return
    fi

    for message; do
        echo -e "${strong}$message${reset}"
    done
}

# Just like log::info, but no \n, so you can make a progress bar
log::progress() {
    for message; do
        echo -e -n "$message"
    done
}

log::info_from_stdin() {
    local messages=()
    while read -r line; do
        messages+=("$line")
    done

    log::info "${messages[@]}"
}

# Print a status line.  Formatted to show up in a stream of output.
log::status() {
    local V="${V:-0}"
    if [[ $VERBOSE < $V ]]; then
        return
    fi

    timestamp=$(date +"[%m%d %H:%M:%S]")
    echo -e "${blue}==> $timestamp${reset} ${strong}$1${reset}"
    shift
    for message; do
        echo "    $message"
    done
}

log::confirm() {
    local message=${1:-Are you sure?}
    echo -e "${green}${message}${reset}"

    while true; do
        read -p "[y/n]: " -n 1 -r
        if [[ ${REPLY} =~ ^[Yy]$ ]]; then
            echo
            return 0
        fi
        if [[ ${REPLY} =~ ^[Nn]$ ]]; then
            echo
            return 1
        fi
        echo -e "\n${red}invalid input ${REPLY}${reset}"
    done
}
