#!/usr/bin/env bash
set -uex
set -o pipefail

# Unset referenced-save-only.
export EARTH_VERSION_FLAG_OVERRIDES=""

cd "$(dirname "$0")"

earthly=${earthly-"../../../build/linux/amd64/earthly"}
"$earthly" --version

# display a pass/fail message at the end
function finish {
  status="$?"
  if [ "$status" = "0" ]; then
    echo "no-output test passed"
  else
    echo "no-output test failed with $status"
  fi
}
trap finish EXIT

# Cleanup from previous tests
docker rmi myimg:623cb5fb1b8c4cff8693281095724bb0 || true

# Do the tests

# first test we can do a regular build
"$earthly" +build
test "$(docker images -q myimg:623cb5fb1b8c4cff8693281095724bb0 | wc -l)" = "1"
docker rmi myimg:623cb5fb1b8c4cff8693281095724bb0

# copy shouldn't produce an image
"$earthly" +copy
test "$(docker images -q myimg:623cb5fb1b8c4cff8693281095724bb0 | wc -l)" = "0"

# --no-output should prevent outputting images
"$earthly" --no-output +build
test "$(docker images -q myimg:623cb5fb1b8c4cff8693281095724bb0 | wc -l)" = "0"

# --image mode only outputs image of directly-referenced image,
# in the case of +build, there is no SAVE IMAGE
"$earthly" --image +build
test "$(docker images -q myimg:623cb5fb1b8c4cff8693281095724bb0 | wc -l)" = "0"

# the +myimg target on the other hand contains an explicit SAVE IMAGE
"$earthly" --image +myimg
test "$(docker images -q myimg:623cb5fb1b8c4cff8693281095724bb0 | wc -l)" = "1"
docker rmi myimg:623cb5fb1b8c4cff8693281095724bb0

# --no-image-output suppresses only the image half of the output, leaving
# SAVE ARTIFACT AS LOCAL alone. See #855.

# baseline: without the flag, both the image and the artifact are produced.
# This is what makes the assertions below meaningful.
rm -rf output
"$earthly" +build-img-and-artifact
test "$(docker images -q myimg:623cb5fb1b8c4cff8693281095724bb0 | wc -l)" = "1"
test -f output/bar
docker rmi myimg:623cb5fb1b8c4cff8693281095724bb0

# --no-image-output: no image, but the artifact is still written.
# +img-and-artifact is reached via BUILD, so this covers the propagation path.
rm -rf output
"$earthly" --no-image-output +build-img-and-artifact
test "$(docker images -q myimg:623cb5fb1b8c4cff8693281095724bb0 | wc -l)" = "0"
test -f output/bar

# the env var form is equivalent
rm -rf output
EARTH_NO_IMAGE_OUTPUT=true "$earthly" +build-img-and-artifact
test "$(docker images -q myimg:623cb5fb1b8c4cff8693281095724bb0 | wc -l)" = "0"
test -f output/bar

# --no-output keeps its broader meaning: neither image nor artifact.
rm -rf output
"$earthly" --no-output +build-img-and-artifact
test "$(docker images -q myimg:623cb5fb1b8c4cff8693281095724bb0 | wc -l)" = "0"
test ! -f output/bar

# --no-image-output is contradictory in image mode, and rejected
if "$earthly" --image --no-image-output +img-and-artifact; then
    echo "expected --image --no-image-output to be rejected"
    exit 1
fi

rm -rf output
