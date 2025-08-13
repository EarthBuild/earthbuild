#!/bin/bash
set -xeu

received_interrupt=0
function interrupt() {
    echo "received interrupt"
    received_interrupt=1
}
trap interrupt INT

function cleanup() {
    status="$?"
    if [ "$received_interrupt" = "0" ]; then
        set +e
        echo "killing background jobs"
        jobs
        jobs -p | xargs -r kill -9
        set -e
        wait
        echo "killing background jobs done"
        if [ "$status" = "0" ]; then
          echo "buildkite-test passed"
        else
          echo "=== buildkit logs ==="
          docker logs earthbuild-dev-buildkitd || true
          echo "=== end of buildkit logs ==="
          echo "buildkite-test failed with $status"
        fi
    fi
}
trap cleanup EXIT

os="$(uname)"
arch="$(uname -m)"

if [ "$os" = "Darwin" ]; then
    if [ "$arch" = "arm64" ]; then
        EARTHBUILD_OS="darwin-m1"
        download_url="https://github.com/earthbuild/earthbuild/releases/latest/download/earthbuild-darwin-arm64"
        earthbuild="./build/darwin/arm64/earthbuild"
    else
        EARTHBUILD_OS="darwin"
        download_url="https://github.com/earthbuild/earthbuild/releases/latest/download/earthbuild-darwin-amd64"
        earthbuild="./build/darwin/amd64/earthbuild"
    fi
elif [ "$os" = "Linux" ]; then
    EARTHBUILD_OS="linux"
    download_url="https://github.com/earthbuild/earthbuild/releases/latest/download/earthbuild-linux-amd64"
    earthbuild="./build/linux/amd64/earthbuild"
else
    echo "failed to handle $os, $arch"
    exit 1
fi

set +xu
echo "Running under pid=$$; arch=$(uname -m)"
for k in BUILDKITE_AGENT_ID BUILDKITE_BUILD_ID BUILDKITE_JOB_ID; do
    echo "$k=${!k}"
done
set -xu

if ! git symbolic-ref -q HEAD >/dev/null; then
    echo "Add branch info back to git (Earthbuild uses it for tagging)"
    git checkout -B "$BUILDKITE_BRANCH" || true
fi

echo "Download latest Earthbuild binary"
if [ -n "$download_url" ]; then
    curl -o ./earthbuild-released -L "$download_url" && chmod +x ./earthbuild-released
    released_earthbuild=./earthbuild-released
fi

echo "docker login"
set +x # dont echo secrets
DOCKER_USER="$("$released_earthbuild" secret --org earthbuild-technologies --project core get -n dockerhub/user)"
DOCKER_TOKEN="$("$released_earthbuild" secret --org earthbuild-technologies --project core get -n dockerhub/token)"
test -n "$DOCKER_USER" || (echo "failed to get DOCKER_USER" && exit 1)
test -n "$DOCKER_TOKEN" || (echo "failed to get DOCKER_TOKEN" && exit 1)
echo "$DOCKER_TOKEN" | docker login --username "$DOCKER_USER" --password-stdin
set -x

echo "Prune cache for cross-version compatibility"
"$released_earthbuild" prune --reset

echo "Build latest Earthbuild using released Earthbuild"
"$released_earthbuild" --version
"$released_earthbuild" config global.disable_analytics true
"$released_earthbuild" +for-"$EARTHBUILD_OS"
chmod +x "$earthbuild"

# WSL2 sometimes gives a "Text file busy" when running the native binary, likely due to crossing the WSL/Windows divide.
# This should be enough retry to skip that, and fail if theres _actually_ a problem.
att_max=5
att_num=1
until "$earthbuild" --version || (( att_num == att_max ))
do
    echo "Attempt $att_num failed! Trying again in $att_num seconds..."
    sleep $(( att_num++ ))
done

"$earthbuild" config global.buildkit_max_parallelism 2

# Yes, there is a bug in the upstream YAML parser. Sorry about the jank here.
# https://github.com/go-yaml/yaml/issues/423
"$earthbuild" config global.buildkit_additional_config "'[registry.\"docker.io\"]
 mirrors = [\"mirror.gcr.io\"]'"

# setup secrets
set +x # dont echo secrets
echo "DOCKERHUB_USER=$($earthbuild secret --org earthbuild-technologies --project core get -n dockerhub/user || kill $$)" > .secret
echo "DOCKERHUB_PASS=$($earthbuild secret --org earthbuild-technologies --project core get -n dockerhub/pass || kill $$)" >> .secret
echo "DOCKERHUB_MIRROR_USER=$($earthbuild secret --org earthbuild-technologies --project core get -n dockerhub-mirror/user || kill $$)" > .secret
echo "DOCKERHUB_MIRROR_PASS=$($earthbuild secret --org earthbuild-technologies --project core get -n dockerhub-mirror/pass || kill $$)" >> .secret
# setup args
echo "DOCKERHUB_MIRROR_AUTH=false" > .arg
echo "DOCKERHUB_MIRROR=mirror.gcr.io" >> .arg
set -x

# stop the released Earthbuild buildkitd container (to preserve memory)
docker rm -f earthbuild-buildkitd 2> /dev/null || true

max_attempts=2
for target in \
        +test-misc-group1 \
        +test-misc-group2 \
        +test-ast-group1 \
        +test-ast-group2 \
        +test-ast-group3 \
        +test-no-qemu-group1 \
        +test-no-qemu-group2 \
        +test-no-qemu-group3 \
        +test-no-qemu-group4 \
        +test-no-qemu-group5 \
        +test-no-qemu-group6 \
        +test-no-qemu-group7 \
        +test-no-qemu-group8 \
        +test-no-qemu-group9 \
        +test-no-qemu-group10 \
        +test-no-qemu-group11 \
        +test-no-qemu-group12 \
        +test-no-qemu-slow \
        +test-qemu \
        ; do
    for attempt in $(seq 1 "$max_attempts"); do
        # kill Earthbuild-* containers to release memory (the macstadium machines have limited memory)
        set +e
        docker ps -a | grep earthbuild- | awk '{print $1}' | xargs -r docker rm -f
        set -e

        echo "=== running $target (attempt $attempt/$max_attempts ==="
        set +e
        "$earthbuild" --ci -P --exec-stats-summary=- "$target"
        exit_code="$?"
        set -e

        if [ "$exit_code" = "0" ]; then
            echo "$target passed"
            break
        fi

        echo "$target failed"
        if [ "$attempt" = "$max_attempts" ]; then
            echo "final attempt reached, giving up"
            exit 1
        fi
    done
done

echo "Execute fail test"
bash -c "! $earthbuild --ci ./tests/fail+test-fail"
