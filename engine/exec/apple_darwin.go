package exec

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"lukechampine.com/blake3"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/guestd"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/mat/overlay"
	"github.com/EarthBuild/earthbuild/engine/timing"
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

	// guestExit is why the guest process ended, and guestGone says it has.
	// Watched rather than waited for: `container exec` does not close the pipe
	// when the guest behind it dies, so nothing else would notice.
	guestMu   sync.Mutex
	guestExit error
	guestGone bool

	// fill faults a path into a step's base, or nil where nothing asked this
	// sandbox to. Guarded because it is set by a worker before the sandbox
	// starts and read when the relay is launched. See SetFill.
	fillMu sync.Mutex
	fill   func(handle, path string) error
	// progress answers how far a blob this host is fetching has been written.
	// See SetProgress: set per build, read per question.
	progress func(blob string, have int64) (int64, error)
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
	resumes atomic.Int64
	// bootMu serialises making the VM exist, so a prewarm and a start cannot
	// both run `container run` for one name.
	bootMu  sync.Mutex
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
		return keepAliveUntilIdle(idleSetting())
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

// EnvSandboxCPUs is how many cores the VM asks for.
//
// **Four, because nobody asked.** `container run` defaults to four vCPUs and
// this never passed `-c`, so every `RUN` on a sixteen-core machine had a quarter
// of it. Docker's VM on the same machine takes all sixteen - which is most of
// why a cold `+earthly` measured slower here than under BuildKit: the same `go
// build` was given four cores on one side and sixteen on the other.
//
// Asked for explicitly now, so the number is a decision rather than somebody
// else's default. Overridable, because a machine with other work to do is the
// case that wants a smaller one.
const EnvSandboxCPUs = "EARTH_SANDBOX_CPUS"

// sandboxCPUs is how many cores to ask for: this machine's, unless told.
//
// A value that is not a count falls back rather than refusing. The setting
// exists to let somebody take cores away, and a typo in it should cost the
// default, not the build.
func sandboxCPUs() string {
	if n, err := strconv.Atoi(os.Getenv(EnvSandboxCPUs)); err == nil && n > 0 {
		return strconv.Itoa(n)
	}

	return strconv.Itoa(runtime.NumCPU())
}

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

