package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// A cache entry from outside the trust domain is data, not a result.
//
// §5.3 and A5. Λ has exactly two outcomes and an entry somebody else wrote is a
// **miss**: the step is rebuilt, which costs time, and the alternative is
// believing a result produced by a machine this one has no reason to trust.
//
// It matters more the moment a fleet exists. Until then every entry in the cache
// was written here; from S6 an entry can arrive from a worker, and the whole of
// A5 is that the driver believes digests it verifies and nothing else.
//
// **This mechanism had no test.** A sweep that deleted the check and ran the
// suite found it green - one of two survivors out of seven invariant-bearing
// mutations (E241), and the only one that was a real gap rather than a mutation
// the platform had compiled away.
func TestAnEntryFromAnUntrustedWriterIsAMiss(t *testing.T) {
	t.Parallel()

	const (
		mine   = "this-engine"
		theirs = "somebody-else"
	)

	layer := digest(3)
	key := core.Key{9}

	for _, tc := range []struct {
		name    string
		writer  string
		trusted map[string]bool
		wantHit bool
	}{
		{
			name: "written here, trusted", writer: mine,
			trusted: map[string]bool{mine: true}, wantHit: true,
		},
		{
			name: "written elsewhere, not trusted", writer: theirs,
			trusted: map[string]bool{mine: true}, wantHit: false,
		},
		{
			// nil is "no trust domain configured", which is the single-machine
			// case: every entry in the cache was written by this engine, and
			// requiring a list would make a local build refuse its own results.
			name: "no trust domain, written elsewhere", writer: theirs,
			trusted: nil, wantHit: true,
		},
		{
			// An empty map is not nil, and means the opposite: a domain was
			// configured and nobody is in it. The distinction is the same one
			// the fleet's allowlist makes, and for the same reason - a caller
			// that built an empty set must not be read as having built none.
			name: "an empty trust domain", writer: mine,
			trusted: map[string]bool{}, wantHit: false,
		},
	} {
		cache := newMemCache()
		cache.Put(key, core.Entry{Layer: layer, Writer: tc.writer})

		_, hit := core.Lookup(cache, allBlobs{}, tc.trusted, key)

		if hit != tc.wantHit {
			t.Errorf("%s: hit=%v, want %v"+
				"\n  an entry from outside the trust domain is data, not a"+
				" result (§5.3, A5)", tc.name, hit, tc.wantHit)
		}
	}
}
