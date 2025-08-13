#!/bin/bash
set -euo pipefail # don't use -x as it will leak the mirror credentials

# to run this locally; in the root of the repo:
#   ./earthbuild +earthbuild-docker && EARTHBUILD_IMAGE="earthbuild/earthbuild:dev-$(git rev-parse --abbrev-ref HEAD | sed 's/\//_/g')" scripts/tests/earthbuild-image.sh

FRONTEND=${FRONTEND:-docker}
EARTHBUILD_IMAGE=${EARTHBUILD_IMAGE:-earthbuild/earthbuild:dev-main}
PATH="$(realpath "$(dirname "$0")/../acbtest"):$PATH"

dockerconfig="$(mktemp /tmp/earthbuild-image-test-docker-config.XXXXXX)"
chmod 600 "$dockerconfig"
cat > "$dockerconfig" <<EOF
{}
EOF

# Note that it is not possible to use GLOBAL_CONFIG for this, due to the fact
# earthbuild-entrypoint.sh starts buildkit instead of the earthbuild binary,
# as a result the buildkit_additional_config value in ~/.earthbuild/config.yml is ignored.
export EARTHBUILD_ADDITIONAL_BUILDKIT_CONFIG='[registry."docker.io"]
  mirrors = ["mirror.gcr.io", "public.ecr.aws"]'

function finish {
  status="$?"
  if [ "$status" = "0" ]; then
    echo "earthbuild-image.sh test passed"
  else
    echo "earthbuild-image.sh failed with $status"
  fi
  rm "$dockerconfig"
}
trap finish EXIT

echo "Test no --privileged and no NO_BUILDKIT=1 -> fail."
if "$FRONTEND" run --rm "${EARTHBUILD_IMAGE}" 2>&1 | tee output.txt; then
    echo "expected failure"
    exit 1
fi
acbgrep "Container appears to be running unprivileged" output.txt

echo "Test no target provided -> fail."
if "$FRONTEND" run --rm --privileged "${EARTHBUILD_IMAGE}" 2>&1 | tee output.txt; then
    echo "expected failure"
    exit 1
fi
acbgrep "Executes earthbuild builds" output.txt # Display help
acbgrep "no target reference provided" output.txt # Show error
if "$FRONTEND" run --rm -e NO_BUILDKIT=1 "${EARTHBUILD_IMAGE}" 2>&1 | tee output.txt; then
    echo "expected failure"
    exit 1
fi
acbgrep "Executes earthbuild builds" output.txt # Display help
acbgrep "no target reference provided" output.txt # Show error

echo "Test --version (smoke test)."
"$FRONTEND" run --rm --privileged "${EARTHBUILD_IMAGE}" --version 2>&1
"$FRONTEND" run --rm -e NO_BUILDKIT=1 "${EARTHBUILD_IMAGE}" --version 2>&1

echo "Test --help."
"$FRONTEND" run --rm --privileged "${EARTHBUILD_IMAGE}" --help 2>&1 | tee output.txt
acbgrep "Executes earthbuild builds" output.txt # Display help
"$FRONTEND" run --rm -e NO_BUILDKIT=1 "${EARTHBUILD_IMAGE}" --help 2>&1 | tee output.txt
acbgrep "Executes earthbuild builds" output.txt # Display help

echo "Test hello world with embedded buildkit."
"$FRONTEND" run --rm --privileged -e EARTHBUILD_ADDITIONAL_BUILDKIT_CONFIG -v "$dockerconfig:/root/.docker/config.json" "${EARTHBUILD_IMAGE}" --no-cache github.com/EarthBuild/hello-world:4d466d524f768a379374c785fdef30470e87721d+hello 2>&1 | tee output.txt
acbgrep "Hello World" output.txt
acbgrep "earthbuild installation is working correctly" output.txt

if [ "$FRONTEND" = "docker" ]; then
    echo "Test use /var/run/docker.sock, but not privileged."
    "$FRONTEND" run --rm -e EARTHBUILD_ADDITIONAL_BUILDKIT_CONFIG -v "$dockerconfig:/root/.docker/config.json" -e NO_BUILDKIT=1 -e EARTHBUILD_NO_BUILDKIT_UPDATE=1 -v /var/run/docker.sock:/var/run/docker.sock "${EARTHBUILD_IMAGE}" --no-cache github.com/EarthBuild/hello-world:4d466d524f768a379374c785fdef30470e87721d+hello 2>&1 | tee output.txt
    acbgrep "Hello World" output.txt
    acbgrep "earthbuild installation is working correctly" output.txt
fi

rm output.txt
echo "=== All tests have passed ==="
