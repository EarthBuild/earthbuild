#!/bin/bash
set -e

# Verify that --push and --no-image-output compose: the image reaches the
# registry, the AS LOCAL artifact reaches the filesystem, and the image is not
# loaded into the local docker instance. See #855.
export CHECK_TAG_WAS_PUSHED=true

cd "$(dirname "$0")"

rm -rf output

# The shared harness already passes -P, which the LOCALLY checks need in order
# to inspect the host's docker instance and filesystem.
../common/test.sh --push --no-image-output

rm -rf output
