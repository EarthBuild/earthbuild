// Command earth-guestd is the agent that runs inside a build sandbox.
//
// It exists because of experiment E1b: Apple's `container exec` accepts no
// mount options, so a running VM cannot have a filesystem attached from
// outside. Layer assembly - overlay mounts, rootfs construction, per-step
// snapshots - therefore happens inside the guest, and this is what does it.
//
// It speaks the guest protocol over stdin and stdout. Nothing is written to
// stdout except protocol frames; diagnostics go to stderr, because a stray
// print would be read as a frame and desynchronise the connection.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/EarthBuild/earthbuild/engine/fdpass"
	"github.com/EarthBuild/earthbuild/engine/guest"
)

func main() {
	// First of all, and it does not return when it applies: this binary is also
	// the shim that a step's own docker daemon is launched through, because
	// `dockerd` needs a user namespace it is root in and a writable `/run`, and
	// Go cannot run code between clone and exec (E373).
	guest.RunDaemonShimIfAsked()

	// Before anything else, and it may not return: a guest spawned into an
	// unmapped user namespace waits here for its ids and then re-executes
	// itself, because capabilities are computed at exec and this image has none
	// (E105).
	err := guest.WaitForIDs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth-guestd: %v\n", err)
		os.Exit(1)
	}

	// After the ids are settled, because mounting needs the capabilities that
	// arrive with them, and before anything is served.
	procForTracing()

	err = run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth-guestd: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root := os.Getenv("EARTH_GUEST_ROOT")
	if root == "" {
		root = "/var/lib/earthbuild"
	}

	scratch := os.Getenv("EARTH_GUEST_SCRATCH")
	if scratch == "" {
		scratch = "/var/lib/earthbuild/scratch"
	}

	mat, err := newMaterialiser(root, scratch)
	if err != nil {
		return err
	}

	srv := &guest.Server{
		Mat:      mat,
		LayerDir: root,
		// Confinement is the guest's job and it is not optional: a step that
		// escapes invalidates every cache claim the engine makes (green paper
		// A3). There is deliberately no flag to turn this off.
		Limits: guest.Limits{
			MemoryMax: envBytes("EARTH_GUEST_MEMORY_MAX"),
			PidsMax:   envBytes("EARTH_GUEST_PIDS_MAX"),
		},
	}

	// The descriptor channel, where the engine gave us one.
	//
	// Named by environment rather than counted: the id gate takes fd 3 only on
	// the ranged path, so a fixed number would move underneath it. Absent means
	// no interactive step can run here, which the server says by name.
	if fd := os.Getenv("EARTH_GUEST_TERMINALS"); fd != "" {
		n, convErr := strconv.Atoi(fd)
		if convErr != nil {
			return fmt.Errorf("EARTH_GUEST_TERMINALS is %q, which is not a descriptor: %w", fd, convErr)
		}

		terms, connErr := fdpass.ConnFromFD(n)
		if connErr != nil {
			return fmt.Errorf("the terminal channel on fd %d: %w", n, connErr)
		}

		defer func() { _ = terms.Close() }()

		srv.Terminals = terms
	}

	// The fault-in channel, where the engine gave us one.
	//
	// Named by environment for the same reason the terminal channel is: a fixed
	// number would move underneath the id gate. Absent means nothing lazily
	// materialises here, which is every build today (E296).
	if fd := os.Getenv("EARTH_GUEST_FILLS"); fd != "" {
		n, convErr := strconv.Atoi(fd)
		if convErr != nil {
			return fmt.Errorf("EARTH_GUEST_FILLS is %q, which is not a descriptor: %w", fd, convErr)
		}

		fills, connErr := fdpass.ConnFromFD(n)
		if connErr != nil {
			return fmt.Errorf("the fault-in channel on fd %d: %w", n, connErr)
		}

		defer func() { _ = fills.Close() }()

		srv.Fills = guest.NewFills(fills)
	}

	err = srv.Serve(context.Background(), stdio{})
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}

	if reason := srv.Degraded(); reason != "" {
		fmt.Fprintf(os.Stderr, "earth-guestd: resource limits not applied: %s\n", reason)
	}

	return nil
}

// stdio joins stdin and stdout into one duplex stream.
type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }   //nolint:wrapcheck // io passthrough
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) } //nolint:wrapcheck // io passthrough

func envBytes(name string) int64 {
	var n int64

	_, err := fmt.Sscanf(os.Getenv(name), "%d", &n)
	if err != nil {
		return 0
	}

	return n
}