// cpus is how many cores this VM asks for. See EnvSandboxCPUs.
func (a *Apple) cpus() string { return sandboxCPUs() }

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
	// guestFast is in here for the reason memory is: a VM is found and reused by
	// name, so a sandbox started before this engine attached a volume would be
	// reused without one - and the build would quietly put its caches back on
	// the shared store, which is the thing being fixed.
	//
	// The idle setting is in here for the same reason once more. It decides how
	// long the machine lives, which is a property of the machine and not of the
	// request that found it - so a build asking for an hour and a build asking
	// for the default have to be asking about two machines, or the second gets
	// whatever the first said (E549's failure class, E555's occasion).
	for _, part := range append(
		[]string{
			image, guestDir, store, memory, guestFast,
			idleSetting(), scratchTmpfsSetting(), storeSetting(), pinSetting(), digestSetting(),
			sandboxCPUs(), shimSetting(),
		},
		command...) {
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

// Resumes reports how many stopped VMs this sandbox woke rather than replaced,
// so the cheap path is observable rather than asserted in a comment.
func (a *Apple) Resumes() int { return int(a.resumes.Load()) }

// resume wakes the stopped VM this build is named for, reporting whether it
// came back. A failure is not an error: every caller's next move is to boot one,
// which is the right move whatever went wrong here.
func (a *Apple) resume(ctx context.Context) bool {
	err := osexec.CommandContext(ctx, "container", "start", a.name).Run() //nolint:gosec // fixed argv
	if err != nil {
		return false
	}

	a.resumes.Add(1)

	return true
}

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
// probeService asks the container service whether it is running.
//
// A variable so a test can count the asking, which is the whole point of the
// memoisation above it.
var probeService = askTheService

// availableOnce holds the answer for the life of the process.
var availableOnce = onceFor()

// onceFor is a fresh memo, so a test can start from nothing.
func onceFor() *serviceAnswer { return &serviceAnswer{} }

type serviceAnswer struct {
	once sync.Once
	err  error
}

// Available reports whether the container service can run a sandbox.
//
// **Asked once.** The probe is a `container system status`, which costs 36ms,
// and a single build asked it four times - a third of the time every invocation
// spent obtaining a guest client before it could run anything (E645). A service
// that stops mid-build is reported by the operation that then fails, not by a
// probe that happened to run again; the same reasoning memoises `needsUserXattr`.
func (a *Apple) Available() error {
	availableOnce.once.Do(func() { availableOnce.err = probeService() })

	return availableOnce.err
}

func askTheService() error {
	bin, err := osexec.LookPath("container")
	if err != nil {
		return errors.New("the `container` CLI is not installed (macOS 26 or later)")
	}

	ctx, cancel := briefly()
	defer cancel()

	out, err := osexec.CommandContext(ctx, bin, "system", "status").CombinedOutput() //nolint:gosec // fixed argv
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
// ensureRunning makes this build's VM exist, and is the half of Start that
// costs anything.
//
// Separate because it needs nothing the Earthfile says - the sandbox image is
// this engine's, not the build's - so it can happen while the plan is still
// being worked out. See Prewarm.
//
// Guarded, because a prewarm and a start can arrive together: two `container
// run` calls for one name is one failure and one wasted boot, and the loser
// would report a machine that is running perfectly well as broken.
func (a *Apple) ensureRunning(ctx context.Context) error {
	a.bootMu.Lock()
	defer a.bootMu.Unlock()

	err := a.Available()
	if err != nil {
		return err
	}

	guestBin, err := a.guestBinary()
	if err != nil {
		return err
	}

	err = checkGuestArch(guestBin, guestArch())
	if err != nil {
		return err
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
		return fmt.Errorf("create the layer store: %w", err)
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

	// And anything named for a directory that has since gone. A content-named
	// VM has no owning process, so reapOrphans cannot see it; see
	// stranded_darwin.go for why nothing else could reach it either.
	reapStranded(seen)

	// **And whether the VM that is there is looking at this store.** A store
	// deleted and recreated leaves the path in place, so `reapStranded`'s rule
	// does not fire, and the VM goes on reading an inode that is gone - which
	// hangs rather than fails (E671).
	if seen[a.name] != "" && !a.seesStore() {
		rmCtx, rmCancel := briefly()
		_ = osexec.CommandContext(rmCtx, "container", "rm", "-f", a.name).Run() //nolint:gosec // fixed argv

		rmCancel()
		delete(seen, a.name)
	}

	if seen[a.name] != "running" {
		a.ensureVolume(ctx)

		// **A VM of this name that is merely stopped is this build's VM asleep.**
		// Same mounts and same volume - the name is a digest of them - so waking
		// it is all that is wanted. The idle timeout stops an unattended sandbox
		// after 30 minutes, which makes the first build of a session find one
		// every time.
		//
		// It used to boot a replacement, and by the slowest route available: a
		// `container run` that fails on the name in use, an `rm -f`, and then
		// the boot. 953ms measured, against 592ms to start the one already
		// there (E524).
		// De Morgan's law would turn this into `!= "stopped" || !resume(...)`,
		// which is the same condition and a worse sentence: what is being asked
		// is whether the machine was stopped *and* came back, and the negation
		// belongs to that pair rather than to each half (QF1001).
		//
		//nolint:staticcheck // see above
		if !(seen[a.name] == "stopped" && a.resume(ctx)) {
			run := osexec.CommandContext(ctx, "container", a.runArgs()...) //nolint:gosec // fixed argv

			out, err := run.CombinedOutput()
			if err != nil {
				// A container of this name that exists but is not running is the
				// remains of a crash. Removed and retried once rather than
				// reported, because the alternative is a machine that cannot
				// build until someone is told to run a cleanup command.
				_ = osexec.CommandContext(ctx, "container", "rm", "-f", a.name).Run() //nolint:gosec // fixed argv

				retry := osexec.CommandContext(ctx, "container", a.runArgs()...) //nolint:gosec // fixed argv

				out2, err2 := retry.CombinedOutput()
				if err2 != nil {
					return fmt.Errorf("boot the sandbox VM (image %s): %w: %s\n  after clearing a stale one: %s",
						a.Image, err, out, out2)
				}
			}

			a.boots.Add(1)
		}
	}

	return nil
}

// Start boots the sandbox if it is not up and returns a connection to its guest.
//
// Idempotent: a VM that is already running is reused, which is what makes the
// second build of a session fast (E524).
func (a *Apple) Start(ctx context.Context) (Conn, error) {
	err := a.ensureRunning(ctx)
	if err != nil {
		return nil, err
	}

	// Memoised by guestBinary, so this is the path ensureRunning already found
	// and checked rather than a second search.
	guestBin, err := a.guestBinary()
	if err != nil {
		return nil, err
	}

	// The environment goes through `container exec -e`, NOT through cmd.Env.
	// cmd.Env sets the environment of the *host* process that speaks to the
	// container service; it never crosses into the VM. Setting it there is silent
	// - the guest simply uses its defaults - and the symptom appears much later
	// as a step that cannot find a file that was definitely written.
	args := []string{
		"exec", "-i",
		"-e", "EARTH_GUEST_ROOT=" + a.guestRoot(),
		// Where an export is staged, which is not where the layers live once
		// they move off the shared mount. Empty means "the same place", which
		// is what it is until they do. See guestExportDir.
		"-e", guest.EnvExportDir + "=" + a.guestExportDir(),
		// Scratch stays on the VM's own filesystem: the shared mount cannot serve
		// as an overlay upper layer, and keeping it out also means a step cannot
		// write into the host's cache.
		"-e", "EARTH_GUEST_SCRATCH=/var/lib/earthbuild/scratch",
		// Storage this sandbox owns, for the things that must outlive a step
		// without the host needing to see them. See guestFast.
		"-e", guest.EnvFast + "=" + guestFast,
		// How long an unused sandbox waits before stopping.
		//
		// Forwarded because it was not, and `EnvIdle`'s own documentation says
		// "unset means DefaultIdle, because the guest is started by a host that
		// supplies one" - which was true of the namespace backend and never
		// true here. A developer setting it against a VM sandbox was changing
		// nothing (E555).
		"-e", guest.EnvIdle + "=" + idleSetting(),
		// **Read in the guest, and until now never sent there.** The scratch
		// tmpfs option is consulted by the materialiser, which runs inside the
		// VM, from an environment nothing on this backend populated - so setting
		// it changed nothing and said nothing, which is the failure
		// `SOURCE_DATE_EPOCH` already had here once (E555, E591).
		//
		// Worth sending: the guest's scratch is ext4 on a virtio block device
		// and twenty thousand file creations cost 1.91s there against 0.14s on
		// tmpfs. It is part of the machine's name below, so asking for a
		// different size gets a different machine rather than the last one.
		"-e", overlay.EnvScratchTmpfs + "=" + scratchTmpfsSetting(),
		// This machine exists for this guest and is held open by the keep-alive
		// in runArgs, so the guest going idle is the machine having nothing to
		// do. Without it the agent stopped and the VM stayed up until its sleep
		// ended (E555).
		"-e", guest.EnvOwnsMachine + "=1",
		// Where this guest listens for faults, when anything wants to fault.
		// Set always: the guest binds cheaply and nothing dials unless a worker
		// asked for fault-in. See SetFill.
		"-e", guest.EnvFillSocket + "=" + guestFillSocket,
	}

	// `SOURCE_DATE_EPOCH` was forwarded here and no longer is. It reached the
	// guest correctly and went stale: a sandbox is named by its image, store
	// and memory, so a second build wanting a different epoch - or none - finds
	// the first build's VM and is answered with the first build's instruction.
	//
	// It travels in the request that it applies to instead, which is the only
	// place a per-build decision can live when the machine serving the build
	// outlives it (E549).

	// Likewise: the phases worth timing are mostly the guest's, and a switch
	// that stops at the sandbox wall reports the round trip without ever saying
	// what the round trip was doing.
	if on := os.Getenv(timing.Env); on != "" {
		args = append(args, "-e", timing.Env+"="+on)
	}

	// Read inside the guest, where the threads are, so it has to travel. Safe
	// to send at start rather than per request only because it is in the
	// sandbox's name: see pinSetting.
	if on := os.Getenv(guest.EnvTracePin); on != "" {
		args = append(args, "-e", guest.EnvTracePin+"="+on)
	}

	// Read by the guest when it launches a step. In the name too: see
	// shimSetting.
	if on := os.Getenv(guest.EnvStepShim); on != "" {
		args = append(args, "-e", guest.EnvStepShim+"="+on)
	}

	// Read by the unpacker, which now runs on the far side of this wall.
	if on := os.Getenv(image.EnvHashOnUnpack); on != "" {
		args = append(args, "-e", image.EnvHashOnUnpack+"="+on)
	}

	// **Settings the guest reads have to be handed to the guest.** Both of these
	// are read inside the sandbox and neither was forwarded, so on this backend
	// they did nothing at all - and the way that surfaced was an experiment that
	// "ruled out" dentry relief by raising a limit the guest never saw (E812).
	// A setting that silently does nothing is worse than one that is missing,
	// because it answers when it is asked.
	if on := os.Getenv(guest.EnvDentryLimit); on != "" {
		args = append(args, "-e", guest.EnvDentryLimit+"="+on)
	}

	if on := os.Getenv(guestd.EnvProfile); on != "" {
		args = append(args, "-e", guestd.EnvProfile+"="+on)
	}

	if on := os.Getenv(guestd.EnvProfileMode); on != "" {
		args = append(args, "-e", guestd.EnvProfileMode+"="+on)
	}

	args = append(args, a.name, "/earth/"+filepath.Base(guestBin))

	cmd := osexec.CommandContext(ctx, "container", args...) //nolint:gosec // a fixed argv

	// **Our own pipes, not `StdinPipe`.** `Cmd` owns the pipes it makes and
	// closes them in `Wait`, and its documentation says calling `Wait` before
	// every write has completed is incorrect - which a watcher started
	// alongside the guest does by construction. The symptom is the guest's
	// stdin closing under it: `Serve` reads EOF, returns nil, and the guest
	// exits cleanly and silently, leaving the host waiting for a reply from a
	// process that decided it was finished (E518).
	inR, stdin, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("guest stdin: %w", err)
	}

	stdout, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = stdin.Close()

		return nil, fmt.Errorf("guest stdout: %w", err)
	}

	cmd.Stdin = inR
	cmd.Stdout = outW

	// Guest diagnostics go to our stderr rather than being discarded: when the
	// guest refuses to start, its reason is the only useful thing on the screen.
	cmd.Stderr = os.Stderr

	err = cmd.Start()

	// The child holds its own ends now. Keeping ours open would mean the read
	// below never sees EOF even when the guest is gone, which is the failure
	// this whole arrangement exists to make visible.
	_ = inR.Close()
	_ = outW.Close()

	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()

		return nil, fmt.Errorf("start earth-guestd in the sandbox: %w", err)
	}

	a.cmd = cmd

	// **A guest that dies must not leave the build waiting for its reply.**
	//
	// `container exec` does not close this pipe when the process behind it
	// exits, so a guest that is killed - or that panics, or is reaped by the
	// kernel - leaves the host blocked in a read that will never return. That is
	// not a slow build: it is one that never ends, and one in three cold builds
	// of this repository was doing it.
	//
	// Closing the read side here turns that into an EOF the protocol already
	// knows how to report, and the exit status turns it into a sentence naming
	// what happened rather than a cancelled context blamed on the step.
	go func() {
		waitErr := cmd.Wait()

		a.guestMu.Lock()
		a.guestExit = waitErr
		a.guestGone = true
		a.guestMu.Unlock()

		_ = stdout.Close()
	}()

	// The fault-in relay, once the guest it dials is running. Started after the
	// guest rather than with it: the relay connects to a socket the guest binds,
	// and one that arrives first waits (see dialFills) but should not have to.
	a.serveFills()

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

		// Not waited for here: the watcher started with the guest owns the
		// Wait, and calling it twice is an error rather than a second answer.
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

	ctx, cancel := briefly()
	defer cancel()

	out, err := osexec.CommandContext(ctx, "container", "rm", "-f", name).CombinedOutput() //nolint:gosec // fixed argv
	if err != nil {
		return fmt.Errorf("remove the sandbox VM %s: %w: %s", name, err, out)
	}

	// **The VM's storage goes with the VM.** `container rm` leaves volumes
	// alone, which is right for a sandbox that stops and comes back and wrong
	// for one being taken away: nothing will ever name this one again, because
	// the name is a digest of mounts that included a directory now gone.
	//
	// Every VM-booting test names its sandbox after a temporary guest directory,
	// so each run minted a volume for ever. Eleven of them, holding 14GB,
	// accumulated in an hour of running this suite (E526).
	//
	// After the container, not before: a volume still attached to a container is
	// refused, and the refusal would be the only news this returned.
	volCtx, volCancel := briefly()
	defer volCancel()

	_ = osexec.CommandContext(volCtx, "container", "volume", "rm", volumeFor(name)).Run() //nolint:gosec // fixed argv

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
//
// It can still be *stranded*, which is a different question with a different
// answer: see reapStranded in stranded_darwin.go. Treating "not an orphan" as
// "never removable" is how thirty-two of them accumulated.
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

		reapCtx, reapCancel := briefly()
		_ = osexec.CommandContext(reapCtx, "container", "rm", "-f", name).Run() //nolint:gosec // fixed argv

		reapCancel()
	}
}

