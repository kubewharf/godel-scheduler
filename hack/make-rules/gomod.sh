#!/bin/bash

module=""
goversion=""
require=()
replace=()
multiline=""

src=${1}
out=${1}.new

while read line; do
    if [[ ${line} =~ ^module ]]; then
        module=${line}
        continue
    fi
    version="^go "
    if [[ ${line} =~ ${version} ]]; then
        goversion=${line}
        continue
    fi
    if [[ ${line} == "replace (" ]]; then
        multiline="replace"
        continue
    elif [[ ${line} =~ replace ]]; then
        replace+=("${line#'replace '}")
        continue
    fi
    if [[ ${line} == "require (" ]]; then
        multiline="require"
        continue
    elif [[ $line =~ require ]]; then
        require+=("${line#'require'}")
        continue
    fi

    if [[ ${line} == ")" ]]; then
        multiline=""
        continue
    fi

    if [[ "${multiline}" == "replace" ]]; then
        replace+=("${line}")
    elif [[ "${multiline}" == "require" ]]; then
        require+=("${line}")
    fi
done <${src}

echo ${module} >${out}
echo >>${out}
echo ${goversion} >>${out}
echo >>${out}

require_num=${#require[@]}
replace_num=${#replace[@]}

if [[ ${require_num} -gt 0 ]]; then
    echo "require (" >>${out}
    # echo >${out}
    for r in "${require[@]}"; do
        echo "    ${r}" >>${out}
    done
    echo ")" >>${out}
    echo >>${out}
fi

if [[ ${replace_num} -gt 0 ]]; then
    echo "replace (" >>${out}
    # echo >${out}
    for r in "${replace[@]}"; do
        echo "    ${r}" >>${out}
    done
    echo ")" >>${out}
    echo >>${out}
fi

mv ${out} $1
