package fleet

import (
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Whether a store can reproduce the bytes a layer it holds was named for.
//
// A diagnostic over a real store: it takes a store root and a layer id from the
// environment, packs that layer the way a fleet peer would be served it, and
// reports the digest. Equal is the invariant; unequal is a layer that can be
// verified and not reproduced.
func TestRepackOfARealLayer(t *testing.T) { //nolint:paralleltest // a real store
	root, want := os.Getenv("EARTH_PROBE_STORE"), os.Getenv("EARTH_PROBE_LAYER")
	if root == "" || want == "" {
		t.Skip("set EARTH_PROBE_STORE and EARTH_PROBE_LAYER")
	}

	var id ir.NodeID

	b, err := parseHex(want)
	if err != nil {
		t.Fatalf("%q: %v", want, err)
	}

	copy(id[:], b)

	l := &Layers{Root: root}
	if !l.Has(id) {
		t.Fatalf("%s holds no layer %s", root, want)
	}

	packed, err := l.Get(id)
	if err != nil {
		t.Fatalf("pack it: %v", err)
	}

	h := ir.NewHasher()
	h.Fixed(packed)

	got := h.Sum()

	t.Logf("asked for %s", id)
	t.Logf("packed to %s (%d bytes)", got, len(packed))

	if got != id {
		t.Fatalf("a peer asking for %s would be sent %s"+
			"\n  the store can check this layer and cannot reproduce it", id, got)
	}
}

func parseHex(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)

	for i := range out {
		var v int

		for j := range 2 {
			c := s[i*2+j]

			switch {
			case c >= '0' && c <= '9':
				v = v*16 + int(c-'0')
			case c >= 'a' && c <= 'f':
				v = v*16 + int(c-'a'+10)
			default:
				return nil, os.ErrInvalid
			}
		}

		out[i] = byte(v)
	}

	return out, nil
}
