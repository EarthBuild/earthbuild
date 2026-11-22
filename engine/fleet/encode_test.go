package fleet_test

import (
	"bytes"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Equal values encode to equal bytes, every time.
//
// B.1's whole requirement. Without it, keys differ across implementations and
// the entire cache is per-implementation - and on the wire, two peers disagree
// about what a step *is*.
//
// Repeated, because the failure it guards is a Go map: iteration order is
// randomised per range, so an encoder that walked `Env` directly would produce
// different bytes on the second call within one process. Once would pass about
// half the time with two keys.
func TestEqualAssignmentsEncodeToEqualBytes(t *testing.T) {
	t.Parallel()

	made := func() fleet.Assignment {
		return fleet.Assignment{
			Version: fleet.Version,
			Base:    []ir.NodeID{{1}, {2}},
			Op: fleet.Op{
				Kind: fleet.KindExec, Args: []string{"cc", "-c", "main.c"},
				Env: map[string]string{
					"PATH": "/usr/bin", "CC": "gcc", "LANG": "C", "TZ": "UTC",
					"HOME": "/root", "SHELL": "/bin/sh",
				},
				Dir: "/src", User: "build",
			},
			Platform: "linux/amd64",
		}
	}

	want := fleet.Encode(made())

	for i := range 20 {
		if got := fleet.Encode(made()); !bytes.Equal(got, want) {
			t.Fatalf("round %d: two equal assignments encoded differently"+
				"\n  a map walked in iteration order, most likely", i)
		}
	}
}

// Fields cannot be confused with their neighbours.
//
// The encoding must be injective: two assignments that differ at all must encode
// differently. The cases here are the ones that a length prefix or an
// unconditional write exists to separate, and each would be a **false cache
// hit** if it collided (I3) - two distinct steps mapping to one key.
func TestDistinctAssignmentsEncodeDifferently(t *testing.T) {
	t.Parallel()

	base := fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"ab", "c"}},
	}

	for _, tc := range []struct {
		name string
		with fleet.Assignment
		why  string
	}{
		{
			name: "a split moved between arguments",
			with: fleet.Assignment{
				Version: fleet.Version,
				Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"a", "bc"}},
			},
			why: "length prefixes are what separate ⟨\"ab\",\"c\"⟩ from ⟨\"a\",\"bc\"⟩",
		},
		{
			name: "a directory that could be a user",
			with: fleet.Assignment{
				Version: fleet.Version,
				Op: fleet.Op{
					Kind: fleet.KindExec, Args: []string{"ab", "c"}, Dir: "x",
				},
			},
			why: "an absent field must not let the next one take its place",
		},
		{
			name: "a network flag",
			with: fleet.Assignment{
				Version: fleet.Version,
				Op: fleet.Op{
					Kind: fleet.KindExec, Args: []string{"ab", "c"},
					NoNetwork: true,
				},
			},
			why: "an isolated step is a different step",
		},
		{
			name: "an environment entry",
			with: fleet.Assignment{
				Version: fleet.Version,
				Op: fleet.Op{
					Kind: fleet.KindExec, Args: []string{"ab", "c"},
					Env: map[string]string{"A": "b"},
				},
			},
			why: "the environment is part of what a step is",
		},
		{
			name: "a different base",
			with: fleet.Assignment{
				Version: fleet.Version,
				Base:    []ir.NodeID{{9}},
				Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"ab", "c"}},
			},
			why: "the base is the filesystem the step runs on",
		},
	} {
		if bytes.Equal(fleet.Encode(base), fleet.Encode(tc.with)) {
			t.Errorf("%s encodes identically to the original: %s", tc.name, tc.why)
		}
	}
}

// The environment is written in ascending key order.
//
// Asserted on the bytes rather than on the sort: an encoder that sorted a copy
// and then wrote the map would pass a test of its sorting and fail on the wire.
func TestTheEnvironmentIsWrittenInOrder(t *testing.T) {
	t.Parallel()

	a := fleet.Assignment{
		Version: fleet.Version,
		Op: fleet.Op{
			Kind: fleet.KindExec,
			Env:  map[string]string{"ZED": "1", "ALPHA": "2", "MIKE": "3"},
		},
	}

	raw := fleet.Encode(a)

	alpha := bytes.Index(raw, []byte("ALPHA"))
	mike := bytes.Index(raw, []byte("MIKE"))
	zed := bytes.Index(raw, []byte("ZED"))

	if alpha < 0 || mike < 0 || zed < 0 {
		t.Fatalf("an environment key is missing from the encoding: %q", raw)
	}

	if alpha >= mike || mike >= zed {
		t.Errorf("the environment is not in ascending key order:"+
			" ALPHA at %d, MIKE at %d, ZED at %d", alpha, mike, zed)
	}
}

// A flag occupies its byte whether or not it is set.
//
// `Bool` writes even a false flag, and the reason given for it is that a field
// appearing only when set makes the field after it shift position. **That is not
// a collision here**, because every variable-width field in this encoding is
// length-prefixed, so a shifted field cannot be misread as its neighbour.
//
// The rule is inherited from `ir.Hasher`, where fields *are* adjacent bytes and
// it is load-bearing. It is kept because it costs one byte and removes a class
// of mistake from any field added later - and asserted here as what it actually
// is, a **width** property, rather than through a collision that cannot happen.
//
// The first version of this test claimed the collision and compared a false flag
// with a true one, which differ under the mutation as well as without it. It
// passed with the rule deleted, which is the class of test this work keeps
// finding: one whose subject is not what its name says.
func TestAFlagCostsItsByteEitherWay(t *testing.T) {
	t.Parallel()

	with := func(isolated bool) fleet.Assignment {
		return fleet.Assignment{
			Version: fleet.Version,
			Op: fleet.Op{
				Kind: fleet.KindExec, Args: []string{"x"}, NoNetwork: isolated,
			},
		}
	}

	off, on := fleet.Encode(with(false)), fleet.Encode(with(true))

	if len(off) != len(on) {
		t.Errorf("a false flag encodes to %d bytes and a true one to %d;"+
			" a field that appears only when set moves everything after it",
			len(off), len(on))
	}

	if bytes.Equal(off, on) {
		t.Error("the flag left no trace at all, so an isolated step and a" +
			" connected one are the same step")
	}
}
