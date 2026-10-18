//go:build linux

package exec

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestUserNetCloseDoesNotWaitOutItsTimeout is the two seconds every microVM
// build paid for its network.
//
// `Close` cancels the context, closes the socket and then waits for the read
// loop to return - bounded, so a stack that will not stop cannot hold a build
// open. The bound was the whole cost: the loop is blocked in a `read(2)` on a
// descriptor the runtime does not poll, closing an `os.File` cannot interrupt
// one of those, and so the wait ran to its end every single time. Measured
// against `+earthly` hot on x86: `sandbox:stop` 2.157s with the engine's own
// network and 0.152s without it.
//
// A socket pair rather than the packet socket this carries in earnest, because
// the defect is not about AF_PACKET: it is about a descriptor arriving in
// blocking mode, which is how one arrives over SCM_RIGHTS whatever it is. The
// pair is made blocking deliberately - that is the shape that used to hang.
func TestUserNetCloseDoesNotWaitOutItsTimeout(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("make a socket pair: %v", err)
	}

	// The far end stays open and silent, so the read loop blocks rather than
	// seeing an end-of-stream that would let it return for the wrong reason.
	defer func() { _ = unix.Close(fds[1]) }()

	u, err := startUserNet(os.NewFile(uintptr(fds[0]), "test"))
	if err != nil {
		t.Fatalf("start the stack: %v", err)
	}

	// Long enough for the loop to reach its read. Without this the test can
	// close before there is anything to interrupt, and pass for no reason.
	time.Sleep(100 * time.Millisecond)

	at := time.Now()
	u.Close()

	took := time.Since(at)
	if took > 500*time.Millisecond {
		t.Fatalf("Close took %v, so it waited out its bound rather than"+
			" interrupting the read", took)
	}
}
