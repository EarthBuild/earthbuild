#!/bin/bash
set -eu

earthbuild=$(pwd)/build/linux/amd64/earthbuild
dockerfiles="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

echo "=== Testing Dockerfile1 ==="
cd "$(mktemp -d)"
echo "working out of $(pwd)"
cp "$dockerfiles"/Dockerfile1 Dockerfile
$earthbuild docker2earthbuild --tag=myimage:latest
$earthbuild +build
docker run --rm myimage:latest say-hi | grep hello

echo "=== Testing Dockerfile2 ==="
cd "$(mktemp -d)"
echo "working out of $(pwd)"
cat "$dockerfiles"/Dockerfile2 | $earthbuild docker2earthbuild --dockerfile - --tag myotherimage:test
cp "$dockerfiles"/app.go .
$earthbuild +build
docker run --rm myotherimage:test | grep greetings

echo "=== Testing args-before-from.Dockerfile ==="
cd "$(mktemp -d)"
echo "working out of $(pwd)"
$earthbuild docker2earthbuild --dockerfile - --tag onemoreimage:test < "$dockerfiles"/args-before-from.Dockerfile
cp "$dockerfiles"/app.go .
$earthbuild +build --BASE=golang --GO_MAJOR=1
docker run --rm onemoreimage:test | grep greetings
