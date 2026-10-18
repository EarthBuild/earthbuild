package guest

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

// EnvFillSocket is where a guest listens for its fault-in channel.
//
// **A second stream, because a fault travels the wrong way.** Every other
// message is the host asking the guest, and `container exec` gives exactly one
// stdio pair, which the main protocol holds. A sandbox that spawns its guest as
// a child can pass a second descriptor and does (`EARTH_GUEST_FILLS`); a sandbox
// that reaches its guest through a VM cannot, so the guest listens instead and
// the host dials in.
//
// Empty means no fault-in here, and a step gets its base whole - slower and
// correct, which is what a worker says out loud rather than assuming (E305).
const EnvFillSocket = "EARTH_GUEST_FILL_SOCKET"

// ListenForFills accepts one fault-in channel and returns it.
//
// One, because there is one host. A second dial would be a second party able to
// answer a fault, and answering wrongly is serving a step a file from somebody
// else's base - the failure E303 exists to prevent.
//
// The listener is closed as soon as it has its connection: nothing else is
// coming, and leaving it open is an endpoint inside a confined guest that
// nothing is watching.
func ListenForFills(at string) (io.ReadWriteCloser, error) {
	err := os.MkdirAll(filepath.Dir(at), 0o700)
	if err != nil {
		return nil, fmt.Errorf("prepare the fault-in socket: %w", err)
	}

	// **A unix socket path is not a path.** `sun_path` is 104 bytes on darwin and
	// 108 on Linux, and a longer one fails with `invalid argument` - which names
	// neither the limit nor the length, and sends a reader looking at
	// permissions. Checked here so the message says the thing.
	if len(at) >= sunPathMax {
		return nil, fmt.Errorf("the fault-in socket path is %d bytes and the limit is %d"+
			"\n  %s"+
			"\n  a unix socket lives in a fixed-size field, so put it somewhere short"+
			" like /run", len(at), sunPathMax-1, at)
	}

	// A stale socket from a guest that died is a file, and Listen refuses to
	// bind over one. Removing it is safe here for the reason it is not safe in
	// general: this guest is the only thing that ever binds this path, and it
	// is starting.
	_ = os.Remove(at)

	l, err := net.Listen("unix", at)
	if err != nil {
		return nil, fmt.Errorf("listen for fault-ins on %s: %w", at, err)
	}

	defer func() { _ = l.Close() }()

	c, err := l.Accept()
	if err != nil {
		return nil, fmt.Errorf("accept a fault-in channel: %w", err)
	}

	return c, nil
}

// RelayFills connects stdio to a guest's fault-in socket.
//
// Run as a second process *inside* the sandbox - `container exec -i <name>
// earth-guestd --fills`, which is how a host with only one stdio pair per exec
// gets a second one. It carries bytes and understands none of them: the protocol
// is between the host and the guest at either end of it.
func RelayFills(at string, in io.Reader, out io.Writer) error {
	c, err := dialFills(at)
	if err != nil {
		return err
	}

	defer func() { _ = c.Close() }()

	done := make(chan error, 2)

	go func() { _, err := io.Copy(c, in); done <- err }()
	go func() { _, err := io.Copy(out, c); done <- err }()

	// The first direction to end ends the relay: a half-open fault-in channel
	// is a step that will wait for an answer nobody is going to send.
	return <-done
}

// dialFills waits for the guest to be listening.
//
// **The relay can arrive first.** The host starts it as a second process in the
// sandbox, and nothing orders that against the guest binding its socket - so a
// single dial fails whenever the two land the wrong way round, which is often
// enough to look like a flake and rare enough to be blamed on something else.
//
// Bounded, because a guest that is never going to listen must not hold a relay
// open for ever: the sandbox reports that it cannot fault in, and a build takes
// whole layers, which is the outcome this whole path is an optimisation over.
func dialFills(at string) (net.Conn, error) {
	const (
		patience = 30 * time.Second
		every    = 20 * time.Millisecond
	)

	deadline := time.Now().Add(patience)

	for {
		c, err := net.Dial("unix", at)
		if err == nil {
			return c, nil
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no guest listening for fault-ins at %s after %v: %w"+
				"\n  the sandbox will take whole layers instead", at, patience, err)
		}

		time.Sleep(every)
	}
}

// sunPathMax is the size of `sockaddr_un.sun_path`.
//
// 104 on darwin, 108 on Linux. The smaller is used on both: a path that fits
// everywhere is one fewer thing that works on the machine it was written on and
// not on the machine it runs on.
const sunPathMax = 104

// SetFills gives this server a fault-in channel after it has started.
//
// After, because the channel arrives when the host dials rather than when the
// guest starts, and a guest that waited for one would not serve the steps of a
// build that never needs to fault anything.
func (s *Server) SetFills(f *Fills) {
	s.fillsMu.Lock()
	defer s.fillsMu.Unlock()

	s.Fills = f
}
