#!/bin/bash

# clean up old images (best effort)
docker images --format '{{.Repository}}:{{.Tag}}' | grep myuser/earthly-multiplatform-wait-test | xargs -n 1 docker rmi

set -e
cd "$(dirname "$0")"
../common/test.sh
