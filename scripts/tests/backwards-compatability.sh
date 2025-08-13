#!/usr/bin/env bash
set -xeu

# used to start earthbuild-integration-buildkitd
earthbuild="${earthbuild:=earthbuild}"
if [ -f "$earthbuild" ]; then
  earthbuild=$(realpath "$earthbuild")
fi

# used for testing backwards compatability issues
crustly="${crustly:=earthbuild-v0.8.0}"
if [ -f "$crustly" ]; then
  crustly=$(realpath "$crustly")
fi

# change directory to script location
cd -- "$( dirname -- "${BASH_SOURCE[0]}" )"

current_git_sha="$(git rev-parse HEAD)"

if "$("$crustly" --version)" | grep "$current_git_sha" >/dev/null; then
  echo "ERROR: $crustly was built using the current git sha $current_git_sha"
  exit 1
fi

echo "running tests with earthbuild=$earthbuild for bootstrapping and crustly=$crustly for cli"
echo "earthbuild=$("$earthbuild" --version)"
echo "crustly=$("$crustly" --version)"
frontend="${frontend:-$(which docker || which podman)}"
test -n "$frontend" || (>&2 echo "Error: frontend is empty" && exit 1)
echo "using frontend=$frontend"

PATH="$(realpath ../acbtest):$PATH"

# prevent the self-update of earthbuild from running (this ensures no bogus data is printed to stdout,
# which would mess with the secrets data being fetched)
date +%s > /tmp/last-earthbuild-prerelease-check

set +x # dont remove or the token will be leaked
test -n "$EARTHBUILD_TOKEN" || (echo "error: EARTHBUILD_TOKEN is not set" && exit 1)
set -x

EARTHBUILD_INSTALLATION_NAME="earthbuild-integration"
export EARTHBUILD_INSTALLATION_NAME
rm -rf "$HOME/.earthbuild.integration/"

echo "$earthbuild"
# ensure earthbuild login works (and print out who gets logged in)
"$earthbuild" account login

# start buildkitd container
"$earthbuild" bootstrap

# start a build using an older version of the earthbuild cli
"$crustly" --no-buildkit-update -P ../../tests/with-docker+all

# validate buildkitd container was compiled using the current branch
buildkitd_EARTHLY_VERSION="$(docker logs earthbuild-integration-buildkitd |& grep -o 'EARTHBUILD_GIT_HASH=[a-z0-9]*')"
acbtest "$buildkitd_EARTHLY_VERSION" = "EARTHBUILD_GIT_HASH=$current_git_sha"

echo "=== All tests have passed ==="
