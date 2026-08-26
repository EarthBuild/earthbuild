#!/bin/bash
set -euo pipefail # don't use -x as it will leak the mirror credentials

# to run this locally; in the root of the repo:
#   ./earth +earthly-docker && EARTH_IMAGE="ghcr.io/earthbuild/earthbuild:dev-$(git rev-parse --abbrev-ref HEAD | sed 's/\//_/g')" scripts/tests/earth-image.sh

FRONTEND=${FRONTEND:-docker}
EARTH_IMAGE="${EARTH_IMAGE:-ghcr.io/earthbuild/earthbuild:dev-main}"
PATH="$(realpath "$(dirname "$0")/../acbtest"):$PATH"

dockerconfig="$(mktemp /tmp/earthbuild-image-test-docker-config.XXXXXX)"
chmod 600 "$dockerconfig"
cat > "$dockerconfig" <<EOF
{}
EOF

# Note that it is not possible to use GLOBAL_CONFIG for this, due to the fact
# earth-entrypoint.sh starts buildkit instead of the earth binary,
# as a result the buildkit_additional_config value in /etc/.<installation name>/config.yml is ignored.
export EARTH_ADDITIONAL_BUILDKIT_CONFIG='[registry."docker.io"]
  mirrors = ["mirror.gcr.io", "public.ecr.aws"]'

function finish {
  status="$?"
  if [ "$status" = "0" ]; then
    echo "earth-image.sh test passed"
  else
    echo "earth-image.sh failed with $status"
  fi
  rm "$dockerconfig"
}
trap finish EXIT

echo "Test no --privileged and no NO_BUILDKIT=1 -> fail."
if "$FRONTEND" run --rm "${EARTH_IMAGE}" 2>&1 | tee output.txt; then
    echo "expected failure"
    exit 1
fi
acbgrep "Container appears to be running unprivileged" output.txt

echo "Test no target provided -> fail."
if "$FRONTEND" run --rm --privileged "${EARTH_IMAGE}" 2>&1 | tee output.txt; then
    echo "expected failure"
    exit 1
fi
acbgrep "Executes earth builds" output.txt # Display help
acbgrep "no target reference provided" output.txt # Show error
if "$FRONTEND" run --rm -e NO_BUILDKIT=1 "${EARTH_IMAGE}" 2>&1 | tee output.txt; then
    echo "expected failure"
    exit 1
fi
acbgrep "Executes earth builds" output.txt # Display help
acbgrep "no target reference provided" output.txt # Show error

echo "Test the CLI is on PATH under its released name."
# The image must expose the CLI as 'earth' on PATH, matching the name the
# release binaries are published under.
"$FRONTEND" run --rm --entrypoint sh "${EARTH_IMAGE}" -c 'command -v earth' 2>&1 | tee output.txt
acbgrep "/earth$" output.txt
"$FRONTEND" run --rm -e NO_BUILDKIT=1 --entrypoint earth "${EARTH_IMAGE}" --version 2>&1 | tee output.txt
acbgrep "^earth version" output.txt

echo "Test the deprecated name still resolves, and says so."
# 'ls' with no Earthfile in the workdir exits non-zero; the rename notice is
# emitted by the before hook, ahead of that error.
"$FRONTEND" run --rm -e NO_BUILDKIT=1 --entrypoint earthly "${EARTH_IMAGE}" ls 2>&1 | tee output.txt || true
acbgrep "the earthly binary has been renamed to earth" output.txt
acbgrep "you can .rm /usr/bin/earthly" output.txt

echo "Test the entrypoint is on PATH under its released name."
# The CI integration guides name the entrypoint explicitly, so both the current
# and the deprecated spelling must resolve to the same script.
"$FRONTEND" run --rm --entrypoint sh "${EARTH_IMAGE}" -c \
    'test -f /usr/bin/earth-entrypoint.sh && test "$(readlink /usr/bin/earthly-entrypoint.sh)" = earth-entrypoint.sh'

echo "Test the config path follows the installation name."
# EARTH_EXEC_CMD makes the entrypoint export the path it resolved and hand over
# to the given command, so 'env' reports the resolution without a build.
"$FRONTEND" run --rm -e NO_BUILDKIT=1 -e EARTH_EXEC_CMD=env "${EARTH_IMAGE}" 2>&1 | tee output.txt
acbgrep "^earth_config=/etc/.earth/config.yml$" output.txt

