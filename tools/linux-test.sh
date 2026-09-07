#!/usr/bin/env bash
# Run this engine's linux-only tests on a macOS development machine.
#
# engine/trace, engine/guest and engine/mat/overlay are linux-only, and CI builds
# linux, so on a mac they are the packages nobody runs until a pull request.
#
# Three things this needs, each learned from the failure it causes:
#
#   --privileged   the tracer installs a seccomp filter and the materialiser
#                  mounts overlayfs
#   -v ...:/tmp    a *volume*, not the container's own root and not a tmpfs.
#                  A container root is overlayfs and overlayfs cannot stack on
#                  overlayfs, so every mount fails with EINVAL - which the
#                  materialiser diagnoses in full, and which is the whole reason
#                  this script exists
#   rsync to /work the source is bind-mounted read-only, and a build writes
#
# Usage: tools/linux-test.sh [packages...]   (default: the linux-only three)
set -euo pipefail

cd "$(dirname "$0")/.."

packages=("$@")
if [ ${#packages[@]} -eq 0 ]; then
    packages=(./engine/trace/... ./engine/guest/... ./engine/mat/...)
fi

docker volume create eb-linux-tmp >/dev/null

exec docker run --rm --privileged \
    -v "$PWD":/src:ro \
    -v "$HOME/go/pkg/mod":/go/pkg/mod:ro \
    -v eb-linux-tmp:/tmp \
    -w /src golang:1.26-alpine sh -c "
        apk add --no-cache rsync >/dev/null 2>&1
        rsync -a --exclude .git /src/ /work/
        cd /work && go test ${packages[*]}
    "
