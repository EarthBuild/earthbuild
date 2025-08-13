#!/bin/bash
# This test is designed to be run directly by github actions or on your host (i.e. not earthbuild-in-earthbuild)
set -uxe
set -o pipefail

cd "$(dirname "$0")"

earthbuild=${earthbuild-"../../../build/linux/amd64/earthbuild"}
echo "using earthbuild=$(realpath "$earthbuild")"

rm .testdata || true # cleanup

! "$earthbuild" $@ +test 2>&1 | tee .earthbuildoutput
! test -f .testdata

grep "TRY/FINALLY doesn't (currently) support CATCH statements" .earthbuildoutput
