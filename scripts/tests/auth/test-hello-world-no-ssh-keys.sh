#!/bin/bash
set -eu # don't use -x as it will leak the private key
# shellcheck source=./setup.sh
source "$(dirname "$0")/setup.sh"

# start ssh-agent and make sure no keys are loaded
eval "$(ssh-agent)"
ssh-add -l | grep 'The agent has no identities'

# test earthbuild can access a public repo
"$earthbuild" github.com/EarthBuild/hello-world:main+hello
