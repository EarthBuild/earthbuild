#!/usr/bin/env bash
set -xeu

earthbuild=${earthbuild:=earthbuild}
if [ "$earthbuild" != "earthbuild" ]; then
  earthbuild=$(realpath "$earthbuild")
fi
echo "running tests with $earthbuild"
"$earthbuild" --version
frontend="${frontend:-$(which docker || which podman)}"
test -n "$frontend" || (>&2 echo "Error: frontend is empty" && exit 1)
echo "using frontend $frontend"

PATH="$(realpath "$(dirname "$0")/../acbtest"):$PATH"

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

# Test 1: export without anything
echo ==== Running test 1 ====
rm -rf /tmp/earthbuild-export-test-1
"$frontend" rmi earthbuild-export-test-1:test || true

mkdir /tmp/earthbuild-export-test-1
cd /tmp/earthbuild-export-test-1
cat >> Earthfile <<EOF
VERSION 0.7
test1:
    FROM busybox:latest
    SAVE IMAGE earthbuild-export-test-1:test
EOF

"$earthbuild" prune --reset
"$earthbuild" +test1

"$frontend" run --rm earthbuild-export-test-1:test

# Test 2: export with only a CMD set
echo ==== Running test 2 ====
rm -rf /tmp/earthbuild-export-test-2
"$frontend" rmi earthbuild-export-test-2:test || true

mkdir /tmp/earthbuild-export-test-2
cd /tmp/earthbuild-export-test-2
cat >> Earthfile <<EOF
VERSION 0.7
test2:
    FROM busybox:latest
    CMD echo "running default cmd"
    SAVE IMAGE earthbuild-export-test-2:test
EOF

"$earthbuild" prune --reset
"$earthbuild" +test2

"$frontend" run --rm earthbuild-export-test-2:test | acbgrep "running default cmd"

# Test 3: export with a single RUN
echo ==== Running test 3 ====
rm -rf /tmp/earthbuild-export-test-3
"$frontend" rmi earthbuild-export-test-3:test || true

mkdir /tmp/earthbuild-export-test-3
cd /tmp/earthbuild-export-test-3
cat >> Earthfile <<EOF
VERSION 0.7
test3:
    FROM busybox:latest
    RUN echo "hello my world" > /data
    SAVE IMAGE earthbuild-export-test-3:test
EOF

"$earthbuild" prune --reset
"$earthbuild" +test3

"$frontend" run --rm earthbuild-export-test-3:test cat /data | acbgrep "hello my world"


# Test 4: export multiplatform image
echo ==== Running test 4 ====
rm -rf /tmp/earthbuild-export-test-4
"$frontend" rmi earthbuild-export-test-4:test || true
"$frontend" rmi earthbuild-export-test-4:test_linux_amd64 || true
"$frontend" rmi earthbuild-export-test-4:test_linux_arm64 || true
"$frontend" rmi earthbuild-export-test-4:test_linux_arm_v7 || true

mkdir /tmp/earthbuild-export-test-4
cd /tmp/earthbuild-export-test-4
cat >> Earthfile <<EOF
VERSION 0.7

multi4:
    # NOTE: keep amd64 in the middle, since earthbuild will fallback to the first defined platform
    # in case loadDockerManifest fails
    BUILD --platform=linux/arm/v7 --platform=linux/amd64 --platform=linux/arm64 +test4

test4:
    FROM busybox:latest
    RUN echo "hello my world" > /data
    RUN uname -m >> /data
    SAVE IMAGE earthbuild-export-test-4:test
EOF

"$earthbuild" prune --reset
"$earthbuild" +multi4

"$frontend" run --rm earthbuild-export-test-4:test cat /data | acbgrep "hello my world"
"$frontend" run --rm earthbuild-export-test-4:test cat /data | acbgrep "$(uname -m)"
"$frontend" run --rm earthbuild-export-test-4:test_linux_amd64 cat /data | acbgrep "hello my world"
"$frontend" run --rm earthbuild-export-test-4:test_linux_amd64 cat /data | acbgrep "x86_64"
"$frontend" run --rm earthbuild-export-test-4:test_linux_arm64 cat /data | acbgrep "hello my world"
"$frontend" run --rm earthbuild-export-test-4:test_linux_arm64 cat /data | acbgrep "aarch64"
"$frontend" run --rm earthbuild-export-test-4:test_linux_arm_v7 cat /data | acbgrep "hello my world"
"$frontend" run --rm earthbuild-export-test-4:test_linux_arm_v7 cat /data | acbgrep "armv7l"


