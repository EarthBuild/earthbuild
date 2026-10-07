#!/bin/sh
# Tests for dockerd-wrapper.sh, run against stub dockerd/docker/iptables
# binaries. The wrapper writes to /etc/docker and /var/run, so run this as root
# in a throwaway container (see +unit-test-scripts), not on a workstation.
set -eu

wrapper="$(cd "$(dirname "$0")" && pwd)/dockerd-wrapper.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
stubs="$work/bin"
mkdir -p "$stubs"

# The stub dockerd records the OTEL_TRACES_EXPORTER it was started with, then
# behaves like a daemon until the wrapper stops it.
cat >"$stubs/dockerd" <<'EOF'
#!/bin/sh
printf '%s' "${OTEL_TRACES_EXPORTER-<unset>}" >"$STUB_RESULTS/dockerd-otel"
echo "$$" >/var/run/docker.pid
trap 'exit 0' TERM
while :; do sleep 0.1; done
EOF

# The stub docker reports the daemon as up once the stub dockerd has started.
cat >"$stubs/docker" <<'EOF'
#!/bin/sh
case "$1" in
    ps) test -s /var/run/docker.pid ;;
    *) echo >&2 "docker stub: unexpected command: $*"; exit 1 ;;
esac
EOF

cat >"$stubs/iptables" <<'EOF'
#!/bin/sh
exit 0
EOF

chmod +x "$stubs/dockerd" "$stubs/docker" "$stubs/iptables"

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

# run_wrapper runs a WITH DOCKER step whose user command records the
# OTEL_TRACES_EXPORTER it sees. Extra NAME=value arguments are set in the
# wrapper's environment, as BuildKit or the user's ENV would set them.
run_wrapper() {
    results="$work/results"
    rm -rf "$results"
    mkdir -p "$results"

    # --flock-acquired skips the cgroup v2 setup, which needs a writable
    # /sys/fs/cgroup that a test container does not have. The user command is
    # single-quoted on purpose: it must expand its own environment, not ours.
    # shellcheck disable=SC2016
    env -u OTEL_TRACES_EXPORTER -u EARTH_DOCKERD_OTEL_TRACES_EXPORTER \
        PATH="$stubs:$PATH" STUB_RESULTS="$results" "$@" \
        sh "$wrapper" execute --data-root="$work/data-root" --flock-acquired -- \
        sh -c 'printf "%s" "${OTEL_TRACES_EXPORTER-<unset>}" >"$STUB_RESULTS/cmd-otel"' \
        >"$work/wrapper.log" 2>&1 || {
        echo "wrapper failed:"
        cat "$work/wrapper.log"
        exit 1
    }
}

result() {
    cat "$work/results/$1" 2>/dev/null || echo "<not run>"
}

# BuildKit sets OTEL_TRACES_EXPORTER=otlp in every RUN when it has tracing
# enabled; that must not reach dockerd (earthly/earthly#4066), but must reach
# the user's command untouched (#901).
run_wrapper OTEL_TRACES_EXPORTER=otlp
expect "dockerd tracing is off when BuildKit enables OTel" "none" "$(result dockerd-otel)"
expect "user command keeps the inherited exporter" "otlp" "$(result cmd-otel)"

run_wrapper
expect "dockerd tracing is off when nothing configures OTel" "none" "$(result dockerd-otel)"
expect "user command does not gain an exporter" "<unset>" "$(result cmd-otel)"

run_wrapper OTEL_TRACES_EXPORTER=otlp EARTH_DOCKERD_OTEL_TRACES_EXPORTER=otlp
expect "EARTH_DOCKERD_OTEL_TRACES_EXPORTER opts dockerd in" "otlp" "$(result dockerd-otel)"
expect "opting dockerd in leaves the user command alone" "otlp" "$(result cmd-otel)"

run_wrapper OTEL_TRACES_EXPORTER=console EARTH_DOCKERD_OTEL_TRACES_EXPORTER=""
expect "an empty opt-in falls back to none" "none" "$(result dockerd-otel)"
expect "user command keeps its own exporter" "console" "$(result cmd-otel)"

if [ "$failures" -ne 0 ]; then
    echo "$failures test(s) failed"
    exit 1
fi

echo "=== All dockerd-wrapper tests have passed ==="
