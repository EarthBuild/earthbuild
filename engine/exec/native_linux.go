package exec

import (
	"context"
	"fmt"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/EarthBuild/earthbuild/engine/fdpass"
	"github.com/EarthBuild/earthbuild/engine/guest"
)

// Native runs steps on this Linux machine, with no VM.
//
// The guest is a child process rather than a VM, so "boot" costs microseconds
// instead of the ~650ms Apple's backend needs. Confinement comes from the
// guest's own namespaces, chroot and cgroups rather than from a hypervisor.
//
// That makes it the second implementation of Sandbox, and the one that tests
// whether the port was honest. It differs from the first in a way a
// single-implementation interface would have hidden: **confinement here is
// conditional**. It requires CAP_SYS_ADMIN, so the same binary confines or does
// not depending on how it was invoked - whereas a VM always does.
type Native struct {
	// GuestBinary is earth-guestd. Built on demand when empty, which is not
	// possible everywhere, so EARTH_GUESTD overrides it.
	GuestBinary string
	// Root holds the layer store and scratch. A temporary directory when empty,
	// made by root() on first use and not moved afterwards.
	Root   string
	rootMu sync.Mutex

	// Fill fetches a path a step's base does not have, if this machine can.
	//
	// The handle says *which* base: a worker runs several steps at once, and a
	// filler that guessed would serve one step a file out of another's (E303).
	//
	// The last link of lazy transfer (E297). Set by whoever holds the peers - a
	// worker - and nil everywhere else, which means the guest gets no fault-in
	// channel, its tracer gets no filler, and a step runs against a base that is
	// all there. That is every build today.
	Fill func(handle, path string) error

	cmd     *osexec.Cmd
	boots   atomic.Int64
	mu      sync.Mutex
	stopped bool

	// terminals is this end of the descriptor channel, for an interactive step.
	terminals *net.UnixConn
	tmp       string
}

// NewNative returns a sandbox with the defaults.
func NewNative() *Native {
	return &Native{GuestBinary: os.Getenv("EARTH_GUESTD")}
}

// Boots reports how many guests were started.
func (n *Native) Boots() int { return int(n.boots.Load()) }

// StoreDir is where layers must be placed for this guest to see them.
func (n *Native) StoreDir() string {
	dir, err := n.root()
	if err != nil {
		// Nowhere to put one. Start reports the same failure with a diagnosis
		// attached; this is a query and has no way to.
		return ""
	}

	return dir
}

// root is where this sandbox keeps its layers, made on first use.
//
// Resolved here rather than in Start, because where the layers live is
// configuration and not a property of a running process. Every caller asks
// before anything boots - the L1 cache is opened against it, and the tests
// place a base layer in it - and a root that appeared only at Start answered
// "" until then. "" is not an error: it is the working directory, so
// `filepath.Join(store, "layers", id)` became a relative path that resolved,
// was created, and was written to. Two tests filled `engine/exec/layers/` in
// the source checkout and then failed with `/probe: no such file or directory`,
// a message about the guest's filesystem describing a mistake in the host's.
//
// `Apple.StoreDir` had already been given this exact treatment, with a comment
// explaining why. This backend was written afterwards against the same
// interface and did it the old way.
func (n *Native) root() (string, error) {
	n.rootMu.Lock()
	defer n.rootMu.Unlock()

	if n.Root != "" {
		return n.Root, nil
	}

	dir, err := os.MkdirTemp("", "earthbuild-native-")
	if err != nil {
		return "", fmt.Errorf("create the layer store: %w", err)
	}

	n.Root, n.tmp = dir, dir

	return dir, nil
}

// Confines is true whenever this backend runs at all.
//
// It was first written as a runtime property - confining as root, not otherwise
// - on the assumption that an unprivileged guest would run steps without
// isolation. It does not: overlayfs requires CAP_SYS_ADMIN (experiment E13), so
// without privilege the guest cannot assemble a layer stack and no step runs.
// There is therefore no state in which this backend works and does not confine,
// and Available refuses rather than returning a sandbox that would.
func (n *Native) Confines() bool { return true }

// Available reports whether this machine can run the backend, and why not when
// it cannot.
func (n *Native) Available() error {
	// Refused, not degraded. Materialisation is not optional - a step with no
	// layer stack has no filesystem - so this is I10 rather than I11, and the
	// message names the capability because "operation not permitted" from a
	// mount inside a guest tells a user nothing they can act on.
	// Not a euid check any more. Mounting overlayfs needs CAP_SYS_ADMIN *in the
	// namespace the mount happens in*, and a user namespace grants it there to
	// a process that has none outside - which is how every rootless container
	// runtime works and, measured on a 6.12 kernel, exactly what this needs:
	//
	//	unshare -Umr sh -c "mount -t overlay ... && rm m/a"
	//	MOUNTED
	//	c--------- 2 root root 0, 0 a
	//
	// An unprivileged user mounted an overlay and `rm` wrote a whiteout device
	// into it. So the refusal below is about whether a user namespace can be
	// made, not about who is asking.
	if os.Geteuid() != 0 && !userNamespacesAvailable() {
		return fmt.Errorf(
			"the native Linux backend needs CAP_SYS_ADMIN to mount overlayfs, and this process"+
				" has euid %d and cannot create a user namespace"+
				"\n  a user namespace would grant it - this machine refuses to make one"+
				"\n  check `sysctl kernel.unprivileged_userns_clone`, or run as root,"+
				" or use --engine=buildkit", os.Geteuid())
	}

	if n.GuestBinary != "" {
		_, err := os.Stat(n.GuestBinary)
		if err != nil {
			return fmt.Errorf("earth-guestd not found at %s: %w", n.GuestBinary, err)
		}

		return nil
	}

	_, err := findGuestBinary()
	if err != nil {
		return err
	}

	return nil
}

