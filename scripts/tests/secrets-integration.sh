#!/usr/bin/env bash
set -eu # don't use -x as it will leak the private key

earthbuild=${earthbuild:=earthbuild}
if [ "$earthbuild" != "earthbuild" ]; then
  earthbuild=$(realpath "$earthbuild")
fi
echo "running tests with $earthbuild"
"$earthbuild" --version

PATH="$(realpath "$(dirname "$0")/../acbtest"):$PATH"

# prevent the self-update of earthbuild from running (this ensures no bogus data is printed to stdout,
# which would mess with the secrets data being fetched)
date +%s > /tmp/last-earthbuild-prerelease-check

set +x # dont remove or the token will be leaked
test -n "$EARTHBUILD_TOKEN" || (echo "error: EARTHBUILD_TOKEN is not set" && exit 1)
set -x

EARTHBUILD_INSTALLATION_NAME="earthbuild.integration"
export EARTHBUILD_INSTALLATION_NAME
rm -rf "$HOME/.earthbuild.integration/"

# ensure earthbuild login works (and print out who gets logged in)
"$earthbuild" account login

# test logout has no effect when EARTHBUILD_TOKEN is set
if GITHUB_ACTIONS="" NO_COLOR=0 "$earthbuild" account logout > output 2>&1; then
    echo "earthbuild account logout should have failed"
    exit 1
fi
diff output <(echo "Error: account logout has no effect when --auth-token (or the EARTHBUILD_TOKEN environment variable) is set")

# fetch shared secret key (this step assumes your personal user has access to the /earthbuild-technologies/ secrets org
echo "fetching manitou-id_rsa"
ID_RSA=$("$earthbuild" secrets --org earthbuild-technologies --project core get -n secrets-integration-manitou-id_rsa)

# now that we grabbed the manitou credentials, unset our token, to ensure that we're only testing using manitou's credentials
unset EARTHBUILD_TOKEN
"$earthbuild" account logout

echo starting new instance of ssh-agent, and loading credentials
eval "$(ssh-agent)"

# grab first 6chars of md5sum of key to help sanity check that the same key is consistently used
set +x # make sure we don't print the key here
md5sum=$(echo -n "$ID_RSA" | md5sum | awk '{ print $1 }' | head -c6)

echo "Adding key (with md5sum $md5sum...) into ssh-agent"
echo "$ID_RSA" | ssh-add -

echo testing that key was correctly loaded into ssh-agent
ssh-add -l | acbgrep manitou

echo testing that the ssh-agent only contains a single key
test "$(ssh-add -l | wc -l)" = "1"

echo "testing earthbuild account login works (and is using the earthbuild-manitou account)"
"$earthbuild" account login 2>&1 | acbgrep 'Logged in as "other-service+earthbuild-manitou@earthbuild.dev" using ssh auth'

mkdir -p /tmp/earthtest
cat << EOF > /tmp/earthtest/Earthfile
VERSION 0.7
PROJECT manitou-org/earthbuild-core-integration-test
FROM alpine:3.18
test-local-secret:
    WORKDIR /test
    RUN --mount=type=secret,target=/tmp/test_file,id=my_secret test "\$(cat /tmp/test_file)" = "my-local-value"
test-server-secret:
    WORKDIR /test
    RUN --mount=type=secret,target=/tmp/test_file,id=my_test_file test "\$(cat /tmp/test_file)" = "secret-value"
EOF

# set and test get returns the correct value
"$earthbuild" secrets --org manitou-org --project earthbuild-core-integration-test set my_test_file "secret-value"
"$earthbuild" secrets --org manitou-org --project earthbuild-core-integration-test get my_test_file | acbgrep 'secret-value'

# test earthbuild will prompt if value is missing
/usr/bin/expect -c '
spawn '"$earthbuild"' secrets --org manitou-org --project earthbuild-core-integration-test set my_test_file
expect "secret value: "
send "its my secret value\n"
expect eof
'
"$earthbuild" secrets --org manitou-org --project earthbuild-core-integration-test get my_test_file | acbgrep 'its my secret value'

# test set --stdin works
echo -e "hello\nworld" | "$earthbuild" secrets --org manitou-org --project earthbuild-core-integration-test set --stdin my_test_file
# note "echo -e "hello\nworld" | md5sum" -> 0f723ae7f9bf07744445e93ac5595156
"$earthbuild" secrets --org manitou-org --project earthbuild-core-integration-test get -n my_test_file
"$earthbuild" secrets --org manitou-org --project earthbuild-core-integration-test get -n my_test_file | md5sum | acbgrep '0f723ae7f9bf07744445e93ac5595156'

# test set --file works
"$earthbuild" secrets --org manitou-org --project earthbuild-core-integration-test set --file <(echo -e "foo\nbar") my_test_file
# note "echo -e "foo\nbar" | md5sum" -> f47c75614087a8dd938ba4acff252494
"$earthbuild" secrets --org manitou-org --project earthbuild-core-integration-test get -n my_test_file | md5sum | acbgrep 'f47c75614087a8dd938ba4acff252494'


# restore the "secret-value", which the org selection test requires
"$earthbuild" secrets --org manitou-org --project earthbuild-core-integration-test set my_test_file "secret-value"

# test selecting org
"$earthbuild" org select manitou-org
"$earthbuild" org ls | acbgrep '^\* \+manitou-org'

# test secrets with org selected in config file
"$earthbuild" secrets --project earthbuild-core-integration-test get my_test_file | acbgrep 'secret-value'
"$earthbuild" secrets --project earthbuild-core-integration-test set my_other_file "super-secret-value"
"$earthbuild" secrets --project earthbuild-core-integration-test get my_other_file | acbgrep 'super-secret-value'
"$earthbuild" secrets --project earthbuild-core-integration-test ls | acbgrep '^my_test_file$'

# test secrets with personal org
"$earthbuild" org select user:other-service+earthbuild-manitou@earthbuild.dev
"$earthbuild" secrets set super/secret hello
"$earthbuild" secrets get super/secret | acbgrep 'hello'
"$earthbuild" secrets get /user/super/secret | acbgrep 'hello'
"$earthbuild" secrets ls | acbgrep '^super/secret$'
"$earthbuild" secrets ls /user | acbgrep '^super/secret$'

echo "=== test 1 ==="
# test RUN --mount can reference a secret from the command line
"$earthbuild" --no-cache --secret my_secret=my-local-value /tmp/earthtest+test-local-secret

echo "=== test 2 ==="
# test RUN --mount can reference a secret from the server that is only specified in the Earthfile
"$earthbuild" --no-cache /tmp/earthtest+test-server-secret

echo "=== test 3 ==="
# Test earthbuild will display a message containing the name of the secret that was not found
set +e
"$earthbuild" --no-cache /tmp/earthtest+test-local-secret > output 2>&1
exit_code="$?"
set -e
cat output
test "$exit_code" != "0"
acbgrep 'unable to lookup secret "my_secret": not found' output
acbgrep 'Help: Make sure to set the project at the top of the Earthfile' output
echo "=== All tests have passed ==="
