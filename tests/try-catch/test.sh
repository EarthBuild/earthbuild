#!/bin/bash
# This test is designed to be run directly by github actions or on your host (i.e. not earthbuild-in-earthbuild)
set -ue
set -o pipefail

cd "$(dirname "$0")"

# display a pass/fail message at the end
function finish {
  status="$?"
  RED='\033[0;31m'
  GREEN='\033[0;32m'
  NC='\033[0m' # No Color
  if [ "$status" = "0" ]; then
    printf '%stry-catch tests passed%s\n' "$GREEN" "$NC"
  else
    printf '%stry-catch tests failed with %s%s\n' "$RED" "$status" "$NC"
  fi
}
trap finish EXIT

# TODO: add back docker-try-finally-fail
for test_path in try-catch-not-currently-implemented try-finally-fail try-finally-pass try-finally-if-exists try-finally-two-files
do
    printf '=== running %s ===\n\n' "$test_path"
    "${test_path}/test.sh"
    printf '%s passed\n\n' "$test_path"
done