// Start launches the guest and returns its stdio as the protocol connection.
func (n *Native) Start(ctx context.Context) (Conn, error) {
	err := n.Available()
	if err != nil {
		return nil, err
	}

	bin, err := n.guestBinary()
	if err != nil {
		return nil, err
	}

	// Before the guest exists. cgroup v2 will not enable a controller for the
	// children of a cgroup that holds processes, and a delegated scope holds
	// this one - so a step's memory ceiling is refused until the engine steps
	// out of the way.
	//
	// **The host has to do it, not the guest.** The guest runs in a pid
	// namespace and pids written to `cgroup.procs` are read in the writer's pid
	// namespace, so a guest moving the host's pids is naming processes that do
	// not exist for it (E124).
	//
	// Best effort: a machine with no delegated cgroup has nothing to take over,
	// and the guest reports the resulting degradation with the reason.
	over, _ := guest.TakeOverCgroup()

	_, err = n.root()
	if err != nil {
		return nil, err
	}

	err = os.MkdirAll(filepath.Join(n.Root, "layers"), 0o750)
	if err != nil {
		return nil, fmt.Errorf("create the layer store: %w", err)
	}

	cmd := osexec.CommandContext(ctx, bin) //nolint:gosec // our own binary

	// Seeded before anything appends to it. The namespace block below adds one
	// variable and the store paths are added after, and an assignment between
	// them silently discarded the first - the guest then never waited for its
	// id mapping, ran as `nobody`, and failed to mount its own overlay.
	cmd.Env = append(os.Environ(),
		"EARTH_GUEST_ROOT="+n.Root,
		"EARTH_GUEST_SCRATCH="+filepath.Join(n.Root, "scratch"))

	// Where step cgroups go. Told, not inferred: the guest inherits the leaf
	// this process moved into and `/proc/self/cgroup` shows it that leaf, not
	// the scope above - so left to work it out the guest nests its step cgroups
	// under the host's own, where the host is a process and the controllers can
	// never be enabled (E124).
	if over != "" {
		cmd.Env = append(cmd.Env, guest.EnvCgroupParent+"="+over)
	}

	// Unprivileged: the guest runs in a user namespace where it is root, which
	// is where its mounts need the capability. Nothing is granted on the host -
	// files it writes are owned by the invoking user, exactly as they are
	// through the VM's shared store, so `--keep-own` refuses there for the same
	// measured reason (E84).
	//
	// Set here rather than by re-executing this process: the mount happens in
	// the guest and the guest is a child we already spawn, so the namespace can
	// be part of spawning it. Re-exec is what a runtime does when the process
	// that needs the namespace is itself.
	// gate releases the guest once its ids are mapped, when a delegated range
	// is being used. Nil otherwise, and the guest then never waits.
	var (
		gate     *os.File
		mapRange func(pid int) error
	)

	if os.Geteuid() != 0 {
		uids, gids, delegated := delegatedIDs()
		if delegated {
			// The whole delegated range, so a step can become another user.
			// `apt` drops to `_apt` to download and cannot when the namespace
			// holds one id - six of eleven corpus examples (E104).
			cmd.SysProcAttr = rangedNamespace()

			r, w, err := os.Pipe()
			if err != nil {
				return nil, fmt.Errorf("open the id-mapping gate: %w", err)
			}

			defer func() { _ = r.Close() }()

			// Fd 3 in the child, which blocks on it until the mapping is
			// written and then re-executes itself: capabilities are computed at
			// exec, and this image has none (E105).
			cmd.ExtraFiles = []*os.File{r}
			cmd.Env = append(cmd.Env, "EARTH_GUEST_ID_GATE=3")

			gate = w
			mapRange = func(pid int) error { return mapIDs(pid, uids, gids) }
		} else {
			// One id, which is all an unprivileged process may map on its own.
			// A step cannot become another user here, and `apt` says so.
			cmd.SysProcAttr = unprivilegedNamespace()
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("guest stdin: %w", err)
	}

	// A channel for descriptors, alongside the framed one.
	//
	// The request connection is this process's pipes to the guest and carries
	// bytes; a terminal is a descriptor and needs a unix socket (E189). Named by
	// environment rather than counted, because the id gate above takes fd 3 only
	// on the ranged path and a positional number would move underneath it.
	hostTerms, guestTerms, err := fdpass.SocketPair()
	if err != nil {
		return nil, fmt.Errorf("open the terminal channel: %w", err)
	}

	guestTermsFile, err := guestTerms.File()
	if err != nil {
		_ = hostTerms.Close()
		_ = guestTerms.Close()

		return nil, fmt.Errorf("terminal channel as a file: %w", err)
	}

	_ = guestTerms.Close()

	defer func() { _ = guestTermsFile.Close() }()

	cmd.ExtraFiles = append(cmd.ExtraFiles, guestTermsFile)
	cmd.Env = append(cmd.Env,
		fmt.Sprintf("EARTH_GUEST_TERMINALS=%d", 2+len(cmd.ExtraFiles)))

	// The fault-in channel, when this machine can answer one.
	//
	// The last link of lazy transfer (E297): the guest asks for a path its base
	// does not have, and whoever set Fill - a worker, over its peers - fetches
	// it. Absent, the guest gets no channel, its tracer gets no filler, and the
	// step runs against a base that is all there, which is every build today.
	if n.Fill != nil {
		hostFills, guestFills, fillErr := fdpass.SocketPair()
		if fillErr != nil {
			_ = hostTerms.Close()

			return nil, fmt.Errorf("open the fault-in channel: %w", fillErr)
		}

		guestFillsFile, fileErr := guestFills.File()
		if fileErr != nil {
			_ = hostTerms.Close()
			_ = hostFills.Close()
			_ = guestFills.Close()

			return nil, fmt.Errorf("fault-in channel as a file: %w", fileErr)
		}

		_ = guestFills.Close()

		defer func() { _ = guestFillsFile.Close() }()

		cmd.ExtraFiles = append(cmd.ExtraFiles, guestFillsFile)
		cmd.Env = append(cmd.Env,
			fmt.Sprintf("EARTH_GUEST_FILLS=%d", 2+len(cmd.ExtraFiles)))

		// Served for as long as the guest is there. It ends when the guest hangs
		// up, which is what closes the loop without anything having to be told.
		go func() {
			defer func() { _ = hostFills.Close() }()

			_ = guest.ServeFills(hostFills, n.Fill)
		}()
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = hostTerms.Close()

		return nil, fmt.Errorf("guest stdout: %w", err)
	}

	cmd.Stderr = os.Stderr

	err = cmd.Start()
	if err != nil {
		_ = hostTerms.Close()

		return nil, fmt.Errorf("start earth-guestd: %w", err)
	}

	n.terminals = hostTerms

	// Mapped while the child waits: the helper needs a pid, and the child is
	// `nobody` until this is written.
	if mapRange != nil {
		err = mapRange(cmd.Process.Pid)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = gate.Close()

			return nil, fmt.Errorf("map the guest's ids: %w", err)
		}

		// One byte, then it re-executes. Closing alone would do; a byte says the
		// mapping *succeeded* rather than that the parent gave up.
		_, err = gate.Write([]byte{1})
		_ = gate.Close()

		if err != nil {
			return nil, fmt.Errorf("release the guest: %w", err)
		}
	}

	n.cmd = cmd

	n.boots.Add(1)

	return &duplex{r: stdout, w: stdin}, nil
}

