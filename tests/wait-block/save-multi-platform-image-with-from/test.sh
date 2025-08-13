#!/usr/bin/env bash
set -uex

# Unset referenced-save-only.
export EARTHLY_VERSION_FLAG_OVERRIDES=""

# clean up old images (best effort)
docker images | grep earthbuild-multiplatform-wait-test-with-from | awk '{print $1 ":" $2}' | xargs -r -n 1 docker rmi

cd "$(dirname "$0")"

earthbuild=${earthbuild-"../../../build/linux/amd64/earthbuild"}
"$earthbuild" +test
