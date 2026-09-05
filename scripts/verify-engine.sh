#!/usr/bin/env bash
#
# Verify the native engine: formatting, vet on both platforms, tests, race.
#
# This exists because the checks were being run as a string of shell commands
# with `&& echo OK` on the end, and four times in one day that OK printed when
# the check had not passed - once because the package had not compiled, once
# because a test binary had timed out, twice because the echo was not attached
# to the thing it claimed to be reporting. A status line that is not conditional
# on the result is not a check.
#
# So: `set -euo pipefail`, one function that reports its own outcome, and a
# final line that is only reachable if nothing exited non-zero.
#
# Usage:
#   scripts/verify-engine.sh          # everything but the sandbox suite
#   scripts/verify-engine.sh --net    # also the tests that need a VM and a network
#   scripts/verify-engine.sh --oracle # and the differential against the shipping engine

set -euo pipefail

cd "$(dirname "$0")/.."

failed=0

# Logs go one file per step. A single shared log was overwritten by whichever
# step ran next, so by the time anyone looked the evidence belonged to a
# different check.
logdir=$(mktemp -d "${TMPDIR:-/tmp}/verify-engine.XXXXXX")

step() {
	local name=$1
	shift

	local log="$logdir/${name//[^a-zA-Z0-9]/-}.log"

	if "$@" >"$log" 2>&1; then
		printf '  ok    %s\n' "$name"
		return 0
	fi

	printf '  FAIL  %s\n' "$name"

	# The lines that name what went wrong, before the ones that describe it. A
	# timeout dumps every goroutine's stack, so the first thirty lines of the
	# raw log were thirty frames of scheduler fan-out and the name of the test
	# that hung was somewhere below - which cost a diagnosis once already.
	grep -nE '^(--- )?FAIL|^panic|^\s+--- FAIL|running tests:|^\t.*\.go:[0-9]+:|test timed out' "$log" |
		head -12 | sed 's/^/        /'

	printf '        ---- first lines ----\n'
	head -12 "$log" | sed 's/^/        /'
	printf '        full log: %s\n' "$log"

	failed=1
	return 0
}

# gofmt reports by printing names, and says nothing on success - so an empty
# output is the pass condition rather than the exit status.
check_gofmt() {
	local out
	out=$(gofmt -l engine/ cmd/ 2>&1)

	if [ -n "$out" ]; then
		printf '%s\n' "$out"
		return 1
	fi
}

printf 'verifying the native engine\n'

step "gofmt" check_gofmt
step "vet (this machine)" go vet ./engine/... ./cmd/...
step "vet (linux/amd64)" env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go vet ./engine/... ./cmd/...
step "tests" go test -count=1 ./engine/...
step "race (short)" go test -count=1 -short -race -timeout 300s ./engine/...

if [ "${1:-}" = "--net" ] || [ "${1:-}" = "--oracle" ]; then
	step "sandbox suite" env EARTH_TEST_NETWORK=1 go test -count=1 -timeout 900s ./engine/cli/
fi

# The differential against the engine that ships, which is the only check that
# this engine agrees with the one people use. Behind its own flag rather than
# --net because it drives a daemon in a container, and a wedged daemon does not
# fail - it stops making progress, and takes the rest of the run with it.
if [ "${1:-}" = "--oracle" ]; then
	step "differential oracle" env EARTH_TEST_NETWORK=1 EARTH_TEST_ORACLE=1 \
		go test -count=1 -timeout 1800s -run TestBothEnginesProduceTheSameArtifact ./engine/cli/
fi

if [ "$failed" -ne 0 ]; then
	printf 'FAILED\n'
	exit 1
fi

printf 'all checks passed\n'
