package layer_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A socket is not a member of a layer.
//
// **A socket inode is a live process's address, and no process crosses a step
// boundary.** `connect` with nothing listening is ECONNREFUSED, so a captured
// socket can never be connected to by anything, ever - it is a zero-byte file
// of a type nothing can use.
//
// A FIFO is the opposite and stays: a named pipe with no reader or writer is
// fully functional, and a later step that opens it gets a working pipe. tar
// draws the same line - `TypeFifo` exists and there is no socket typeflag.
//
// Three further reasons, each of which bit before this:
//
//   - The guest cannot recreate one, and materialised it as a FIFO instead, so
//     capture -> materialise -> recapture was not a fixed point and a Φ-squash
//     (4.8) of a range holding a socket disagreed with the range it flattened.
//   - tar cannot carry one, so the same layer exported and re-read lost it -
//     one layer with two contents depending on the route.
//   - Whether one exists depends on whether some daemon ran during the step and
//     whether it unlinked on exit, which is cache-key noise for no function.
func TestASocketIsNotPartOfALayer(t *testing.T) {
	t.Parallel()

	withSocket := t.TempDir()
	if err := os.WriteFile(filepath.Join(withSocket, "real.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	l, err := net.Listen("unix", filepath.Join(withSocket, "daemon.sock"))
	if err != nil {
		t.Skip("cannot make a unix socket here:", err)
	}

	defer l.Close()

	without := t.TempDir()
	if err := os.WriteFile(filepath.Join(without, "real.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	with, err := layer.Take(withSocket)
	if err != nil {
		t.Fatal(err)
	}

	bare, err := layer.Take(without)
	if err != nil {
		t.Fatal(err)
	}

	if with.Content != bare.Content {
		t.Errorf("a tree with a socket digests to %v and the same tree without"+
			"\n  it to %v - so whether some daemon happened to leave one behind"+
			"\n  decides whether this step's cache entry is found",
			with.Content, bare.Content)
	}

	if with.Sockets != 1 {
		t.Errorf("%d sockets reported excluded, want 1: dropped silently, a"+
			"\n  build that depended on one has no way to find out", with.Sockets)
	}

	if bare.Sockets != 0 {
		t.Errorf("%d sockets reported for a tree with none", bare.Sockets)
	}
}

// A FIFO stays, because a FIFO still works.
func TestAFifoIsPartOfALayer(t *testing.T) {
	t.Parallel()

	withFifo := t.TempDir()
	if err := mkfifo(filepath.Join(withFifo, "pipe")); err != nil {
		t.Skip("cannot make a fifo here:", err)
	}

	bare := t.TempDir()

	with, err := layer.Take(withFifo)
	if err != nil {
		t.Fatal(err)
	}

	empty, err := layer.Take(bare)
	if err != nil {
		t.Fatal(err)
	}

	if with.Content == empty.Content {
		t.Error("a fifo made no difference to a layer's content, so a named" +
			"\n  pipe a later step opens is not recorded as being there")
	}
}
