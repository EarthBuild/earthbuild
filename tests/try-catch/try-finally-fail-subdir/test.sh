#!/bin/bash
# This test is designed to be run directly by github actions or on your host (i.e. not earthbuild-in-earthbuild)
# Regression test for #578: SAVE ARTIFACT ... AS LOCAL in FINALLY must export
# relative to the Earthfile's directory when the TRY body fails, even when
# earth is run from a different directory.
set -uxe
set -o pipefail

cd "$(dirname "$0")"

earthly=${earthly-"../../../build/linux/amd64/earthly"}
echo "using earthly=$(realpath "$earthly")"

cleanup() {
  rm -r data out/ sub/data sub/out/ || true
}

execute() {
  local target="$1"
  local expected_path="$2"
  local unexpected_path="$3"
  shift 3

  cleanup

  set +e
  "$earthly" $@ "$target"
  exit_code="$?"
  set -e

  test -f "$expected_path"
  test ! -e "$unexpected_path"
  test "$(cat "$expected_path")" = "magic"
  test "$exit_code" -ne "0"
}

execute './sub+test' sub/data data "$@"
execute './sub+test-save-to-child-dir' sub/out/data out/data "$@"
cleanup
