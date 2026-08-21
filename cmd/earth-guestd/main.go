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
	"time"

	"github.com/EarthBuild/earthbuild/engine/fdpass"
	"github.com/EarthBuild/earthbuild/engine/guest"
)

func main() {
	// The relay: a second process inside the sandbox whose stdio is the
	// fault-in channel. It carries bytes and understands none of them.
	if len(os.Args) > 1 && os.Args[1] == "--fills" {
		at := os.Getenv(guest.EnvFillSocket)
		if at == "" {
			fmt.Fprintf(os.Stderr, "earth-guestd --fills: %s is not set\n", guest.EnvFillSocket)
			os.Exit(1)
		}

		if err := guest.RelayFills(at, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "earth-guestd --fills: %v\n", err)
			os.Exit(1)
		}

		return
	}

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
		// A sandbox nobody is using stops itself. The host cannot be trusted to
		// do it: the host is what gets killed, and a VM whose reaper died is
		// exactly the VM that leaks (nits, 2026-08-21).
		Idle: guest.NewIdle(envDuration(guest.EnvIdle, guest.DefaultIdle)),
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

	// The fault-in channel over a socket, where the engine reaches this guest
	// through a VM and has no descriptor to pass. See guest.EnvFillSocket.
	//
	// Accepted in the background: a guest must serve steps whether or not
	// anything ever dials, and a host that starts its relay late is ordinary
	// rather than an error.
	if at := os.Getenv(guest.EnvFillSocket); at != "" {
		go func() {
			c, err := guest.ListenForFills(at)
			if err != nil {
				fmt.Fprintf(os.Stderr, "earth-guestd: no fault-in channel: %v"+
					"\n  steps will take whole layers\n", err)

				return
			}

			srv.SetFills(guest.NewFills(c))
		}()
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

	// Started before serving and never joined: it outlives every request by
	// design, and the only way it ends is by ending the process.
	go srv.Idle.Watch(func() {
		fmt.Fprintf(os.Stderr, "earth-guestd: nothing has used this sandbox for %v, stopping"+
			"\n  set %s to change that, or 0 to keep it up\n",
			envDuration(guest.EnvIdle, guest.DefaultIdle), guest.EnvIdle)
		os.Exit(0)
	})

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

// envDuration reads a duration from the environment, falling back when it is
// unset and **refusing when it will not parse**.
//
// Refused rather than defaulted: `EARTH_GUEST_IDLE=30` looks like thirty
// minutes and is not a duration, and silently using the default would leave an
// operator certain they had configured something. The one value that must not be
// guessed is the one somebody set deliberately.
func envDuration(name string, fallback time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}

	d, err := time.ParseDuration(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth-guestd: %s is %q, which is not a duration"+
			" (try 30m, 2h, 90s); using %v\n", name, v, fallback)

		return fallback
	}

	return d
}
