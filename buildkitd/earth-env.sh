#!/bin/sh
# earth-env.sh provides earth_env, the shell counterpart of internal/env.Lookup.
#
# It is sourced by scripts baked into the earthbuild images. It is deliberately
# NOT sourced by dockerd-wrapper.sh, which is bind-mounted into RUN commands as
# a single file and therefore has to carry its own copy of this helper.
#
# NOTE: the EARTHLY_ fallback is a temporary shim to support the
# EARTHLY_ -> EARTH_ migration; drop it once EARTHLY_ support is officially
# removed.

# earth_env SUFFIX [DEFAULT]
#
# Echoes the value of EARTH_<SUFFIX>. If that is unset or empty, falls back to
# the deprecated EARTHLY_<SUFFIX>, warning on stderr. If neither is set, echoes
# DEFAULT (empty when omitted).
earth_env() {
    eval "_earth_env_v=\${EARTH_$1-}"
    if [ -n "$_earth_env_v" ]; then
        printf '%s' "$_earth_env_v"
        return 0
    fi

    eval "_earth_env_v=\${EARTHLY_$1-}"
    if [ -n "$_earth_env_v" ]; then
        echo >&2 "WARNING: EARTHLY_$1 is deprecated. Use EARTH_$1."
        printf '%s' "$_earth_env_v"
        return 0
    fi

    printf '%s' "${2-}"
}
