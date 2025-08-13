#!/bin/bash
# Note: Most of this test runs as earthbuild-in-earthbuild so that we can easily mess with the earthbuild config
#       without the host earthbuild's config being affected.

set -uxe
set -o pipefail

testdir="$(realpath $(dirname "$0"))"

earthbuild=${earthbuild-"$testdir/../../build/linux/amd64/earthbuild"}
# docker / podman
frontend="${frontend:-$(which docker || which podman)}"
test -n "$frontend" || (>&2 echo "Error: frontend is empty" && exit 1)

# Cleanup previous run.
"$frontend" stop registry || true
"$frontend" rm registry || true
"$frontend" network disconnect registry-certs earthbuild-buildkitd || true
"$frontend" network rm registry-certs || true
rm -rf "$testdir/certs" || true

# Define network settings. I've chosen a subnet that is unlikely to conflict
# with default Docker networks.
export REGISTRY_IP="172.29.0.2"
export REGISTRY="$REGISTRY_IP"
SUBNET="172.29.0.0/16"

# Create user defined network.
"$frontend" network create --subnet="$SUBNET" -d bridge registry-certs

# Generate certs.
"$earthbuild" \
    --build-arg REGISTRY \
    --build-arg REGISTRY_IP \
     "$testdir/+certs"

# Run registry. This will use the same IP address as allocated above.
"$frontend" run --rm -d \
    --network registry-certs \
    --ip "$REGISTRY_IP" \
    -v "$testdir"/certs:/certs \
    -e REGISTRY_HTTP_ADDR=0.0.0.0:443 \
    -e REGISTRY_HTTP_TLS_CERTIFICATE=/certs/domain.crt \
    -e REGISTRY_HTTP_TLS_KEY=/certs/domain.key \
    -p "127.0.0.1:5443:443" \
    --name registry registry:2

# Ensure buildkitd can connect to the registry-certs network so that
# build containers can communicate with the registry.
"$frontend" network connect registry-certs earthbuild-buildkitd

# Test.
set +e
"$earthbuild" --allow-privileged \
    --ci \
    --build-arg REGISTRY \
    --build-arg REGISTRY_IP \
    "$@" \
    "$testdir/+all"
exit_code="$?"
set -e

# Cleanup.
"$frontend" stop registry || true

exit "$exit_code"
