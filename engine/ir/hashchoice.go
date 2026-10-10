package ir

import (
	"crypto/sha256"
	"fmt"
	"hash"
	"os"
	"strings"
	"sync/atomic"

	"lukechampine.com/blake3"
)

// HashFunc is which function ℋ is for this store.
//
// **One per store, chosen when it is made, never negotiated at runtime.** Green
// paper §3.1 fixes ℋ as BLAKE3-256 and says it is not configurable; this is the
// one exception and it exists for a reason the specification did not anticipate.
// Buck2 sends SHA-256 to a remote execution service and declines to make that
// configurable, so a store that is to be read by one has to be built in
// SHA-256. Bazel accepts BLAKE3 (`DigestFunction` 9) and needs no such thing.
//
// Safe because the two never meet. A key derived under one function is not a key
// under the other, so a store holding both generations yields a miss rather than
// a wrong answer (I3) - and collection removes whichever stops being used. There
// is nothing to stamp and nothing to migrate.
type HashFunc int32

const (
	// HashBLAKE3 is the default and what §3.1 names.
	HashBLAKE3 HashFunc = iota
	// HashSHA256 is what a Buck2-flavoured remote execution service speaks.
	HashSHA256
)

func (f HashFunc) String() string {
	if f == HashSHA256 {
		return "SHA-256"
	}

	return "BLAKE3-256"
}

// chosen is read on every hash and written at most once, before anything is
// hashed. An atomic rather than a plain variable because "written once at
// startup" is a claim about callers, and a data race is not the way to find out
// they were wrong.
var chosen atomic.Int32

// Hash is which function ℋ currently is.
func Hash() HashFunc { return HashFunc(chosen.Load()) }

// SelectHash sets ℋ for this process.
//
// Must be called before anything is hashed. Nothing enforces that at runtime -
// a check on every hash would cost more than it could ever save - so the
// discipline is that a store's function is settled where the store is opened,
// once, before any work.
func SelectHash(f HashFunc) { chosen.Store(int32(f)) }

// newHash is ℋ as a streaming hash.
func newHash() hash.Hash {
	if Hash() == HashSHA256 {
		return sha256.New()
	}

	return blake3.New(HashSize, nil)
}

// sumOf is ℋ over a byte string.
func sumOf(b []byte) NodeID {
	if Hash() == HashSHA256 {
		return NodeID(sha256.Sum256(b))
	}

	return NodeID(blake3.Sum256(b))
}

// SelectHashForTest sets ℋ for one test and hands back how to put it back.
//
// In a normal file rather than an `_test.go` one because tests outside this
// package need it, and named so a reader knows what it is for. A test that uses
// it cannot be parallel: the choice is process-wide, and what it races against
// is every other test that hashes anything.
func SelectHashForTest(t interface{ Helper() }, f HashFunc) func() {
	t.Helper()

	was := Hash()
	SelectHash(f)

	return func() { SelectHash(was) }
}

// assertHashWidth fails loudly if a function is added whose output is not the
// width every digest in the engine is (§3.1).
func assertHashWidth() {
	for _, f := range []HashFunc{HashBLAKE3, HashSHA256} {
		was := Hash()
		SelectHash(f)

		if n := len(sumOf(nil)); n != HashSize {
			SelectHash(was)
			panic(fmt.Sprintf("ℋ as %v is %d bytes, and every digest in the engine is %d", f, n, HashSize))
		}

		SelectHash(was)
	}
}

func init() {
	assertHashWidth()
	selectFromEnv()
}

// EnvDigest names the digest function a store is built with.
//
// Read in this package's init, which is the only place that cannot be
// forgotten: ℋ has to be settled before anything is hashed, the engine is three
// binaries, and a call at the top of each `main` is three chances to omit one.
// Package initialisation runs before every `main`, and every binary links this
// package because every binary hashes.
const EnvDigest = "EARTH_DIGEST"

// HashFromEnv reads a digest function from what the variable was set to.
//
// **An unrecognised value is refused, not defaulted.** Someone who writes
// `sha-1` and silently gets BLAKE3 has a store no remote execution service will
// read, a build that simply stops getting hits, and nothing anywhere saying
// why. Empty is the only thing that means "the default".
func HashFromEnv(v string) (HashFunc, error) {
	switch normaliseHashName(v) {
	case "":
		return HashBLAKE3, nil
	case "blake3", "blake3256":
		return HashBLAKE3, nil
	case "sha256":
		return HashSHA256, nil
	default:
		return HashBLAKE3, fmt.Errorf(
			"%s is set to %q, which is not a digest function this engine has"+
				"\n  it is one of: blake3 (the default), sha256"+
				"\n  sha256 is for a store a Buck2 remote execution service will read;"+
				" Bazel accepts blake3 and needs no setting",
			EnvDigest, v)
	}
}

// normaliseHashName strips the punctuation people write digest names with, so
// `SHA-256`, `sha_256` and `sha256` are one answer.
func normaliseHashName(v string) string {
	var out []rune

	for _, r := range strings.ToLower(v) {
		if r == '-' || r == '_' || r == ' ' {
			continue
		}

		out = append(out, r)
	}

	return string(out)
}

// selectFromEnv settles ℋ, or refuses to start.
//
// A panic, which is what an unusable configuration deserves at initialisation:
// there is no build yet to fail, no diagnostic channel open, and continuing
// would mean building a store the author did not ask for.
func selectFromEnv() {
	f, err := HashFromEnv(os.Getenv(EnvDigest))
	if err != nil {
		panic(err.Error())
	}

	SelectHash(f)
}
