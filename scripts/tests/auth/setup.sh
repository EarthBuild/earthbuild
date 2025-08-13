#!/bin/bash
set -eu

if [ -z ${GITHUB_ACTIONS+x} ]; then
    echo "this script should only be run from GHA; if run locally it will modify your ssh settings and earthbuild config"
    exit 1
fi

earthbuild=${earthbuild:=earthbuild}
earthbuild=$(realpath "$earthbuild")
echo "running tests with $earthbuild"

# ensure earthbuild login works (and print out who gets logged in)
"$earthbuild" account login

# these tests require the EARTHBUILD_TOKEN not be set
unset EARTHBUILD_TOKEN

# make sure ssh-agent is not running
test -z "${SSH_AUTH_SOCK:-}"

# make sure tests start without a config
rm -f ~/.earthbuild-dev/config.yml
