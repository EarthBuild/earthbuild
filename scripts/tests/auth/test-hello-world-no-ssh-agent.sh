#!/bin/bash
set -eu # don't use -x as it will leak the private key
# shellcheck source=./setup.sh
source "$(dirname "$0")/setup.sh"

# test earthbuild can access a public repo
"$earthbuild" github.com/EarthBuild/hello-world:main+hello
