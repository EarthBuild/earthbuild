#!/usr/bin/env bash
# Compare the native engine against earthly (BuildKit) on the same target.
#
# **Everything here is a mistake somebody already made.** Each guard below is a
# comparison that came out wrong and had to be thrown away:
#
#   - Two engines, two core counts. `container run` defaults to four vCPUs and
#     Docker's VM takes all sixteen, so a `go build` was given a quarter of the
#     machine on one side. That alone reversed the result.
#   - A baseline from a different Earthfile. Numbers were compared across two
#     workloads and read as a regression. The workload is a parameter here, and
#     it is recorded beside every row.
#   - A machine that was not quiet. Two orphaned shell loops span for ten hours
#     and taxed everything measured against them.
#   - One run each. Drift over a session is larger than most of the differences
#     being looked for, so the two engines alternate and both orders are used.
#   - Nowhere to look afterwards. Every row carries the commit it was taken at.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
ledger="$here/docs-internals/bench-ledger.tsv"

target="+earthly"
pairs=2
states="cold warm"
settle_max=300
native="${EARTH_NATIVE_BIN:-$here/build/earth-native}"

usage() {
    cat <<'USAGE'
usage: benchmark-earthly.sh [options]

  -t TARGET     Earthfile target to build (default +earthly)
  -n PAIRS      alternating pairs per state (default 2)
  -s STATES     "cold", "warm", or "cold warm" (default both)
  -b BINARY     the native engine to test (default build/earth-native)
  -q SECONDS    how long to wait for the machine to settle (default 300)
  -h            this

Every run appends a row to docs-internals/bench-ledger.tsv, tagged with the
commit, so two engines are never compared across a change to either.
USAGE
}

while getopts ':t:n:s:b:q:h' opt; do
    case "$opt" in
        t) target="$OPTARG" ;;
        n) pairs="$OPTARG" ;;
        s) states="$OPTARG" ;;
        b) native="$OPTARG" ;;
        q) settle_max="$OPTARG" ;;
        h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done

fail() { printf 'benchmark-earthly: %s\n' "$*" >&2; exit 1; }

# Each run's output, kept only until the next one. `timed` discarded it, which
# is why a build failing in two seconds looked exactly like a build succeeding
# in two seconds.
# `-t PREFIX` alone is BSD-only; GNU coreutils demands a template with X's, and
# this machine has both mktemps depending on PATH. The template form is the one
# that works on either.
runlog=$(mktemp -t earthbench.XXXXXX)
trap 'rm -f "$runlog"' EXIT

for tool in earthly container docker git python3; do
    command -v "$tool" >/dev/null 2>&1 || fail "$tool is not installed"
done

[ -x "$native" ] || fail "no native engine at $native (build it, or pass -b)"

cores=$(sysctl -n hw.ncpu 2>/dev/null || nproc)
commit=$(git -C "$here" rev-parse --short HEAD)
dirty=$(git -C "$here" status --porcelain | head -1)

# **Both engines get the whole machine, or the comparison is about core counts.**
# Docker's VM is configured in Docker Desktop and cannot be set from here, so it
# is checked rather than adjusted: a mismatch is reported and the run goes on
# saying so, because a known-unfair number beats a silently unfair one.
export EARTH_SANDBOX_CPUS="$cores"
docker_cpus=$(docker info --format '{{.NCPU}}' 2>/dev/null || echo 0)

printf '── benchmark %s ──────────\n' "$target"
printf '  commit      %s%s\n' "$commit" "${dirty:+ (working tree dirty)}"
printf '  host cores  %s\n' "$cores"
printf '  native      %s cores (EARTH_SANDBOX_CPUS)\n' "$cores"
printf '  docker      %s cores' "$docker_cpus"

if [ "$docker_cpus" != "$cores" ]; then
    printf '   ** MISMATCH: this comparison is not like for like **'
fi

printf '\n'

# Quiet enough. The load average lags, so this waits for it to fall rather than
# sampling once - and says what is keeping it up, since the usual answer is a
# browser and the second-usual is something this session forgot to kill.
settle() {
    local want deadline load
    want=$(python3 -c "print(max(2.0, $cores * 0.35))")
    deadline=$(( $(date +%s) + settle_max ))

    while :; do
        load=$(uptime | sed 's/.*load average: *//' | cut -d, -f1 | tr -d ' ')

        if python3 -c "import sys; sys.exit(0 if float('$load') <= $want else 1)"; then
            printf '  load        %s (quiet enough, want <= %.1f)\n' "$load" "$want"
            return 0
        fi

        if [ "$(date +%s)" -ge "$deadline" ]; then
            printf '  load        %s ** still busy after %ss; measuring anyway **\n' \
                "$load" "$settle_max"
            printf '  busiest     %s\n' \
                "$(ps -Ao pcpu,comm -r 2>/dev/null | sed -n '2p' | tr -s ' ')"
            return 0
        fi

        sleep 5
    done
}

settle

record() {
    local engine=$1 state=$2 secs=$3 rc=$4
    local load
    load=$(uptime | sed 's/.*load average: *//' | cut -d, -f1 | tr -d ' ')

