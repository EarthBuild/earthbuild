#!/bin/sh
# Idle exit for buildkitd: stop the container once nothing has used BuildKit for
# IDLE_TIMEOUT seconds, so the VM it runs in stops and the host gets its memory
# back.
#
# Why a VM needs this: Apple's `container` gives the guest no balloon device,
# so memory a build touched stays with the VM until the VM stops, however much
# the guest frees. An unattended BuildKit VM holds its high-water mark for as
# long as it runs. The cache is on a volume, so stopping costs a restart and
# nothing that was built.
#
# Sourced by entrypoint.sh, and by idle-test.sh with a fake /proc/net.

# buildkit_active succeeds while a client is connected to BuildKit: an
# established connection whose *local* port is 8372 (a TCP client), or a
# `buildctl` process (the dial-stdio route Docker and Podman use). Either
# means a build may be running, and a false "busy" only delays an exit.
buildkit_active() {
    net="${IDLE_PROC_NET:-/proc/net}"

    # Column 2 is local_address as HEX_IP:HEX_PORT, column 4 the state; 01 is
    # ESTABLISHED, and 8372 is 0x20B4.
    if awk 'NR > 1 && $4 == "01" && $2 ~ /:20B4$/ { found = 1 } END { exit !found }' \
        "$net/tcp" "$net/tcp6" 2>/dev/null; then
        return 0
    fi

    "${IDLE_PGREP:-pgrep}" -x buildctl >/dev/null 2>&1
}

# idle_tick takes the time now, in seconds, and succeeds once BuildKit has
# been idle for IDLE_TIMEOUT seconds. A timeout of 0 or less never succeeds.
idle_tick() {
    now="$1"

    if [ "${IDLE_TIMEOUT:-0}" -le 0 ]; then
        return 1
    fi

    if buildkit_active; then
        idle_since=""

        return 1
    fi

    if [ -z "${idle_since:-}" ]; then
        idle_since="$now"

        return 1
    fi

    [ $((now - idle_since)) -ge "$IDLE_TIMEOUT" ]
}
