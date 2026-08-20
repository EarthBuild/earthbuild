package exec

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"lukechampine.com/blake3"
)

// Apple runs steps inside a macOS VM via Apple's `container` CLI.
//
// Measured on this machine, matching experiment E1b: ~650ms to boot a VM,
// ~60ms to exec inside a running one. That ratio is the entire argument for the
// Sandbox port - one VM per run, not per step.
//
// The guest binary is bind-mounted rather than baked into an image, so a
// developer's rebuild of earth-guestd takes effect immediately instead of
// requiring an image publish.
type Apple struct {
	// Image is the sandbox's root filesystem.
	Image string
	// GuestBinary is a linux binary of earth-guestd for the VM's architecture.
	// Built on demand when empty.
	GuestBinary string

	// Command is what the VM runs to stay alive. Empty means the image's own
	// entrypoint, which is how a sandbox with a daemon in it starts one: the
	// dind image's entrypoint *is* dockerd, so overriding the command with a
	// sleep produced a VM with a docker client, a socket path and nothing
	// listening on it - and a build that waited ninety seconds for a daemon
	// that was never going to arrive.
	Command []string

	// Store is the host directory bind-mounted as the guest's layer store. A
	// step's filesystem is its layer stack and nothing else - not the sandbox
	// image - so layers must reach the guest, and this is the path they take.
	Store string

	// Memory is how much the VM gets, in `container run -m` form.
	//
	// Set explicitly because the default is 1 GiB, and a build VM at 1 GiB is
	// not a build VM: a real `npm install` fills the guest's page cache with
	// writes over virtiofs, and the next allocation - typically a mkdir while
	// copying the result into the layer store - fails with ENOMEM. That reads
	// like a disk fault and is not one, which is why it went unexplained for so
	// long. See E25.
	Memory string

	name    string
	dir     string
	cmd     *osexec.Cmd
	boots   atomic.Int64
	mu      sync.Mutex
	stopped bool
}

// keepAlive is the command that holds the VM open.
//
// A long sleep by default, because the plain sandbox image has no entrypoint
// worth running. An image that provides a daemon runs its own instead, and
// overriding it is how a VM ends up with a docker client and nothing listening.
func (a *Apple) keepAlive() []string {
	if len(a.Command) > 0 {
		return a.Command
	}

	if a.Image == "" || a.Image == defaultSandboxImage {
		return []string{"sleep", "86400"}
	}

	return nil
}

// defaultSandboxImage is the plain sandbox: no daemon, and no entrypoint worth
// running, so it is held open with a sleep.
const defaultSandboxImage = "alpine:3.20"

// defaultSandboxMemory is what a build VM gets when nothing says otherwise.
//
// `container run` defaults to 1 GiB, which is enough to run a step and not
// enough to capture its result: writes over virtiofs fill the guest's page
// cache, and a `mkdir` into the layer store then fails with ENOMEM. 8 GiB is
// the figure Docker Desktop settled on for the same job. It is a *ceiling*, not
// a reservation - the VM takes what it uses - so the cost of being generous is
// address space rather than memory. Override with EARTH_SANDBOX_MEMORY.
const defaultSandboxMemory = "8G"

// sandboxMemory is the configured size, so a machine that cannot spare 8 GiB
// has a way out that does not involve editing a constant.
func sandboxMemory() string {
	if m := os.Getenv("EARTH_SANDBOX_MEMORY"); m != "" {
		return m
	}

	return defaultSandboxMemory
}

// memory is the size this VM asks for.
//
// An Apple built as a literal rather than through NewApple gets the default
// too: a zero value here would mean "whatever container feels like", which is
// the bug this exists to prevent.
func (a *Apple) memory() string {
	if a.Memory != "" {
		return a.Memory
	}

	return sandboxMemory()
}

// SandboxName names the VM for a set of mounts.
//
// Derived from what is baked into the VM - the image and the two bind mounts -
// rather than from the process id, which is what it used to be. A per-process
// name meant every invocation booted its own VM: 620-700ms of Apple's
// `container run`, measured, and the largest single cost in a one-line-change
// rebuild (E19). Naming it after its contents lets the next build attach to the
// VM the last one left running.
//
// The digest is what makes reuse safe rather than merely fast: a VM with
// different mounts hashes differently and is never mistaken for this one. A
// guest binary rebuilt in place needs no new VM, because the directory holding
// it is bind-mounted and the new binary is already visible inside.
func SandboxName(image, guestDir, store string) string {
	return SandboxNameWith(image, guestDir, store, defaultSandboxMemory, nil)
}