// Stop ends the guest and removes anything this sandbox created.
func (n *Native) Stop() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.stopped {
		return nil
	}

	n.stopped = true

	if n.cmd != nil && n.cmd.Process != nil {
		_ = n.cmd.Process.Kill()
		_ = n.cmd.Wait()
	}

	// Only what we made: a caller-supplied Root may hold a real cache, and
	// removing it would delete the thing the engine exists to accumulate.
	if n.tmp != "" {
		err := os.RemoveAll(n.tmp)
		if err != nil {
			return fmt.Errorf("remove the layer store: %w", err)
		}
	}

	return nil
}

func (n *Native) guestBinary() (string, error) {
	if n.GuestBinary != "" {
		return n.GuestBinary, nil
	}

	p, err := findGuestBinary()
	if err != nil {
		return "", err
	}

	n.GuestBinary = p

	return p, nil
}

// Terminals is the descriptor channel to this guest, for an interactive step.
//
// Read through the optional interface `engine/exec` checks after dialling, so a
// backend that cannot pass descriptors simply does not have the method.
func (n *Native) Terminals() *net.UnixConn { return n.terminals }

// SetFill gives this sandbox somewhere to fetch a path a step's base lacks.
//
// A method rather than a field so that a caller holding the `Sandbox` interface
// can ask - **and be told no**. Not every backend can fault in: the Apple one
// runs a VM whose filesystem this engine does not reach the same way, and a
// caller that set a field it could not see would think it had (E305).
func (n *Native) SetFill(f func(handle, path string) error) { n.Fill = f }