// listContainers asks the backend what exists. An error is an empty listing:
// the caller's next move is to boot, which is the right move when the question
// cannot be answered.
func listContainers() map[string]string {
	ctx, cancel := briefly()
	defer cancel()

	out, err := osexec.CommandContext(ctx, "container", "ls", "-a").Output() //nolint:gosec // fixed argv
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

// guestFast is where the sandbox's own storage is mounted inside the guest.
//
// A block device rather than a share, so the filesystem lives in the guest
// kernel and a metadata operation never crosses the VM boundary. Measured in one
// guest, 4,000 files: untarring into it takes 0.09s where the shared store takes
// 2.31s, and removing the tree 0.00s against 0.62s.
const guestFast = "/var/lib/earthbuild/fast"

// volumeName is the block-device volume this sandbox keeps fast storage on.
//
// **Named after the sandbox, and that is a correctness requirement rather than
// tidiness.** Two VMs attaching one volume writably corrupts the filesystem, and
// Virtualization.framework offers no lock that would stop them - it is silent.
// A sandbox is already named by everything that makes it different, so deriving
// the volume from it means exactly one VM ever attaches it.
func (a *Apple) volumeName() string { return volumeFor(a.name) }

// volumeFor names a sandbox's storage. One definition, because Remove works
// from a name it may have had to recompute rather than from a.name, and a
// suffix written twice is a suffix that drifts.
func volumeFor(name string) string { return name + "-fast" }

// runArgs is the argv that starts this sandbox.
//
// One place, because there were two: the start and the retry-after-removal built
// the same list separately, so anything added to a build's sandbox had to be
// added twice or it applied only until the first crash.
func (a *Apple) runArgs() []string {
	args := make([]string, 0, 13+len(a.keepAlive()))
	args = append(args,
		"run", "-d",
		"--name", a.name,
		"-m", a.memory(),
		"-c", a.cpus(),
		"-v", a.dir+":/earth",
		"-v", a.Store+":"+guestStore,
		// Which directory this is, not merely where it was. See LabelStoreInode.
		"-l", LabelStoreInode+"="+strconv.FormatUint(inodeOf(a.Store), 10),
		"-v", a.volumeName()+":"+guestFast,
		a.Image,
	)

	return append(args, a.keepAlive()...)
}

// ensureVolume creates this sandbox's volume if it is not already there.
//
// Best-effort and deliberately quiet: `volume create` fails when the volume
// exists, which is the ordinary case on every build after the first. A failure
// that is not that shows up as the sandbox refusing to start with a mount it
// cannot satisfy, which names the volume - a better message than anything that
// could be guessed here.
func (a *Apple) ensureVolume(ctx context.Context) {
	_ = osexec.CommandContext(ctx, "container", "volume", "create", a.volumeName()).Run() //nolint:gosec // fixed argv
}

// GuestFailure says why the guest ended, if it ended on its own.
//
// Empty while the guest is running and after an ordinary shutdown. A caller that
// sees its connection close asks this, so the message names the cause instead of
// reporting that a pipe closed - which is true, uninformative, and reads like a
// bug in the caller.
func (a *Apple) GuestFailure() string {
	a.guestMu.Lock()
	defer a.guestMu.Unlock()

	if !a.guestGone {
		return ""
	}

	if a.guestExit == nil {
		return "the guest in this sandbox exited"
	}

	return fmt.Sprintf("the guest in this sandbox exited: %v"+
		"\n  the build was waiting for it to answer, and nothing else would have"+
		" reported this", a.guestExit)
}

// Prewarm starts this build's VM without waiting for the plan.
//
// **The boot needs nothing the Earthfile says.** The sandbox image is this
// engine's own, not the build's, so which machine to start is known before a
// line is parsed - while planning meanwhile spends a registry round trip
// resolving what the build's `FROM` means. Done one after the other a build pays
// for both; done together it pays for the longer of the two (E537).
//
// Silent about failure on purpose. This is an optimisation, so a prewarm that
// cannot work must leave a build that is slower rather than one that stops -
// and whatever is wrong is reported by the Start that follows, which has the
// context to say it properly.
func (a *Apple) Prewarm(ctx context.Context) {
	if a.Available() != nil {
		return
	}

	_ = a.ensureRunning(ctx)
}

// idleSetting is what this invocation asks an unused sandbox to wait.
//
// The host's own environment, passed on rather than interpreted: the guest
// parses it and owns what an unparseable value means, and a second reading here
// would be a second answer to the same question.
// scratchTmpfsSetting is the size a scratch tmpfs was asked for, or empty.
//
// Part of the sandbox's name as well as its environment: a machine's scratch is
// made once, when it starts, so a build asking for a different size must not be
// handed the machine that was built for the last one. Exactly the reason
// `idleSetting` is in that name.
func scratchTmpfsSetting() string { return os.Getenv(overlay.EnvScratchTmpfs) }

func idleSetting() string {
	if v := os.Getenv(guest.EnvIdle); v != "" {
		return v
	}

	return guest.DefaultIdle.String()
}

// keepAliveUntilIdle holds a sandbox open while anything is using it.
//
// **The reaper has to outlive the agent, and the agent is what was doing the
// reaping.** `guest.Idle` stops a sandbox nobody has used - that is its whole
// design, and it lives in the guest because the host is the process that gets
// killed. On this backend the guest does not live long enough to run it: it is
// one `container exec` per build, and it is gone within a second of the build
// ending. So the timeout never fired, the machine stayed up until its sleep
// ended a day later, and twenty-six of them were found on one laptop (E555).
//
// This is the same rule in the one process that is still there: PID 1, which is
// the machine. It asks whether an agent is present rather than being told, so
// nothing has to remember to report - a build that crashes, a host that is
// SIGKILLed and a `container exec` that dies all look the same from here, which
// is exactly the set of cases a reaper is for.
//
// A shell loop because the sandbox image is the plain one and a shell is what
// it has. `pgrep` and `date` are busybox builtins; nothing here needs the
// engine's own binaries, which is the point - this must work when the agent
// cannot start at all.
func keepAliveUntilIdle(idle string) []string {
	secs := int((30 * time.Minute).Seconds())

	d, err := time.ParseDuration(idle)
	if err == nil && d > 0 {
		secs = int(d.Seconds())
	} else if err == nil {
		// Zero is `EnvIdle`'s "keep it up", which a developer debugging a
		// sandbox asks for. The plain sleep is what this was before any of
		// this, so "never" means exactly as long as it ever did - a day - and
		// not longer.
		return []string{"sleep", "86400"}
	}

	// The poll is a fraction of the timeout, so a sandbox stops within about a
	// tenth of what was asked rather than a fixed interval that is either
	// wasteful for a long timeout or coarse for a short one. Floored at a
	// second: a poll faster than that is a busy loop in every idle VM.
	poll := max(secs/10, 1)

	return []string{"sh", "-c", fmt.Sprintf(
		`idle=0; while :; do sleep %d; `+
			`if pgrep earth-guestd >/dev/null 2>&1; then idle=0; `+
			`else idle=$((idle+%d)); fi; `+
			`[ "$idle" -lt %d ] || exit 0; done`, poll, poll, secs)}
}

// briefly bounds a `container` invocation that has no caller's context to take.
//
// These are probes and cleanups - is the backend there, take that VM away, list
// what is running - and none of them is the build's work. Unbounded they can
// hang it: a wedged `container ls` at startup stops a build that has not begun,
// with nothing on screen to say what it is waiting for.
//
// Not the caller's context even where one exists nearby: a cleanup that stops
// because the build was cancelled leaves the VM behind, which is the thing the
// cleanup was for.
func briefly() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// seesStore reports whether the sandbox already running is looking at the
// directory this build is about to use.
//
// Best effort in the reuse direction: a backend that will not answer, or a VM
// this engine cannot read a label from, is kept. See SandboxSeesStore.
func (a *Apple) seesStore() bool {
	ctx, cancel := briefly()
	defer cancel()

	out, err := osexec.CommandContext(ctx, "container", "inspect", a.name).Output() //nolint:gosec // fixed argv
	if err != nil {
		return true
	}

	var found []struct {
		Configuration struct {
			Labels map[string]string `json:"labels"`
		} `json:"configuration"`
	}

	err = json.Unmarshal(out, &found)
	if err != nil || len(found) == 0 {
		return true
	}

	return SandboxSeesStore(found[0].Configuration.Labels, inodeOf(a.Store))
}

// GuestPath is where this sandbox's guest sees a host path, if it sees it.
//
// **The two sides do not share a filesystem, only some of it.** A host path
// under the store or the guest directory is visible inside the VM at a
// different place, and anything else is not visible at all - so a caller
// handing the guest a path has to translate it and has to be told when it
// cannot.
//
// Reported rather than assumed. A path outside both mounts would name something
// inside the VM that has nothing to do with what the host meant, which is a
// worse answer than "no".
func (a *Apple) GuestPath(host string) (string, bool) {
	for _, m := range []struct{ from, to string }{
		{a.Store, guestStore},
		{a.dir, "/earth"},
	} {
		if m.from == "" {
			continue
		}

		rel, err := filepath.Rel(m.from, host)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}

		return path.Join(m.to, filepath.ToSlash(rel)), true
	}

	return "", false
}

