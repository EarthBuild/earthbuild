package cacheshare_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/cacheshare"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A pinned helper this machine lacks is fetched from the fleet.
//
// **The thing that makes a pin worth having.** E-F21 measured a worker that
// attempts to share and cannot: the module is in the driver's store, the
// worker's is empty, and nothing moved it. The pin names bytes, the fleet moves
// bytes by name, and this is the wire between them.
func TestAHelperThisMachineLacksComesFromTheFleet(t *testing.T) {
	t.Parallel()

	module := []byte("not a wasm module, but bytes with a name")
	id := ir.DigestOf(module)

	s := cacheshare.New(t.TempDir(), "", nil)
	s.Away(&away{has: map[ir.NodeID][]byte{id: module}})

	err := s.Offer(context.Background(),
		ir.Mount{ID: "k", Portable: true, HelperID: id.String()}, t.TempDir())
	if err == nil {
		t.Fatal("garbage compiled as a helper module")
	}

	if !strings.Contains(err.Error(), "compile helper") {
		t.Errorf("the module did not arrive from the fleet: %v", err)
	}
}

// And it is kept, so the next step does not fetch it again.
//
// A helper is asked for once per cache mount per step, and a fleet worker runs
// many. Re-fetching four megabytes each time would make the pin more expensive
// than the path it replaced.
func TestAFetchedHelperIsKept(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	module := []byte("fetched once")
	id := ir.DigestOf(module)

	src := &away{has: map[ir.NodeID][]byte{id: module}}

	s := cacheshare.New(root, "", nil)
	s.Away(src)

	m := ir.Mount{ID: "k", Portable: true, HelperID: id.String()}
	_ = s.Offer(context.Background(), m, t.TempDir())

	if src.asked != 1 {
		t.Fatalf("the fleet was asked %d times for the first fetch, want 1", src.asked)
	}

	st, err := blob.New(root)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.Get(id); err != nil {
		t.Errorf("a module fetched from the fleet was not kept: %v"+
			"\n  every step on this worker would fetch it again", err)
	}
}

// A fleet that answers with the wrong bytes is not believed.
//
// §5.3: what arrives from another trust domain is unauthenticated data until
// verified, and A5 is an assumption about this engine's scepticism rather than
// about a peer's good faith. A wrong answer is a miss, so the cache does not
// cross and the build is slower - never wrong.
func TestABadAnswerFromTheFleetIsAMiss(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := ir.DigestOf([]byte("what was asked for"))

	s := cacheshare.New(root, "", nil)
	s.Away(&away{has: map[ir.NodeID][]byte{id: []byte("something else")}})

	err := s.Offer(context.Background(),
		ir.Mount{ID: "k", Portable: true, HelperID: id.String()}, t.TempDir())
	if err == nil {
		t.Fatal("bytes that do not hash to their name were run as a helper")
	}

	if strings.Contains(err.Error(), "compile helper") {
		t.Error("a peer's wrong bytes were handed to the runtime")
	}

	st, err := blob.New(root)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.Get(id); err == nil {
		t.Error("wrong bytes were filed under the name they failed to hash to")
	}
}

// Without a fleet nothing changes, which is every local build.
func TestWithoutAFleetAMissIsAMiss(t *testing.T) {
	t.Parallel()

	s := cacheshare.New(t.TempDir(), "", nil)

	err := s.Offer(context.Background(),
		ir.Mount{ID: "k", Portable: true, HelperID: ir.DigestOf([]byte("gone")).String()},
		t.TempDir())
	if err == nil {
		t.Fatal("a helper nobody holds was run")
	}
}

// away is a fleet holding exactly what it is given, and counting.
type away struct {
	has   map[ir.NodeID][]byte
	asked int
}

func (a *away) Node(_ context.Context, id ir.NodeID) ([]byte, error) {
	a.asked++

	if b, ok := a.has[id]; ok {
		return b, nil
	}

	return nil, errors.New("not here")
}
