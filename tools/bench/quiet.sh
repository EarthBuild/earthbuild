#!/usr/bin/env bash
# Refuse to benchmark on a machine that is busy.
#
# Every wrong measurement taken against this engine has been a measurement of
# the machine: a stale guest binary, a dozen leaked sandbox VMs holding 241,401
# file descriptors, a load average of 49 caused by the other engine's own daemon.
# Each looked like a result about the engine and each was reproducible, which is
# what made them convincing.
#
# Run this before a benchmark. It reports what is loud and exits non-zero, so a
# script can gate on it rather than a person remembering to look.
#
#   tools/bench/quiet.sh || echo "not now"
#
# Thresholds are deliberately generous: the point is to catch a machine that is
# obviously unfit, not to insist on silence.

set -uo pipefail

max_load=${BENCH_MAX_LOAD:-4}
max_fd_pct=${BENCH_MAX_FD_PCT:-25}

noisy=0

# macOS prints "load averages: 1.23 4.56 7.89"; Linux "load average: 1.23, 4.56".
# The commas matter: `awk '{print $1}'` leaves one attached, and awk then compares
# "29.77," against "4" as *strings* - "2" sorts before "4" - so a machine at load
# 30 reported itself quiet. The `+0` is what makes the comparison arithmetic.
load=$(uptime | sed -E 's/.*average[s]?:[[:space:]]*//' | tr ',' ' ' | awk '{print $1+0}')
if awk -v l="$load" -v m="$max_load" 'BEGIN{exit !(l+0 > m+0)}'; then
    echo "LOUD: load average ${load}, over ${max_load}"
    ps -A -o %cpu,pid,comm | sort -rn | head -4 | sed 's/^/      /'
    noisy=1
fi

if num=$(sysctl -n kern.num_files 2>/dev/null) && max=$(sysctl -n kern.maxfiles 2>/dev/null); then
    pct=$((num * 100 / max))
    if [ "$pct" -gt "$max_fd_pct" ]; then
        echo "LOUD: ${pct}% of the system's file descriptors are in use (${num}/${max})"
        echo "      a leaked sandbox holds one per file in its store; see nits"
        noisy=1
    fi
fi

# Sandboxes nobody is building with. Each holds descriptors and memory, and each
# is a build that was interrupted rather than finished.
vms=0
for p in $(pgrep -f "Virtualization.VirtualMachine" 2>/dev/null); do
    if [ "$(lsof -p "$p" 2>/dev/null | grep -c earthbuild)" -gt 0 ]; then
        vms=$((vms + 1))
    fi
done

if [ "$vms" -gt 1 ]; then
    echo "LOUD: ${vms} sandbox VMs are running; a benchmark will contend with them"
    noisy=1
fi

if [ "$noisy" -eq 0 ]; then
    echo "quiet: load ${load}, descriptors $((num * 100 / max))%, ${vms} sandbox(es)"
fi

exit "$noisy"
