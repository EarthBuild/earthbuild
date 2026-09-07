package fleet_test

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"testing/quick"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// What Encode writes, Decode reads.
//
// There is no schema between them - the decoder mirrors the encoder field for
// field, in order, and nothing but this test keeps the two in step. A field
// added to one and not the other shifts everything after it, which is the same
// failure a missing length prefix causes and just as quiet.
//
// Randomised, because a hand-written case tests the fields somebody thought of.
// `testing/quick` generates the ones they did not.
func TestWhatIsWrittenCanBeRead(t *testing.T) {
	t.Parallel()

	roundTrips := func(a fleet.Assignment) bool {
		got, err := fleet.Decode(fleet.Encode(a))
		if err != nil {
			t.Logf("decoding failed: %v (%+v)", err, a)

			return false
		}

		return same(a, got)
	}

	err := quick.Check(roundTrips, &quick.Config{MaxCount: 500})
	if err != nil {
		t.Error(err)
	}
}

// same compares two assignments as the wire preserves them.
//
// An empty slice and a nil one encode identically - a count of zero - so they
// must compare equal here, or the property fails on a distinction the format
// deliberately does not carry.
func same(a, b fleet.Assignment) bool {
	if a.Version != b.Version || a.Platform != b.Platform ||
		a.DeadlineUnix != b.DeadlineUnix {
		return false
	}

	if !slices.Equal(a.Base, b.Base) || len(a.Sources) != len(b.Sources) {
		return false
	}

	for i := range a.Sources {
		if !slices.Equal(a.Sources[i], b.Sources[i]) {
			return false
		}
	}

	if a.Op.Kind != b.Op.Kind || a.Op.Dir != b.Op.Dir ||
		a.Op.User != b.Op.User || a.Op.NoNetwork != b.Op.NoNetwork {
		return false
	}

	if !slices.Equal(a.Op.Args, b.Op.Args) || !maps.Equal(a.Op.Env, b.Op.Env) {
		return false
	}

	return slices.Equal(a.Hints.Images, b.Hints.Images) &&
		slices.Equal(a.Hints.ReadsPredicted, b.Hints.ReadsPredicted) &&
		a.Hints.EstimatedSeconds == b.Hints.EstimatedSeconds
}

// Rubbish from a peer is refused, and never panics.
//
// These bytes come from somebody else, so every way they can be wrong is a
// **case** rather than an accident. A decoder that panicked on a bad length
// would let any peer stop the driver by sending four bytes - which is not a
// crash bug, it is a denial of service with a one-line exploit.
func TestMalformedBytesAreRefusedRatherThanFatal(t *testing.T) {
	t.Parallel()

	good := fleet.Encode(fleet.Assignment{
		Version: fleet.Version,
		Base:    []ir.NodeID{{1}},
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
	})

	cases := map[string][]byte{
		"nothing":              {},
		"a lone count":         {0, 0, 0, 1},
		"truncated":            good[:len(good)/2],
		"one byte short":       good[:len(good)-1],
		"trailing rubbish":     append(slices.Clone(good), 'x'),
		"a count of 4 billion": {0xff, 0xff, 0xff, 0xff},
	}

	for name, b := range cases {
		// The panic is the failure being tested for, so it is caught rather
		// than allowed to take the suite with it.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: the decoder panicked (%v); a peer can stop"+
						" the driver by sending it", name, r)
				}
			}()

			_, err := fleet.Decode(b)
			if !errors.Is(err, fleet.ErrMalformed) {
				t.Errorf("%s: %v, want ErrMalformed", name, err)
			}
		}()
	}
}

// A count from a peer is refused for being a count, not for running out of bytes.
//
// Two mechanisms refuse a four-billion count and only one of them is the point.
// Every slice is allocated `min(n, 64)` and every element read can fail, so a
// wild count is *also* caught by simply running out of input - which means an
// assertion that "it was refused" passes with the stated bound deleted. It did
// (E245).
//
// So the assertion is on **which** refusal. `maxCount` is a limit this engine
// declares; truncation is an accident of how much the peer happened to send. A
// peer that sent a wild count *and* enough bytes to satisfy it would meet only
// the first, and that is the case the bound exists for.
func TestAPeersCountIsRefusedByTheStatedBound(t *testing.T) {
	t.Parallel()

	// A well-formed version, then a base count of nearly four billion.
	b := []byte{0, 0, 0, 0, 0, 0, 0, 1, 0xff, 0xff, 0xff, 0xf0}

	_, err := fleet.Decode(b)
	if !errors.Is(err, fleet.ErrMalformed) {
		t.Fatalf("a four-billion count gave %v, want ErrMalformed", err)
	}

	if !strings.Contains(err.Error(), "will allocate for a peer") {
		t.Errorf("it was refused for running out of bytes, not for the count:"+
			"\n  %v"+
			"\n  a peer that also sent enough bytes would get past this", err)
	}
}
