#!/usr/bin/env bash
set -uex
set -o pipefail

# Unset referenced-save-only.
export EARTHLY_VERSION_FLAG_OVERRIDES=""

cd "$(dirname "$0")"

earthbuild=${earthbuild-"../../../build/linux/amd64/earthbuild"}
"$earthbuild" --version

# display a pass/fail message at the end
function finish {
  status="$?"
  if [ "$status" = "0" ]; then
    echo "save-artifact test passed"
  else
    echo "save-artifact test failed with $status"
  fi
}
trap finish EXIT

# Cleanup from previous tests
rm -f data

"$earthbuild" $@ +test
test "$(cat data)" = "foo"

# next, check for an expected failure
set +e
("$earthbuild" $@ +test-fail; echo $? > earthbuild.exitcode) 2>&1 | tee earthbuild.log
set -e
test "$(cat earthbuild.exitcode)" = "1"
grep 'unable to copy file data, which has is outputted elsewhere' earthbuild.log

if grep "this magic string should never appear" earthbuild.log >/dev/null; then
  echo "magic string command should never have run, but did"
  exit 1
fi
