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
	"github.com/EarthBuild/earthbuild/engine/store"
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
	// And the shim that holds a step's network namespace open while the agent
	// puts an interface in it. A child rather than a thread of this process,
	// because a thread that enters a namespace does not reliably come back -
	// see RunStepNetShimIfAsked.
	guest.RunStepNetShimIfAsked()

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

	// **Collected beside the server rather than before it.** The host waits
	// thirty seconds for a handshake, and a collection worth doing outlasts
	// that - so collecting first meant the guest was killed mid-tidy and the
	// store never got smaller. The server answers Hello immediately and holds
	// every other request until this closes, which is honest: the store those
	// requests would use is not ready yet.
	fmt.Fprintf(os.Stderr, "%s: starting, collecting the store\n", label())

	ready := make(chan struct{})

	go func() {
		defer close(ready)

		reclaim(root)
	}()

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

	fmt.Fprintf(os.Stderr, "%s: serving\n", label())

	// One reading a second, which costs 2.2us and is ample resolution for a
	// figure describing minutes. Stopped with the agent.
	stopWatch := make(chan struct{})
	defer close(stopWatch)

	srv := &guest.Server{
		Ready:    ready,
		Pressure: store.Watch(root, stopWatch),
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

	// Started here rather than at the top of Main: the modes above are one-shot
	// helpers that exit, and a profile of one of those is a profile of a process
	// that did nothing the ceiling is about.
	writeProfiles := profiling()

	err = srv.Serve(context.Background(), stdio{})

	writeProfiles()

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

// EnvStoreFree is how much room the store should have before a build starts.
//
// **Because nothing collected it and a device is a fixed size.** The store grew
// without limit - a cache with a collector nothing called - and on a host
// directory that is untidy while on a guest's own device it stops the build:
// five suite runs in one afternoon ended with `no space left on device` partway
// through a capture, each time after twenty minutes of work.
//
// Accepts the sizes `earth prune` does: `20G`, `500M`. Zero or unset is the
// default below; `0` explicitly is off, for a machine that would rather run out
// than lose a layer.
const EnvStoreFree = "EARTH_STORE_FREE"

// defaultStoreFree is what a build is left before it starts.
//
// Enough to unpack a large image and capture what a step wrote, which is the
// unit of work that fails when it runs out. Smaller would collect more often
// and still stop mid-build; larger throws away layers nobody asked it to.
const defaultStoreFree = 8 << 30

// reclaim makes room in the store, before anything reads it.
//
// **Here because this is the one moment nothing is running.** There is no lock
// on the store, and a build that read a layer this removed would materialise a
// filesystem missing an element - so the collection happens as the agent comes
// up and not while it serves. Both backends pass through here, which is what
// makes this the engine's answer rather than the microVM's.
//
// Best-effort and loud: a store that cannot be measured or collected is a build
// that may run out of room, which is slower and not wrong - so it says so and
// carries on.
// collectBudget is how long the agent may collect before it starts serving.
//
// **The host is waiting on a handshake while this runs.** Collection here is
// housekeeping nobody asked for, and it was unbounded: on a store of 44,015
// layers with 5G free it outlasted the host's thirty-second budget, so every
// sandbox in a build failed with "the guest did not answer the handshake" -
// describing a guest that had booted, accepted the connection, and was busy.
//
// Well under that thirty seconds, because the boot has its own costs and the
// handshake budget covers all of them. A collection that does not finish leaves
// the rest for the next build; the store converges over boots, and a build that
// genuinely runs out of room says so in words that name the problem.
const defaultCollectBudget = 5 * time.Second

// EnvCollectBudget overrides that budget, and zero removes it.
//
// **The only way to collect a device-backed store.** `earth prune` collects the
// host's store directory; a microVM's store is a fixed-size image the host has
// never opened, so the command that would normally do this cannot reach it. The
// agent's own collection is budgeted so housekeeping never blocks a handshake,
// and a busy build writes more than five seconds of collecting frees - one
// session took a store from 21G free to 5G while collecting on every sandbox
// start. Without this the only remedy left is remaking the image, which
// discards every layer in it.
//
// Raising it means a build may wait: a guest that spends ten minutes collecting
// answers nothing for ten minutes, and the host gives up long before that. It
// is for a maintenance run - one build, told to tidy up - and not for a
// setting anybody leaves on.
const EnvCollectBudget = "EARTH_COLLECT_BUDGET"

// budgetFrom reads the budget from a setting's value.
//
// Zero is meaningful and is not "unset": it asks for an unbudgeted collection,
// which is what an operator tidying a store wants and what prune does on a
// store the host can reach. Unset and unparseable both give the default -
// a typo should choose neither "never collect" nor "block forever".
func budgetFrom(v string) time.Duration {
	if v == "" {
		return defaultCollectBudget
	}

	d, err := time.ParseDuration(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s is %q, which is not a duration"+
			" (try 30s, 10m); using %v\n", label(), EnvCollectBudget, v, defaultCollectBudget)

		return defaultCollectBudget
	}

	return d
}

func reclaim(root string) {
	want := uint64(defaultStoreFree)

	if v := os.Getenv(EnvStoreFree); v != "" {
		n, err := store.ParseSize(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s is %q, which is not a size: %v\n",
				label(), EnvStoreFree, v, err)

			return
		}

		want = n
	}

	budget := budgetFrom(os.Getenv(EnvCollectBudget))

	report, err := store.ReclaimWithin(root, want, nil, budget)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: the store could not be collected, so this build"+
			" may run out of room: %v\n", label(), err)

		return
	}

	if report.Removed > 0 {
		fmt.Fprintf(os.Stderr, "%s: %s\n", label(), report)
	}

	if report.Stopped {
		fmt.Fprintf(os.Stderr, "%s: the store still has less than %dG free after %s of"+
			" collecting, and the rest is left for the next build\n"+
			"  a build may yet run out of room\n%s",
			label(), want>>30, budget, adviceFor(root))
	}
}