// guestRoot is where this sandbox's guest keeps its layers.
//
// **On the block device it owns, when asked.** The shared mount is reached over
// virtiofs and every metadata operation on it crosses the VM boundary; the
// volume is a filesystem in the guest kernel. See `guest.EnvStoreInVM` for what
// that is worth and what it costs.
func (a *Apple) guestRoot() string {
	if guest.StoreInVM() {
		return path.Join(guestFast, "store")
	}

	return guestStore
}

// guestExportDir is where the guest stages an artifact on its way out.
//
// The shared mount, whenever the layers are not already on it. An export exists
// to leave the sandbox and the host reads it off that mount by a path it
// computes itself; when the layers moved to the guest's own device the staging
// followed them, onto a filesystem the host cannot open, and every SAVE ARTIFACT
// failed with `the guest did not stage` naming a host path that was never going
// to exist.
//
// Empty when the layers have not moved, because then the guest's own default is
// this directory already and saying it twice is how two answers come to
// disagree. See guest.EnvExportDir.
func (a *Apple) guestExportDir() string {
	if guest.StoreInVM() {
		return guestStore
	}

	return ""
}

// storeSetting is where this invocation asked the layer store to live.
//
// In the sandbox's name for the reason memory and the idle timeout are: a VM is
// found and reused by name, so a machine started against one store would be
// reused for a build asking for the other - and the build would quietly read a
// store that is not the one it meant.
func storeSetting() string { return os.Getenv(guest.EnvStoreInVM) }

