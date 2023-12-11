#!/bin/bash

# ref https://github.com/fsaintjacques/semver-tool/blob/master/src/semver
NAT='0|[1-9][0-9]*'
ALPHANUM='[0-9]*[A-Za-z-][0-9A-Za-z-]*'
IDENT="$NAT|$ALPHANUM"
FIELD='[0-9A-Za-z-]+'

SEMVER_REGEX="\
^[vV]?\
($NAT)\\.($NAT)\\.($NAT)\
(\\-(${IDENT})(\\.(${IDENT}))*)?\
(\\+${FIELD}(\\.${FIELD})*)?$"

semver::validate_version() {
    local version=$1
    if [[ "$version" =~ $SEMVER_REGEX ]]; then
        # if a second argument is passed, store the result in var named by $2
        if [ "$#" -eq "2" ]; then
            local major=${BASH_REMATCH[1]}
            local minor=${BASH_REMATCH[2]}
            local patch=${BASH_REMATCH[3]}
            local prere=${BASH_REMATCH[4]}
            local build=${BASH_REMATCH[8]}
            eval "$2=(\"$major\" \"$minor\" \"$patch\" \"$prere\" \"$build\")"
        else
            echo "$version"
        fi
    else
        log::error "version $version does not match the semver scheme 'X.Y.Z(-PRERELEASE)(+BUILD)'. See help for more information."
    fi
}

# given two (named) arrays containing NAT and/or ALPHANUM fields, compare them
# one by one according to semver 2.0.0 spec. Return -1, 0, 1 if left array ($1)
# is less-than, equal, or greater-than the right array ($2).  The longer array
# is considered greater-than the shorter if the shorter is a prefix of the longer.
#
semver::compare_fields() {
    # local l="$1[@]"
    # local r="$2[@]"
    local leftfield=("$1[@]")
    local rightfield=("$2[@]")
    local left
    local right

    local i=$((-1))
    local order=$((0))

    while true; do
        [ $order -ne 0 ] && {
            echo $order
            return
        }

        : $((i++))
        left="${leftfield[$i]}"
        right="${rightfield[$i]}"

        is-null "$left" && is-null "$right" && {
            echo 0
            return
        }
        is-null "$left" && {
            echo -1
            return
        }
        is-null "$right" && {
            echo 1
            return
        }

        is-nat "$left" && is-nat "$right" && {
            order=$(order-nat "$left" "$right")
            continue
        }
        is-nat "$left" && {
            echo -1
            return
        }
        is-nat "$right" && {
            echo 1
            return
        }
        {
            order=$(order-string "$left" "$right")
            continue
        }
    done
}

# shellcheck disable=SC2206     # checked by "validate"; ok to expand prerel id's into array
semver::compare_version() {
    local order
    semver::validate_version "$1" V
    semver::validate_version "$2" V_

    # compare major, minor, patch

    local left=("${V[0]}" "${V[1]}" "${V[2]}")
    local right=("${V_[0]}" "${V_[1]}" "${V_[2]}")

    order=$(semver::compare_fields left right)
    [ "$order" -ne 0 ] && {
        echo "$order"
        return
    }

    # compare pre-release ids when M.m.p are equal

    local prerel="${V[3]:1}"
    local prerel_="${V_[3]:1}"
    local left=(${prerel//./ })
    local right=(${prerel_//./ })

    # if left and right have no pre-release part, then left equals right
    # if only one of left/right has pre-release part, that one is less than simple M.m.p

    [ -z "$prerel" ] && [ -z "$prerel_" ] && {
        echo 0
        return
    }
    [ -z "$prerel" ] && {
        echo 1
        return
    }
    [ -z "$prerel_" ] && {
        echo -1
        return
    }

    # otherwise, compare the pre-release id's

    semver::compare_fields left right
}

is-nat() {
    [[ "$1" =~ ^($NAT)$ ]]
}

is-null() {
    [ -z "$1" ]
}

order-nat() {
    [ "$1" -lt "$2" ] && {
        echo -1
        return
    }
    [ "$1" -gt "$2" ] && {
        echo 1
        return
    }
    echo 0
}

order-string() {
    [[ $1 < $2 ]] && {
        echo -1
        return
    }
    [[ $1 > $2 ]] && {
        echo 1
        return
    }
    echo 0
}
