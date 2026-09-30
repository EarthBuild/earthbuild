#!/bin/sh
# Unit tests for the idle exit in idle.sh.
set -eu

# shellcheck source-path=SCRIPTDIR
# shellcheck source=idle.sh
. "$(dirname "$0")/idle.sh"

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

fake=$(mktemp -d)
trap 'rm -rf "$fake"' EXIT
IDLE_PROC_NET="$fake"
IDLE_PGREP=false

header="  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode"

# 8372 is 0x20B4. State 01 is ESTABLISHED, 0A is LISTEN.
printf '%s\n   0: 00000000:20B4 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 1\n' "$header" > "$fake/tcp"
printf '%s\n' "$header" > "$fake/tcp6"
expect "a listening socket alone is idle" "idle" "$(buildkit_active && echo busy || echo idle)"

printf '   1: 0100007F:20B4 0100007F:C350 01 00000000:00000000 00:00000000 00000000 0 0 2\n' >> "$fake/tcp"
expect "an established client on 8372 is busy" "busy" "$(buildkit_active && echo busy || echo idle)"

printf '%s\n   1: 0100007F:C350 0100007F:20B4 01 00000000:00000000 00:00000000 00000000 0 0 2\n' "$header" > "$fake/tcp"
expect "8372 as the remote port is not a client of ours" "idle" "$(buildkit_active && echo busy || echo idle)"

printf '%s\n' "$header" > "$fake/tcp"
printf '%s\n   0: 00000000000000000000000001000000:20B4 00000000000000000000000001000000:C350 01 00000000:00000000 00:00000000 00000000 0 0 3\n' "$header" > "$fake/tcp6"
expect "an established IPv6 client is busy" "busy" "$(buildkit_active && echo busy || echo idle)"

printf '%s\n' "$header" > "$fake/tcp6"
IDLE_PGREP=true
expect "a buildctl dial-stdio client is busy" "busy" "$(buildkit_active && echo busy || echo idle)"
IDLE_PGREP=false

# The clock: idle for the whole timeout, and only then.
IDLE_TIMEOUT=60
idle_since=""
expect "the first idle tick starts the clock" "wait" "$(idle_tick 1000 && echo exit || echo wait)"
idle_tick 1000 || true
expect "59 seconds idle is not enough" "wait" "$(idle_tick 1059 && echo exit || echo wait)"
expect "60 seconds idle is" "exit" "$(idle_tick 1060 && echo exit || echo wait)"

IDLE_PGREP=true
idle_tick 1061 || true
IDLE_PGREP=false
expect "activity restarts the clock" "wait" "$(idle_tick 1100 && echo exit || echo wait)"

IDLE_TIMEOUT=0
idle_since=""
idle_tick 1 || true
expect "a timeout of 0 never exits" "wait" "$(idle_tick 999999 && echo exit || echo wait)"

if [ "$failures" -ne 0 ]; then
    echo "$failures failure(s)"
    exit 1
fi
