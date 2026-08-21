package guest

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// twoWay is a pair of ends that can be written to and read from independently,
// standing in for the two sides of a relay.
//
// Built from two os.Pipes rather than net.Pipe: net.Pipe is synchronous and
// unbuffered, so a write blocks until somebody reads, and a relay copying in
// both directions at once deadlocks against its own test rather than against
// anything real.
type twoWay struct {
	io.Reader
	io.Writer
}

func relayPipes(t *testing.T) (host twoWay, relay twoWay) {
	t.Helper()

	hostToRelayR, hostToRelayW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	relayToHostR, relayToHostW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = hostToRelayR.Close()
		_ = hostToRelayW.Close()
		_ = relayToHostR.Close()
		_ = relayToHostW.Close()
	})

	return twoWay{Reader: relayToHostR, Writer: hostToRelayW},
		twoWay{Reader: hostToRelayR, Writer: relayToHostW}
}

// shortSocketPath is a socket path that fits in sun_path.
//
// t.TempDir() on darwin is already longer than the 104 bytes a unix socket
// address allows, so a test that used it would fail with `invalid argument` and
// look like a permissions problem.
func shortSocketPath(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "efs")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return filepath.Join(dir, "f.sock")
}