// SandboxNameWith names a VM for a set of mounts and the command it runs.
//
// The command is part of what the machine *is*, not merely how it started. The
// docker sandbox runs its daemon with the containerd image store enabled -
// which is what makes `docker load` accept the OCI layout this engine writes -
// and a VM already running without that flag answers the listing, gets reused,
// and fails the load with a complaint about a missing `blobs/json`, the legacy
// format it fell back to.
func SandboxNameWith(image, guestDir, store, memory string, command []string) string {
	h := blake3.New(32, nil)
	// Length-prefixed, so ("ab", "c") and ("a", "bc") are different sandboxes
	// rather than the same one.
	//
	// Memory is in here because a VM is found and reused by name: leave it out
	// and raising the setting changes nothing until every existing sandbox has
	// been removed by hand, while the configuration insists it took effect.
	for _, part := range append([]string{image, guestDir, store, memory}, command...) {
		fmt.Fprintf(h, "%d:%s", len(part), part)
	}

	return "earthbuild-" + hex.EncodeToString(h.Sum(nil))[:16]
}

// NewApple returns a sandbox with the defaults.
func NewApple() *Apple {
	return &Apple{Image: defaultSandboxImage, Memory: sandboxMemory()}
}

// Boots reports how many VMs this sandbox has started, so "one VM per run" is
// observable rather than asserted in a comment.
func (a *Apple) Boots() int { return int(a.boots.Load()) }

// StoreDir is where layers live for this sandbox.
//
// Resolved here rather than when the VM starts, because where the layers live
// is configuration and not a property of a running machine. It mattered the
// moment the boot became lazy: the caller opens the L1 cache against this path
// before any step runs, and a StoreDir that was only filled in by Start
// answered "" until something had booted - so the cache would have been opened
// in the working directory.
func (a *Apple) StoreDir() string {
	if a.Store == "" {
		guestBin, err := a.guestBinary()
		if err == nil {
			a.Store = filepath.Join(filepath.Dir(guestBin), "store")

			return a.Store
		}

		// No guest binary to resolve it from - `container` is running and
		// nothing has been built yet, which is an ordinary state on a fresh
		// checkout. This used to answer "", and "" is the working directory:
		// the caller joins "layers" onto it and fills the checkout.
		//
		// A cache directory instead, which is absolute, stable between runs and
		// per-user. Start still fails with the diagnosis about the missing
		// binary; this is a query and has no way to.
		cache, err := os.UserCacheDir()
		if err != nil {
			cache = os.TempDir()
		}

		a.Store = filepath.Join(cache, "earthbuild", "store")
	}

	err := os.MkdirAll(a.Store, 0o750)
	if err != nil {
		return ""
	}

	return a.Store
}

// Name is the sandbox VM's container name, for diagnostics.
func (a *Apple) Name() string { return a.name }

// Confines is true: steps run inside a VM, so green paper A3 holds and results
// captured here may enter the cache.
func (a *Apple) Confines() bool { return true }

// SharesStoreAsRoot reports that the layer store appears inside this sandbox
// owned by root, whatever it is owned by outside.
//
// The VM's share does the shift, not a user namespace, so the guest has no
// `uid_map` to read and cannot know: the host says so instead. Without it every
// file in every base digests differently on the two sides and Κ₂ can never serve
// a RUN (E494).
func (a *Apple) SharesStoreAsRoot() bool { return true }

// Available reports whether this machine can run the backend, and says what is
// missing when it cannot. A backend that is merely absent should skip a test;
// one that is broken should say how.
func (a *Apple) Available() error {
	bin, err := osexec.LookPath("container")
	if err != nil {
		return errors.New("the `container` CLI is not installed (macOS 26 or later)")
	}

	out, err := osexec.Command(bin, "system", "status").CombinedOutput() //nolint:gosec // fixed argv
	if err != nil {
		return fmt.Errorf("`container system status` failed - is the service running? %w: %s", err, out)
	}

	if !strings.Contains(string(out), "apiserver is running") {
		return fmt.Errorf("the container apiserver is not running: %s", out)
	}

	return nil
}

