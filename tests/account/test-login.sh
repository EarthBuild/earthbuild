#!/bin/sh
set -eo pipefail # DONT add a set -x or you will leak the key

acbtest -n "$USER1_TOKEN"
acbtest -n "$USER2_TOKEN"
acbtest -n "$USER2_SSH_KEY"
acbtest -z "$SSH_AUTH_SOCK"

echo "== it should login to user1 with token =="
EARTHBUILD_TOKEN="$USER1_TOKEN" earthbuild account login 2>&1 | acbgrep 'Logged in as "other-service.earthbuild-user1@earthbuild.dev" using token auth'

echo "== it should stay logged in as user1 even though EARTHBUILD_TOKEN is no longer set =="
earthbuild account login 2>&1 | acbgrep 'Logged in as "other-service.earthbuild-user1@earthbuild.dev" using cached jwt auth'

echo "== it should stay logged in as user1 since the cached jwt is used (even though user2's ssh key is available via ssh keys) =="
eval "$(ssh-agent)"
echo "$USER2_SSH_KEY" | ssh-add -
ssh-add -l | acbgrep '(ED25519)'

earthbuild account login 2>&1 | acbgrep 'Logged in as "other-service.earthbuild-user1@earthbuild.dev" using cached jwt auth'

ssh-add -D # remove the key

echo "== forcing a logout should allow us to change users =="
earthbuild account logout
EARTHBUILD_TOKEN="$USER2_TOKEN" earthbuild account login 2>&1 | acbgrep 'Logged in as "other-service.earthbuild-user2@earthbuild.dev" using token auth'

echo "== it should stay logged in as user2 =="
earthbuild account login 2>&1 | acbgrep 'Logged in as "other-service.earthbuild-user2@earthbuild.dev" using cached jwt auth'

echo "== it should be able to login as user2 with ssh =="
earthbuild account logout
echo "$USER2_SSH_KEY" | ssh-add -
earthbuild account login 2>&1 | acbgrep 'Logged in as "other-service.earthbuild-user2@earthbuild.dev" using ssh auth'

echo "== using token param should behave similarly to EARTHBUILD_TOKEN env =="
earthbuild account login --token "$USER2_TOKEN" 2>&1 | acbgrep 'Logged in as "other-service.earthbuild-user2@earthbuild.dev" using token auth'

echo "== same as above but first ensure we're logged out =="
earthbuild account logout
rm -vf ~/.earthbuild/auth.*
earthbuild account login --token "$USER2_TOKEN" 2>&1 | acbgrep 'Logged in as "other-service.earthbuild-user2@earthbuild.dev" using token auth'

echo "== ensure auth files are recreated =="
acbtest -f ~/.earthbuild/auth.credentials
acbtest -f ~/.earthbuild/auth.jwt