    if [ ! -s "$ledger" ]; then
        printf 'commit\twhen\ttarget\tengine\tstate\tseconds\trc\tcores\tload\tdirty\n' >>"$ledger"
    fi

    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
        "$commit" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$target" "$engine" "$state" \
        "$secs" "$rc" "$cores" "$load" "${dirty:+dirty}" >>"$ledger"
}

# A cold run pays for the machine as well as the build: earthly's `prune --reset`
# restarts buildkitd, and removing the sandbox makes the native engine boot a VM.
# Neither engine is given a head start the other does not get.
cold_earthly() { earthly prune --reset >/dev/null 2>&1 || true; }
cold_native() {
    # **Not swallowed.** A reset that quietly fails leaves the next build
    # standing on the last one's layers and calling itself cold, which is how
    # 7.86s was once recorded for a build that takes forty (E691). If the
    # machine cannot be put back, the number that follows is worthless and the
    # run stops rather than printing it.
    EARTH_RESET_CACHE=1 "$here/scripts/reset-native-sandbox.sh" >/dev/null \
        || fail "could not reset the native engine; a cold measurement is not possible"
}

# **Sets `secs` and `return_code` in this shell, and is not called in a command
# substitution.** It was, and `$(...)` is a subshell: the exit code assigned
# inside it never reached the caller, so every row said rc=0 - including four
# runs that had failed in under eight seconds and were recorded as a fast build
# (E691). A benchmark that cannot tell a failure from a result is worse than no
# benchmark.
timed() {
    local start end
    start=$(python3 -c 'import time; print(time.time())')

    set +e
    "$@" >"$runlog" 2>&1
    return_code=$?
    set -e

    end=$(python3 -c 'import time; print(time.time())')
    secs=$(python3 -c "print(f'{$end - $start:.2f}')")

    # **A rate limit is not a measurement.** Docker Hub allows an anonymous
    # puller 100 manifest requests an hour, and a benchmark loop of cold builds
    # exhausts that in an afternoon. Every FROM then fails in a second or two,
    # which lands in the ledger looking like the fastest build ever recorded -
    # the same class of lie as the reset that did not reset (E691).
    #
    # Both wordings: the native engine reports the HTTP status, and buildkitd
    # reports the registry's own error text. Matching only one of them means the
    # guard covers whichever engine happens to fail first and not the other.
    if grep -qiE '429 Too Many Requests|toomanyrequests|pull rate limit' "$runlog"; then
        fail "Docker Hub is rate-limiting this machine (429), so this run measured
  nothing. Wait for the hour to roll over, or use a mirror - EARTH_REGISTRY_MIRRORS
  for the native engine, buildkitd's own \`mirrors\` for earthly. Set BOTH or
  neither: one engine on a mirror and the other on Docker Hub is a comparison
  between two networks, not between two engines."
    fi
}

run_earthly() { earthly --no-output "$target"; }
run_native() { "$native" "$target"; }

for state in $states; do
    printf '\n  %s\n' "$state"

    for pair in $(seq 1 "$pairs"); do
        # Both orders, so a machine that is drifting one way does not favour
        # whichever engine happens to go first.
        if [ $(( pair % 2 )) -eq 1 ]; then
            order="earthly native"
        else
            order="native earthly"
        fi

        for engine in $order; do
            if [ "$state" = cold ]; then
                case "$engine" in
                    earthly) cold_earthly ;;
                    native)  cold_native ;;
                esac
            fi

            return_code=0
            secs=0
            timed "run_$engine"
            record "$engine" "$state" "$secs" "$return_code"

            if [ "$return_code" -ne 0 ]; then
                printf '    pair %s  %-8s %7ss  ** FAILED rc=%s - this time means nothing **\n' \
                    "$pair" "$engine" "$secs" "$return_code"
            else
                printf '    pair %s  %-8s %7ss\n' "$pair" "$engine" "$secs"
            fi
        done
    done
done

printf '\n  medians, this commit\n'
python3 - "$ledger" "$commit" "$target" <<'PY'
import statistics, sys

path, commit, target = sys.argv[1], sys.argv[2], sys.argv[3]
rows = {}

with open(path) as f:
    head = f.readline()
    for line in f:
        c, _, t, engine, state, secs, rc, *_ = line.rstrip("\n").split("\t")
        if c == commit and t == target and rc == "0":
            rows.setdefault((state, engine), []).append(float(secs))

for state in ("cold", "warm"):
    got = {e: v for (s, e), v in rows.items() if s == state}
    if len(got) < 2:
        continue

    def show(v):
        return f"{statistics.median(v):6.2f}s [{min(v):.2f}-{max(v):.2f} n={len(v)}]"

    ev, nv = got.get("earthly"), got.get("native")
    if not ev or not nv:
        continue

    e, n = statistics.median(ev), statistics.median(nv)
    faster = "native" if n < e else "earthly"

    # The spread is printed because the median alone hides the run that is not
    # like the others - a first build after the sandbox is renamed re-does work
    # the next one finds already done, and reads as a regression that is not one.
    print(f"    {state:5s} earthly {show(ev)}   native {show(nv)}")
    print(f"          {faster} by {abs(e-n)/max(e,n)*100:.0f}% on the median")
PY

printf '\n  rows appended to %s\n' "${ledger#"$here"/}"
