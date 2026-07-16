#!/bin/bash
# This test is designed to be run directly by github actions or on your host (i.e. not earthbuild-in-earthbuild)
set -uxe
set -o pipefail

cd "$(dirname "$0")"

earthly=${earthly-"../../../build/linux/amd64/earthly"}
echo "using earthly=$(realpath "$earthly")"

rm .testdata || true # cleanup

"$earthly" "$@" +test
test -f .testdata && exit 1
test -f .otherdata

"$earthly" "$@" +test --fail=yes && exit 1
test -f .testdata && exit 1
test -f .otherdata
