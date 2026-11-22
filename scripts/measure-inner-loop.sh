#!/usr/bin/env bash
#
# Measure what a developer actually waits for: a no-op rebuild, and a rebuild
# after changing one line.
#
# The numbers in experiments-adversarial.md E19-E22 come from here. It exists so
# they can be reproduced rather than believed - the cheapest way to be wrong
# about performance is to quote a figure from a fortnight ago.
#
# Two traps this avoids, both of which produced a false reading by hand:
#
#   - Both binaries are rebuilt. earth-guestd runs inside the VM and is a
#     separate build for its platform; rebuilding only the host side leaves the
#     old guest running, reporting the bug you just fixed.
#   - Every timed run edits to a value never used before. Writing the same line
#     twice makes the second run a cache hit, which returns in 20ms and looks
#     like a triumph.
#
# Usage: scripts/measure-inner-loop.sh [steps]

set -euo pipefail

cd "$(dirname "$0")/.."

steps=${1:-3}
work=$(mktemp -d "${TMPDIR:-/tmp}/earth-inner-loop.XXXXXX")
trap 'rm -rf "$work"' EXIT

printf 'building both binaries\n'
go build -o "$work/earth-native" ./cmd/earth-native
GOOS=linux GOARCH="$(go env GOARCH)" go build -o "$work/earth-guestd" ./cmd/earth-guestd

proj=$work/project
mkdir -p "$proj"

{
	printf 'VERSION 0.8\nmain:\n    FROM alpine:3.22\n    COPY src.txt /src.txt\n'
	for i in $(seq 1 "$steps"); do printf '    RUN cat /src.txt > /o%s.txt\n' "$i"; done
} >"$proj/Earthfile"

run() {
	( cd "$proj" && /usr/bin/time -p "$work/earth-native" +main ) 2>&1 |
		awk '/^real/{printf "%s", $2}'
}

# A first build to boot the VM and fill the cache. Its time is the cold number
# and is reported separately, because nothing else in this script pays it.
printf 'seed-%s\n' "$(date +%s%N)" >"$proj/src.txt"
printf 'cold (boots the VM, pulls the image) %8ss\n' "$(run)"

printf 'no-op rebuild                        '
for _ in 1 2 3; do printf ' %ss' "$(run)"; done
printf '\n'

printf 'one line changed                     '
for _ in 1 2 3; do
	printf 'change-%s\n' "$(date +%s%N)" >"$proj/src.txt"
	printf ' %ss' "$(run)"
done
printf '\n'

printf '\nsteps below the edit: %s\n' "$steps"
printf 'the VM is left running on purpose; earth-native -stop-sandbox removes it\n'