// pinSetting is whether this invocation asked a traced step to share a CPU with
// the thread answering its syscalls.
//
// In the name for the same reason, and it is not a fussy one: the guest reads
// this at start, so a sandbox already running was started with whatever the
// *first* build said - and flipping the switch would report the old arrangement
// under the new name, which is how a measurement comes out saying nothing
// changed (E549).
func pinSetting() string { return os.Getenv(guest.EnvTracePin) }

// digestSetting is whether this invocation asked the unpacker to hand its
// digests on or let the store read the tree back.
//
// In the name for the reason pinSetting is: the guest reads it when it unpacks,
// and a machine already running was started under whatever the previous build
// said. An A/B where both arms reuse one machine reports that the switch does
// nothing, which reads exactly like a switch that does nothing (E682).
func digestSetting() string { return os.Getenv(image.EnvHashOnUnpack) }

// shimSetting is whether this invocation launches steps through the shim.
//
// In the name for the reason the two above are: the guest reads it when it
// launches a step, and a machine already running was started under whatever the
// previous build said. An A/B whose arms share a sandbox reports that the switch
// does nothing, which reads exactly like a switch that does nothing (E549, E682,
// E701).
func shimSetting() string {
	if guest.StepShimWanted() {
		return "shim"
	}

	return "noshim"
}