// Start boots the VM and execs the guest inside it, returning the guest's stdio
// as the protocol connection.
//
// `container exec -i` was verified to be 8-bit clean, which the length-prefixed
// framing requires: a transport that mangled 0x00 or 0xff would corrupt frames
// rather than fail, and corruption is much harder to diagnose than a refusal.
func (a *Apple) Start(ctx context.Context) (Conn, error) {
	err := a.Available()
	if err != nil {
		return nil, err
	}

	guestBin, err := a.guestBinary()
	if err != nil {
		return nil, err
	}

	err = checkGuestArch(guestBin, guestArch())
	if err != nil {
		return nil, err
	}

	a.dir = filepath.Dir(guestBin)

	// The store gets its own bind mount rather than living inside the guest
	// binary's directory. Deriving it from that directory couples the two: a
	// caller setting Store to a path outside it produced a guest whose layer
	// root pointed at nothing, and the symptom was a step unable to find a
	// binary that had definitely been unpacked.
	// 0750: the layer store is this engine's, and the guest reads it as root.
	err = os.MkdirAll(filepath.Join(a.StoreDir(), "layers"), 0o750)
	if err != nil {
		return nil, fmt.Errorf("create the layer store: %w", err)
	}
	a.name = SandboxNameWith(a.Image, a.dir, a.StoreDir(), a.memory(), a.keepAlive())

	// The VM the last build left running is this build's VM, if its mounts are
	// the same - which the name asserts, being derived from them. Booting one
	// per invocation cost 620-700ms of `container run` on every build that had
	// anything to do (E19), for a machine identical to the one just discarded.
	// One listing, two answers: what to reap, and whether this build's VM is up.
	seen := listContainers()

	// Anything left by a process that has exited goes now, before this build
	// adds to the pile.
	reapOrphans(seen)

	if seen[a.name] != "running" {
		run := osexec.CommandContext(ctx, "container", "run", "-d", //nolint:gosec // fixed argv
			"--name", a.name,
			"-m", a.memory(),
			"-v", a.dir+":/earth",
			"-v", a.Store+":"+guestStore,
			a.Image)

		run.Args = append(run.Args, a.keepAlive()...)

		out, err := run.CombinedOutput()
		if err != nil {
			// A container of this name that exists but is not running is the
			// remains of a crash. Removed and retried once rather than reported,
			// because the alternative is a machine that cannot build until
			// someone is told to run a cleanup command.
			_ = osexec.Command("container", "rm", "-f", a.name).Run() //nolint:gosec // fixed argv

			retry := osexec.CommandContext(ctx, "container", "run", "-d", //nolint:gosec // fixed argv
				"--name", a.name,
				"-m", a.memory(),
				"-v", a.dir+":/earth",
				"-v", a.Store+":"+guestStore,
				a.Image)

			retry.Args = append(retry.Args, a.keepAlive()...)

			out2, err2 := retry.CombinedOutput()
			if err2 != nil {
				return nil, fmt.Errorf("boot the sandbox VM (image %s): %w: %s\n  after clearing a stale one: %s",
					a.Image, err, out, out2)
			}
		}

		a.boots.Add(1)
	}

	// The environment goes through `container exec -e`, NOT through cmd.Env.
	// cmd.Env sets the environment of the *host* process that speaks to the
	// container service; it never crosses into the VM. Setting it there is silent
	// - the guest simply uses its defaults - and the symptom appears much later
	// as a step that cannot find a file that was definitely written.
	args := []string{
		"exec", "-i",
		"-e", "EARTH_GUEST_ROOT=" + guestStore,
		// Scratch stays on the VM's own filesystem: the shared mount cannot serve
		// as an overlay upper layer, and keeping it out also means a step cannot
		// write into the host's cache.
		"-e", "EARTH_GUEST_SCRATCH=/var/lib/earthbuild/scratch",
	}

	// Forwarded, because the guest writes files too and the decision has to
	// reach it. It is the invocation's instruction about this build, not the
	// machine's about itself, so it travels with the build rather than being
	// read from whatever the VM happens to have.
	if at := os.Getenv(sourceDateEpoch); at != "" {
		args = append(args, "-e", sourceDateEpoch+"="+at)
	}

	args = append(args, a.name, "/earth/"+filepath.Base(guestBin))

	cmd := osexec.CommandContext(ctx, "container", args...) //nolint:gosec // a fixed argv

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("guest stdin: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("guest stdout: %w", err)
	}

	// Guest diagnostics go to our stderr rather than being discarded: when the
	// guest refuses to start, its reason is the only useful thing on the screen.
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("start earth-guestd in the sandbox: %w", err)
	}

	a.cmd = cmd

	return &duplex{r: stdout, w: stdin}, nil
}

