#!/bin/bash

# Exit on error. Append "|| true" if you expect an error.
set -o errexit
# Do not allow use of undefined vars. Use ${VAR:-} to use an undefined VAR
set -o nounset
# Catch the error in pipeline.
set -o pipefail

MAKE_RULES_ROOT=$(dirname "${BASH_SOURCE}")
VERBOSE="${VERBOSE:-1}"
source "${MAKE_RULES_ROOT}/lib/init.sh"

inline() {
	while IFS="\n" read -r line; do
		if [[ "${line}" =~ (\.|source)\s+.+ ]]; then
			# get file name after source, delete the "" around it
			file="$(echo "${line}" | cut -d' ' -f2 | tr -d '"')"
			# replace MAKE_RULES_ROOT with its value
			file=$(echo "${file}" | sed 's:\${MAKE_RULES_ROOT}:'${MAKE_RULES_ROOT}':g')
			echo "$(cat ${file})"
		else
			echo "${line}"
		fi
	done <"$1"
}

template=$1
output=$2

cp ${template} ${output}
while egrep -q '^(source|\.)' ${output}; do
	echo "$(inline ${output})" >${output}
done

# delete MAKE_RULES_ROOT line
sed -i '/MAKE_RULES_ROOT/d' ${output}

# header until set -o pipefail
head=$(head -12 ${output})
# tail after set -o pipefail
tail=$(tail -n +13 ${output})
# delete unused options and comments
tail=$(echo "${tail}" | sed '/^set -o errexit/d')
tail=$(echo "${tail}" | sed '/^set -o nounset/d')
tail=$(echo "${tail}" | sed '/^set -o pipefail/d')
tail=$(echo "${tail}" | sed '/^\#\!\/bin\/bash/d')

echo "${head}" >${output}
echo "${tail}" >>${output}
