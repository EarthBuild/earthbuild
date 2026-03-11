#!/usr/bin/env bash
set -uex

# Unset referenced-save-only.
export EARTHLY_VERSION_FLAG_OVERRIDES=""

# clean up old images (best effort)
docker images --format '{{.Repository}}:{{.Tag}}' | grep earthly-multiplatform-wait-test-with-from | xargs -r -n 1 docker rmi

cd "$(dirname "$0")"

earthly=${earthly-"../../../build/linux/amd64/earthly"}
"$earthly" +test
