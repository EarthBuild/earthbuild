#!/bin/sh
set -ex

# WARNING -- RACE-CONDITION: this test is not thread-safe (since it makes use of a shared project's secrets)
# the lock.sh and unlock.sh scripts must first be run

ORG="ryan-test"
PROJECT="registry-command-test-project"

clearprojectsecrets() {
    earthbuild secrets --org "$ORG" --project "$PROJECT" ls /user/std/registry | xargs -r -n 1 earthbuild secrets --org "$ORG" --project "$PROJECT" rm
}

# clear out secrets from previous test
clearprojectsecrets

# test dockerhub credentials do not exist
earthbuild registry --org "$ORG" --project "$PROJECT" list | grep -v registry-1.docker.io

# set dockerhub credentials
earthbuild registry --org "$ORG" --project "$PROJECT" setup --username myprojecttest --password keepitsecret

# test dockerhub credentials exist
earthbuild registry --org "$ORG" --project "$PROJECT" list | grep registry-1.docker.io

# test username and password were correctly stored in underlying std secret
test "$(earthbuild secrets --org "$ORG" --project "$PROJECT" get std/registry/registry-1.docker.io/username)" = "myprojecttest"
test "$(earthbuild secrets --org "$ORG" --project "$PROJECT" get std/registry/registry-1.docker.io/password)" = "keepitsecret"

# test a different host
echo -n keepitsecret2  | earthbuild registry --org "$ORG" --project "$PROJECT" setup --username myprojecttest2 --password-stdin corp-registry.earthbuild.dev

# both dockerhub and corp-registry should exist
earthbuild registry --org "$ORG" --project "$PROJECT" list | grep registry-1.docker.io
earthbuild registry --org "$ORG" --project "$PROJECT" list | grep corp-registry.earthbuild.dev

# test username and password were correctly stored in underlying std secret
test "$(earthbuild secrets --org "$ORG" --project "$PROJECT" get std/registry/registry-1.docker.io/username)" = "myprojecttest"
test "$(earthbuild secrets --org "$ORG" --project "$PROJECT" get std/registry/registry-1.docker.io/password)" = "keepitsecret"
test "$(earthbuild secrets --org "$ORG" --project "$PROJECT" get std/registry/corp-registry.earthbuild.dev/username)" = "myprojecttest2"
test "$(earthbuild secrets --org "$ORG" --project "$PROJECT" get std/registry/corp-registry.earthbuild.dev/password)" = "keepitsecret2"

earthbuild registry --org "$ORG" --project "$PROJECT" remove
earthbuild registry --org "$ORG" --project "$PROJECT" list | grep -v registry-1.docker.io

clearprojectsecrets
