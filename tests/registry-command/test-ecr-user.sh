#!/bin/sh
set -ex

# WARNING -- RACE-CONDITION: this test is not thread-safe (since it makes use of a shared user's secrets)
# the lock.sh and unlock.sh scripts must first be run

clearusersecrets() {
    earthbuild secrets ls /user/std/ | xargs -r -n 1 earthbuild secrets rm
}

test -n "$earthbuild_config" # set by earthbuild-entrypoint.sh
test -n "$ECR_REGISTRY_HOST"

# clear out secrets from previous test
clearusersecrets

# test credentials do not exist
earthbuild registry list | grep -v $ECR_REGISTRY_HOST

# set ecr credentials
set +x # don't remove, or keys will be leaked
test -n "$AWS_ACCESS_KEY_ID" || (echo "AWS_ACCESS_KEY_ID is empty" && exit 1)
test -n "$AWS_SECRET_ACCESS_KEY" || (echo "AWS_SECRET_ACCESS_KEY is empty" && exit 1)
set -x
earthbuild registry setup --cred-helper=ecr-login "$ECR_REGISTRY_HOST"
echo "done setting up cred helper (and secrets)"

earthbuild registry list | grep "$ECR_REGISTRY_HOST"

uuid="$(uuidgen)"

cat > Earthfile <<EOF
VERSION 0.7
pull:
  FROM $ECR_REGISTRY_HOST/integration-test:latest
  RUN test -f /etc/passwd

push:
  FROM alpine
  RUN echo $uuid > /some-data
  SAVE IMAGE --push $ECR_REGISTRY_HOST/integration-test:latest
EOF

# --no-output is required for earthbuild-in-earthbuild; however a --push to ecr will still occur
earthbuild --config "$earthbuild_config" --verbose +pull
earthbuild --config "$earthbuild_config" --no-output --push --verbose +push

earthbuild registry remove "$ECR_REGISTRY_HOST"
earthbuild registry list | grep -v $ECR_REGISTRY_HOST

# clear out secrets (just in case project-based registry accidentally uses user-based)
clearusersecrets
