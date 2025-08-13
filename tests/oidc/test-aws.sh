#!/bin/sh
set -eo pipefail # DONT add a set -x or you will leak the key

acbtest -n "$OIDC_USER_TOKEN"
acbtest -n "$ROLE_ARN"
acbtest -n "$earthbuild_config" # set by earthbuild-entrypoint.sh


echo "== it should login to user with token =="
EARTHBUILD_TOKEN="$OIDC_USER_TOKEN" earthbuild account login 2>&1 | acbgrep 'Logged in as "other-service+oidc-ci-test@earthbuild.dev" using token auth'

echo "== it should access aws via oidc =="
earthbuild --config "$earthbuild_config" +oidc --ROLE_ARN="$ROLE_ARN"

echo "== it should access aws via oidc-with-docker =="
earthbuild --config "$earthbuild_config" --allow-privileged +oidc-with-docker --ROLE_ARN="$ROLE_ARN"