# Test 5: export multiple images
echo ==== Running test 5 ====
rm -rf /tmp/earthbuild-export-test-5
"$frontend" rmi earthbuild-export-test-5:test-img1 || true
"$frontend" rmi earthbuild-export-test-5:test-img2 || true

mkdir /tmp/earthbuild-export-test-5
cd /tmp/earthbuild-export-test-5
cat >> Earthfile <<EOF
VERSION 0.7

all5:
    BUILD +test5-img1
    BUILD +test5-img2

test5-img1:
    FROM busybox:latest
    RUN echo "hello my world 1" > /data
    SAVE IMAGE earthbuild-export-test-5:test-img1

test5-img2:
    FROM busybox:latest
    RUN echo "hello my world 2" > /data
    SAVE IMAGE earthbuild-export-test-5:test-img2
EOF

"$earthbuild" prune --reset
"$earthbuild" +all5

"$frontend" run --rm earthbuild-export-test-5:test-img1 cat /data | acbgrep "hello my world 1"
"$frontend" run --rm earthbuild-export-test-5:test-img2 cat /data | acbgrep "hello my world 2"

# Test 6: no manifest list
echo ==== Running test 6 ====
rm -rf /tmp/earthbuild-export-test-6
"$frontend" rmi earthbuild-export-test-6:test || true
"$frontend" rmi earthbuild-export-test-6:test_linux_arm64 || true

mkdir /tmp/earthbuild-export-test-6
cd /tmp/earthbuild-export-test-6
cat >> Earthfile <<EOF
VERSION 0.7

multi6:
    BUILD --platform=linux/arm64 +test6

test6:
    FROM busybox:latest
    RUN echo "hello my world" > /data
    RUN uname -m >> /data
    SAVE IMAGE --no-manifest-list earthbuild-export-test-6:test
EOF

"$earthbuild" prune --reset
"$earthbuild" +multi6

"$frontend" run --rm earthbuild-export-test-6:test cat /data | acbgrep "hello my world"
"$frontend" run --rm earthbuild-export-test-6:test cat /data | acbgrep "aarch64"
if "$frontend" inspect earthbuild-export-test-6:test_linux_arm64 >/dev/null 2>&1 ; then
    echo "Expected failure"
    exit 1
fi

# Test 7: remote cache on target with only BUILDs
echo ==== Running test 7 ====
rm -rf /tmp/earthbuild-export-test-7
mkdir /tmp/earthbuild-export-test-7
cd /tmp/earthbuild-export-test-7
cat >> Earthfile <<EOF
VERSION 0.7
test7:
    BUILD +b
b:
    FROM busybox:latest
EOF

# This simply tests that this does not hang (#1945).
timeout -k 11m 10m "$earthbuild" --ci --push --remote-cache earthbuild/test-cache:export-test-7 +test7

# Test 8: earthbuild LABELS
echo ==== Running test 8 ====
rm -rf /tmp/earthbuild-export-test-8
"$frontend" rmi earthbuild-export-test-8a:test || true
"$frontend" rmi earthbuild-export-test-8b:test || true

mkdir /tmp/earthbuild-export-test-8
cd /tmp/earthbuild-export-test-8
cat >> Earthfile <<EOF
VERSION 0.7

test8:
    FROM busybox:latest
    RUN echo "hello my world" > /data
    SAVE IMAGE earthbuild-export-test-8a:test
    LABEL foo=bar
    SAVE IMAGE earthbuild-export-test-8b:test
EOF

"$earthbuild" prune --reset
"$earthbuild" +test8

label_count=$("$frontend" inspect earthbuild-export-test-8a:test | jq .[].Config.Labels | grep -c dev.earthbuild.)
if [ "$label_count" -ne "3" ]; then
    echo "Expected 3 dev.earthbuild labels on first image; got $label_count"
    exit 1
fi

label_count=$("$frontend" inspect earthbuild-export-test-8b:test | jq .[].Config.Labels | grep -c dev.earthbuild.)
if [ "$label_count" -ne "3" ]; then
    echo "Expected 3 dev.earthbuild labels on second image; got $label_count"
    exit 1
fi

echo "=== All tests have passed ==="
