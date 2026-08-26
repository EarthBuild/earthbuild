// Package guestd is the agent that runs inside a build sandbox.
//
// It is a package rather than a command so that the CLI can be it: `earth
// guestd ...` runs [Main]. A nested build copies one binary into a step and
// has nowhere beside it to put a second, so an agent that is a separate file
// is an agent a nested build cannot reach.
//
// It exists because of experiment E1b: Apple's `container exec` accepts no
// mount options, so a running VM cannot have a filesystem attached from
// outside. Layer assembly - overlay mounts, rootfs construction, per-step
// snapshots - therefore happens inside the guest, and this is what does it.
//
// It speaks the guest protocol over stdin and stdout. Nothing is written to
// stdout except protocol frames; diagnostics go to stderr, because a stray
// print would be read as a frame and desynchronise the connection.
package guestd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fdpass"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Command is the word that selects the agent when it is a subcommand.
//
// Named here rather than spelled at each call site, because the engine has to
// build the same invocation when it launches one.
const Command = "guestd"

// label is the command as the operator typed it.
//
// The agent is reachable two ways, and a message that always said
// `earth-guestd` sent somebody looking for a file a one-binary installation
// does not have. Read from os.Args rather than passed in, because Main is given
// the agent's own arguments and the invocation is not one of them.
func label() string {
	name := filepath.Base(os.Args[0])

	if len(os.Args) > 1 && os.Args[1] == Command {
		return name + " " + Command
	}

	return name
}

// Main runs the sandbox agent. args is what follows the command that selected
// it, so `earth-guestd --fills` and `earth guestd --fills` reach here alike.
//
// **One binary rather than two.** The agent used to ship as its own executable
// beside the CLI, which meant every place the CLI travels had to carry a second
// file - and the places it travels include the inside of a step, where a nested
// build runs a copy of the CLI that was copied in on its own. Those builds
// reported "cannot find earth-guestd" and there was nowhere sensible to put it.
//
// A subcommand goes wherever the CLI goes, which is the same trick the daemon
// shim and the test prober already use: re-execute this binary and tell it which
// half of itself to be.
func Main(args []string) {
	// The relay: a second process inside the sandbox whose stdio is the
	// fault-in channel. It carries bytes and understands none of them.
	if len(args) > 0 && args[0] == "--fills" {
		at := os.Getenv(guest.EnvFillSocket)
		if at == "" {
			fmt.Fprintf(os.Stderr, "%s --fills: %s is not set\n", label(), guest.EnvFillSocket)
			os.Exit(1)
		}

		err := guest.RelayFills(at, os.Stdin, os.Stdout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s --fills: %v\n", label(), err)
			os.Exit(1)
		}

		return
	}

	// Packing one layer of the store onto stdout, for a host that cannot open
	// the store itself. One layer per invocation and nothing on stdout but the
	// blob, so the caller is a pipe rather than a protocol (E556).
	if len(args) > 1 && args[0] == "--pack" {
		root := os.Getenv("EARTH_GUEST_ROOT")
		if root == "" {
			root = "/var/lib/earthbuild"
		}

		id, err := ir.ParseNodeID(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s --pack: %v\n", label(), err)
			os.Exit(1)
		}

		err = guest.PackLayer(root, id, os.Stdout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s --pack: %v\n", label(), err)
			os.Exit(1)
		}

		return
	}

	// First of all, and it does not return when it applies: this binary is also
	// the shim that a step's own docker daemon is launched through, because
	// `dockerd` needs a user namespace it is root in and a writable `/run`, and
	// Go cannot run code between clone and exec (E373).
	guest.RunDaemonShimIfAsked()
	guest.RunStepShimIfAsked()

	// Before anything else, and it may not return: a guest spawned into an
	// unmapped user namespace waits here for its ids and then re-executes
	// itself, because capabilities are computed at exec and this image has none
	// (E105).
	err := guest.WaitForIDs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", label(), err)
		os.Exit(1)
	}

	// After the ids are settled, because mounting needs the capabilities that
	// arrive with them, and before anything is served.
	procForTracing()

	err = run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", label(), err)
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

	// Off Linux newMaterialiser always fails - see mat_other.go, which refuses
	// rather than layering without overlayfs - so on that build this branch is
	// always taken. On Linux, the build that matters, it is a real check.
	mat, releaseScratch, err := newMaterialiser(root, scratch)
	if err != nil {
		return err
	}

	// The scratch may be a tmpfs this process mounted, and a mount outlives the
	// process that made it unless somebody unmounts it.
	defer releaseScratch()

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
			// Named for what it is rather than `err`, which shadows the outer
			// one this goroutine closes over and makes the two impossible to
			// tell apart in a diff (govet shadow).
			c, listenErr := guest.ListenForFills(at)
			if listenErr != nil {
				fmt.Fprintf(os.Stderr, "%s: no fault-in channel: %v"+
					"\n  steps will take whole layers\n", label(), listenErr)

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
		fmt.Fprintf(os.Stderr, "%s: nothing has used this sandbox for %v, stopping"+
			"\n  set %s to change that, or 0 to keep it up\n",
			label(), envDuration(guest.EnvIdle, guest.DefaultIdle), guest.EnvIdle)

		// **Stopping the agent is not stopping the sandbox.** In a VM the
		// machine is held open by a keep-alive at PID 1, so exiting here left a
		// running VM with a `sleep` in it and its memory reserved until that
		// sleep ended a day later - twenty-six of them on one machine (E555).
		//
		// Reported and not fatal: the exit below is what this function is for,
		// and a machine that will not stop is the state the engine was already
		// in.
		stopErr := guest.StopMachine()
		if stopErr != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", label(), stopErr)
		}

		os.Exit(0)
	})

	err = srv.Serve(context.Background(), stdio{})
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}

	if reason := srv.Degraded(); reason != "" {
		fmt.Fprintf(os.Stderr, "%s: resource limits not applied: %s\n", label(), reason)
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
		fmt.Fprintf(os.Stderr, "%s: %s is %q, which is not a duration"+
			" (try 30m, 2h, 90s); using %v\n", label(), name, v, fallback)

		return fallback
	}

	return d
}