// Stop ends this build's guest process and leaves the VM running.
//
// The guest is per-build - its own `container exec` over stdio - so ending it
// releases everything this build held. The VM is not: keeping it is the whole
// point, since booting one costs 620-700ms and the next build wants exactly the
// same machine.
//
// Remove takes it away, and something has to, or a developer accumulates a VM
// per project and attributes the memory to anything but the build tool.
func (a *Apple) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.stopped {
		return nil
	}

	a.stopped = true

	if a.cmd != nil && a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
		_ = a.cmd.Wait()
	}

	return nil
}

// Remove takes the VM away, whether or not this process started it.
func (a *Apple) Remove() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	name := a.name
	if name == "" {
		guestBin, err := a.guestBinary()
		if err != nil {
			return err
		}

		name = SandboxNameWith(a.Image, filepath.Dir(guestBin), a.StoreDir(), a.memory(), a.keepAlive())
	}

	out, err := osexec.Command("container", "rm", "-f", name).CombinedOutput() //nolint:gosec // fixed argv
	if err != nil {
		return fmt.Errorf("remove the sandbox VM %s: %w: %s", name, err, out)
	}

	return nil
}

// IsOrphanedSandbox reports whether a VM was left behind by a process that no
// longer exists.
//
// Only the old `earthbuild-<pid>-<n>` names can be orphans. Start used to
// remove *its own* name before booting, which can never be stale because the
// pid is this process - so nothing was ever reaped, and 38 such VMs were found
// running on the development machine, each holding 1GB, from runs whose process
// had long since exited. The comment claimed the pid was there so a VM
// outliving a crashed engine would be reaped; it guaranteed the opposite.
//
// A content-named VM is never an orphan. It has no owning process by design -
// that is what makes it reusable - and reaping one would take the sandbox out
// from under a concurrent build in another project.
func IsOrphanedSandbox(name string) bool {
	rest, ok := strings.CutPrefix(name, "earthbuild-")
	if !ok {
		return false
	}

	pidText, _, ok := strings.Cut(rest, "-")
	if !ok {
		return false
	}

	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return false
	}

	// Signal 0 asks whether the process exists without disturbing it. A process
	// this one may not signal still exists, and ESRCH is the only answer that
	// means gone.
	err = syscall.Kill(pid, 0)

	return errors.Is(err, syscall.ESRCH)
}

// ParseContainers reads `container ls -a` into name -> state.
//
// One listing answers both questions this engine has about VMs: which are
// orphans to reap, and whether this build's own is up. It used to ask twice,
// the second being `container exec <name> true` at 50-70ms on every build that
// ran anything - measured - against 10-20ms for a listing that already knew.
//
// A line that is not a listing yields nothing rather than a plausible name. The
// decision made from this is "boot a VM or attach to one", and attaching to a
// machine that is not there fails at the first step with a protocol error
// instead of booting.
func ParseContainers(out []byte) map[string]string {
	found := map[string]string{}

	for line := range strings.SplitSeq(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] == "ID" {
			continue
		}

		// Column 5 is STATE. Anything else in that position is not a listing
		// row, and guessing from a row this does not recognise is how a garbled
		// line becomes a container.
		switch f[4] {
		case "running", "stopped", "created", "stopping":
			found[f[0]] = f[4]
		}
	}

	return found
}

// reapOrphans removes VMs left behind by processes that have exited.
//
// Best effort throughout: a build must not fail because tidying up did not
// work, and the cost of missing one is that it is found next time.
func reapOrphans(seen map[string]string) {
	for name := range seen {
		if !IsOrphanedSandbox(name) {
			continue
		}

		_ = osexec.Command("container", "rm", "-f", name).Run() //nolint:gosec // fixed argv
	}
}

// listContainers asks the backend what exists. An error is an empty listing:
// the caller's next move is to boot, which is the right move when the question
// cannot be answered.
func listContainers() map[string]string {
	out, err := osexec.Command("container", "ls", "-a").Output() //nolint:gosec // fixed argv
	if err != nil {
		return nil
	}

	return ParseContainers(out)
}

// guestBinary locates the agent that runs inside the VM.
func (a *Apple) guestBinary() (string, error) {
	if a.GuestBinary != "" {
		return a.GuestBinary, nil
	}

	p, err := findGuestBinary()
	if err != nil {
		return "", err
	}

	a.GuestBinary = p

	return p, nil
}

// guestArch is the VM's architecture. Apple's container runs the host's
// architecture, so an arm64 Mac gets an arm64 guest.
func guestArch() string {
	if os.Getenv("EARTH_GUEST_ARCH") != "" {
		return os.Getenv("EARTH_GUEST_ARCH")
	}

	return "arm64"
}