echo "Test a config mounted at the deprecated location is still honoured."
# A caller mounting the pre-rename path must keep working for one deprecation
# cycle, so it wins over the derived path when nothing is mounted there.
deprecated_config="$(mktemp /tmp/earthbuild-image-test-config.XXXXXX)"
cat > "$deprecated_config" <<EOF
global:
  disable_analytics: true
EOF
"$FRONTEND" run --rm -e NO_BUILDKIT=1 -e EARTH_EXEC_CMD=env \
    -v "$deprecated_config:/etc/.earthly/config.yml" "${EARTH_IMAGE}" 2>&1 | tee output.txt
rm "$deprecated_config"
acbgrep "^earth_config=/etc/.earthly/config.yml$" output.txt
# The deprecated spelling is still exported for callers that read it.
acbgrep "^earthly_config=/etc/.earthly/config.yml$" output.txt

# A trivial build that only needs the parser, so the deprecation scan can be
# observed without standing up buildkit.
deprecation_probe='cd /tmp && printf "VERSION 0.8\nfoo:\n    FROM alpine\n" > Earthfile && earth ls'

echo "Test the image does not emit deprecation warnings for its own configuration."
# The deprecation scan cannot tell a user-set EARTHLY_* variable from one
# earthbuild set on itself, so the image must only ever set EARTH_* names.
# See https://github.com/EarthBuild/earthbuild/issues/751.
"$FRONTEND" run --rm --entrypoint sh "${EARTH_IMAGE}" -c "$deprecation_probe" 2>&1 | tee output.txt
acbgrep "+foo" output.txt # the probe really ran
# Match the env-var warning shape specifically, so the unrelated
# earthly-is-now-earth binary rename notice does not trip this.
if grep -E "WARNING: EARTHLY_[A-Z0-9_]+ is deprecated" output.txt; then
    echo "the image emitted deprecation warnings the user cannot act on"
    exit 1
fi

echo "Test a user-set EARTHLY_ variable still warns, and is still honoured."
"$FRONTEND" run --rm --entrypoint sh -e EARTHLY_TMP_DIR=/tmp/earthbuild "${EARTH_IMAGE}" -c "$deprecation_probe" 2>&1 | tee output.txt
acbgrep "WARNING: EARTHLY_TMP_DIR is deprecated. Use EARTH_TMP_DIR." output.txt

echo "Test --version (smoke test)."
"$FRONTEND" run --rm --privileged "${EARTH_IMAGE}" --version 2>&1
"$FRONTEND" run --rm -e NO_BUILDKIT=1 "${EARTH_IMAGE}" --version 2>&1

echo "Test --help."
"$FRONTEND" run --rm --privileged "${EARTH_IMAGE}" --help 2>&1 | tee output.txt
acbgrep "Executes earth builds" output.txt # Display help
"$FRONTEND" run --rm -e NO_BUILDKIT=1 "${EARTH_IMAGE}" --help 2>&1 | tee output.txt
acbgrep "Executes earth builds" output.txt # Display help

echo "Test hello world with embedded buildkit."
"$FRONTEND" run --rm --privileged -e EARTH_ADDITIONAL_BUILDKIT_CONFIG -v "$dockerconfig:/root/.docker/config.json" "${EARTH_IMAGE}" --no-cache github.com/EarthBuild/hello-world:4d466d524f768a379374c785fdef30470e87721d+hello 2>&1 | tee output.txt
acbgrep "Hello World" output.txt
acbgrep "Earthly installation is working correctly" output.txt

if [ "$FRONTEND" = "docker" ]; then
    echo "Test use /var/run/docker.sock, but not privileged."
    "$FRONTEND" run --rm -e EARTH_ADDITIONAL_BUILDKIT_CONFIG -v "$dockerconfig:/root/.docker/config.json" -e NO_BUILDKIT=1 -e EARTH_NO_BUILDKIT_UPDATE=1 -v /var/run/docker.sock:/var/run/docker.sock "${EARTH_IMAGE}" --no-cache github.com/EarthBuild/hello-world:4d466d524f768a379374c785fdef30470e87721d+hello 2>&1 | tee output.txt
    acbgrep "Hello World" output.txt
    acbgrep "Earthly installation is working correctly" output.txt
fi

rm output.txt
echo "=== All tests have passed ==="
