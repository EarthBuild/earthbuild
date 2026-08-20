package fdpass_test

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fdpass"
)

// A terminal can be handed to the guest, and only on this machine.
//
// `RUN --interactive` needs a terminal attached to a running step. The guest's
// connection is a framed byte stream - `net.Pipe` in process, an OS pipe to a
// guest process - and bytes are all it can carry, so a terminal would have to be
// relayed: a pty in the guest, input frames in the protocol, and a second
// copy of every keystroke and every byte of output.
//
// A unix socket can carry the descriptor itself. The step then holds *the*
// terminal rather than a relay of it, which is the difference between a shell
// that works and a shell that mostly works: job control, window size, raw mode
// and `isatty` all come from the descriptor.
//
// **And a descriptor cannot cross a machine.** That is the whole of the
// restriction agreed for this construct - driver and workers on one host - and
// it is not a policy bolted onto the feature but the mechanism stated as one.
//
// Measured before anything is built on it.
func TestATerminalCanBeHandedOverAUnixSocket(t *testing.T) {
	t.Parallel()

	here, there, err := fdpass.SocketPair()
	if err != nil {
		t.Fatalf("no socketpair on this machine: %v", err)
	}

	t.Cleanup(func() { _ = here.Close(); _ = there.Close() })

	// A file with known contents stands in for the terminal: what matters is
	// that the *same open file* arrives, not a copy of its bytes.
	path := filepath.Join(t.TempDir(), "tty-stand-in")

	err = os.WriteFile(path, []byte("attached\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	// Read the first two bytes here, so the offset is not zero when it travels.
	head := make([]byte, 2)
	if _, err := f.Read(head); err != nil {
		t.Fatal(err)
	}

	go func() { _ = fdpass.SendFile(here, f) }()

	got, err := fdpass.RecvFile(there)
	if err != nil {
		t.Fatalf("the descriptor did not arrive: %v", err)
	}

	defer got.Close()

	b := make([]byte, 16)

	n, err := got.Read(b)
	if err != nil {
		t.Fatal(err)
	}

	// **The same open file, not a copy of it.** SCM_RIGHTS passes the open file
	// *description*, so the offset is shared: the other end continues where this
	// one stopped. A relay through a byte stream would deliver the file from the
	// beginning, and so would anything that re-opened the path.
	//
	// This is the property the whole restriction buys. A terminal that arrives
	// as a copy is not a terminal - `isatty` is false, there is no window size,
	// and job control has nothing to signal.
	if string(b[:n]) != "tached\n" {
		t.Errorf("the descriptor that arrived reads %q from the start, so it is a"+
			" copy rather than the same open file", b[:n])
	}
}

// And the framed connection cannot, which is why this needs its own channel.
func TestAPipeCannotCarryADescriptor(t *testing.T) {
	t.Parallel()

	a, b := net.Pipe()

	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })

	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	err = fdpass.SendFile(a, f)
	if err == nil {
		t.Fatal("a pipe accepted a descriptor, which would mean the transport" +
			" question this restriction rests on is not a question")
	}

	if !errors.Is(err, fdpass.ErrNoDescriptorChannel) {
		t.Errorf("the refusal does not say why: %v", err)
	}
}
