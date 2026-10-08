#!/bin/sh
# Unit tests for the earth_env shim in earth-env.sh.
set -eu

# shellcheck source-path=SCRIPTDIR
# shellcheck source=earth-env.sh
. "$(dirname "$0")/earth-env.sh"

failures=0

expect() {
    desc="$1"
    want="$2"
    got="$3"

    if [ "$want" = "$got" ]; then
        echo "ok   - $desc"
    else
        echo "FAIL - $desc: want [$want], got [$got]"
        failures=$((failures+1))
    fi
}

# The new name wins, and is silent.
EARTH_TEST_VAR=new EARTHLY_TEST_VAR=old
export EARTH_TEST_VAR EARTHLY_TEST_VAR
expect "new prefix wins" "new" "$(earth_env TEST_VAR 2>/dev/null)"
expect "new prefix does not warn" "" "$(earth_env TEST_VAR 2>&1 >/dev/null)"

# The deprecated name still works, but warns.
unset EARTH_TEST_VAR
expect "deprecated prefix is honoured" "old" "$(earth_env TEST_VAR 2>/dev/null)"
expect "deprecated prefix warns" \
    "WARNING: EARTHLY_TEST_VAR is deprecated. Use EARTH_TEST_VAR." \
    "$(earth_env TEST_VAR 2>&1 >/dev/null)"

# Neither set: the default applies, silently.
unset EARTHLY_TEST_VAR
expect "default is used" "fallback" "$(earth_env TEST_VAR fallback 2>/dev/null)"
expect "default does not warn" "" "$(earth_env TEST_VAR fallback 2>&1 >/dev/null)"
expect "empty when no default" "" "$(earth_env TEST_VAR 2>/dev/null)"

# An empty value is treated as unset, so the fallback chain continues.
EARTH_TEST_VAR="" EARTHLY_TEST_VAR=old
export EARTH_TEST_VAR EARTHLY_TEST_VAR
expect "empty new prefix falls through" "old" "$(earth_env TEST_VAR 2>/dev/null)"

# Values with spaces survive intact, rather than being word-split.
unset EARTHLY_TEST_VAR
EARTH_TEST_VAR="$(printf "a b  'c'")"
export EARTH_TEST_VAR
expect "values are not word-split" "$EARTH_TEST_VAR" "$(earth_env TEST_VAR)"

if [ "$failures" -ne 0 ]; then
    echo "$failures test(s) failed"
    exit 1
fi

echo "=== All earth_env tests have passed ==="
