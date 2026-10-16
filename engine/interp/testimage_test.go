package interp_test

// testBaseImage is the image these tests build on.
//
// One name, because a base image gets bumped and the bump is what the tests are
// *for*: E133 measured what a move from alpine:3.21 to 3.22 does to the cache,
// and doing that again should not mean editing the literal in a dozen files and
// wondering which one was missed.
const testBaseImage = "alpine:3.22"
