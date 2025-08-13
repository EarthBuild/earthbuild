#!/bin/sh
set -ex

# WARNING -- RACE-CONDITION: this test is not thread-safe (since it makes use of a shared user's secrets)
# the lock.sh and unlock.sh scripts must first be run

clearusersecrets() {
    earthbuild secrets ls /user/std/ | xargs -r -n 1 earthbuild secrets rm
}

test -n "$earthbuild_config" # set by earthbuild-entrypoint.sh

# clear out secrets from previous test
clearusersecrets

# test credentials do not exist
earthbuild registry list | grep -v "$GCP_SERVER"

# set credentials
set +x # don't remove, or keys will be leaked
test -n "$GCP_KEY" || (echo "GCP_KEY is empty" && exit 1)
export GCP_SERVICE_ACCOUNT_KEY="$GCP_KEY" # registry setup reads from this env
set -x
earthbuild registry setup --cred-helper=gcloud "$GCP_SERVER"

# test credentials exist
earthbuild registry list | grep "$GCP_SERVER"

uuid="$(uuidgen)"

cat > Earthfile <<EOF
VERSION 0.7
pull:
  FROM $GCP_FULL_ADDRESS/$IMAGE:latest
  RUN test -f /etc/passwd

push:
  FROM alpine
  RUN echo $uuid > /some-data
  SAVE IMAGE --push $GCP_FULL_ADDRESS/$IMAGE:latest
EOF

# --no-output is required for earthbuild-in-earthbuild; however a --push to gcp will still occur
earthbuild --config "$earthbuild_config" --verbose +pull
earthbuild --config "$earthbuild_config" --no-output --push --verbose +push

earthbuild registry remove "$GCP_SERVER"
earthbuild registry list | grep -v $GCP_SERVER

# clear out secrets (just in case project-based registry accidentally uses user-based)
clearusersecrets
