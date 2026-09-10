//go:build linux

package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	osexec "os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/EarthBuild/earthbuild/cmd/earth-vmboot/vmboot"
	"github.com/EarthBuild/earthbuild/engine/bulk"
)

// Firecracker runs the guest inside a microVM rather than in namespaces.
//
// **A second boundary, chosen for what an escape costs.** The Native backend
// confines a child process, which is one boundary over a shared kernel: a kernel
// bug is an escape, and the thing on the other side is an Earthfile - somebody
// else's code, fetched from somewhere else, on a machine with credentials on it.
//
// Firecracker rather than a fuller VMM because its device model is the argument:
// block, net, vsock, balloon and rng, and nothing else. It has no virtio-fs at
// all, which decides the shape of everything here - the layer store arrives as a
// block device, and the guest formats it (E971).
type Firecracker struct {
	// Binary is the firecracker executable. EARTH_FIRECRACKER overrides it.
	Binary string
	// Kernel is an uncompressed ELF vmlinux. Firecracker cannot boot a bzImage,
	// which is what a distribution ships, so this is a deliberate artefact
	// rather than something found on the machine.
	Kernel string
	// Initrd carries earth-vmboot as `/init` and earth-guestd beside it.
	Initrd string
	// StoreImage is the block device holding the layer store, formatted XFS so
	// the guest keeps reflinks on a host that may have none. See EnvVMStore.
	StoreImage string

	// Root is where this sandbox keeps its sockets. A temporary directory when
	// empty.
	Root string

	// Store is where the *host* keeps this sandbox's blobs, action cache and
	// staged exports. Not the guest's layers, which are on StoreImage: the two
	// are one directory only where a filesystem is shared, and none is here.
	Store string

	// VCPUs and MemoryMiB size the guest. Zero takes this machine's own
	// processors and half its memory - see defaultCPUs and defaultMemory, and
	// EnvVMCPUs for why a build wants that rather than a small slice.
	VCPUs     int
	MemoryMiB int

	mu  sync.Mutex
	cmd *osexec.Cmd
	tmp string
	// unlock releases this sandbox's claim on its directory. See holdSandbox.
	unlock  func()
	vsockAt string
	exports string
	tap     string
	net     vmboot.Net
	release func()
	console *os.File
	// ownNet is set when the engine provides the guest's network itself, which
	// is the default; own is the stack it started. See userNet.
	ownNet bool
	own    *userNet
	// conn is the agent's own channel, kept so that stopping can close it: the
	// agent ends at end-of-stream, which is what lets the guest flush.
	conn Conn
	// gone is closed when the VMM process ends, however it ended. See dialPort.
	gone    chan struct{}
	stopped bool

	// exporting holds the export device to one artifact at a time. There is one
	// device and the stream starts at its beginning; two at once would
	// interleave. `SAVE ARTIFACT` is not on the hot path, and the alternative -
	// offsets, agreed between a host and a guest - is a second allocator to get
	// wrong.
	exporting sync.Mutex
}

const (
	// guestCID is the guest's vsock address. 2 is the host and 0-2 are
	// reserved, so 3 is the first a guest may have.
	guestCID = 3
)

// NewFirecracker returns a sandbox with the defaults and the environment's
// overrides.
func NewFirecracker() *Firecracker {
	return &Firecracker{
		Binary:     envOr("EARTH_FIRECRACKER", "firecracker"),
		VCPUs:      envInt(EnvVMCPUs),
		MemoryMiB:  envInt(EnvVMMemory),
		Kernel:     os.Getenv("EARTH_VM_KERNEL"),
		Initrd:     os.Getenv("EARTH_VM_INITRD"),
		StoreImage: os.Getenv(EnvVMStore),
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}

	return fallback
}

// Available reports whether this machine can run a microVM, and why not when it
// cannot.
//
// **I11 rather than I10**: the caller degrades to the namespace backend and says
// so. Refusing would break every machine that works today, and the boundary is
// an improvement rather than a requirement.
func (f *Firecracker) Available() error {
	_, err := os.Stat("/dev/kvm")
	if err != nil {
		return fmt.Errorf("no /dev/kvm, so no hardware virtualisation: %w"+
			"\n  a hosted CI runner commonly has none, and nested virtualisation"+
			" is not something Firecracker can emulate", err)
	}

	_, err = osexec.LookPath(f.Binary)
	if err != nil {
		return fmt.Errorf("no firecracker on this machine: %w"+
			"\n  set EARTH_FIRECRACKER to it, or install it", err)
	}

	for what, at := range map[string]string{"kernel": f.Kernel, "initrd": f.Initrd} {
		if at == "" {
			return fmt.Errorf("no %s for the guest"+
				"\n  set EARTH_VM_KERNEL and EARTH_VM_INITRD: a microVM boots an"+
				" uncompressed vmlinux, which a distribution does not ship", what)
		}

		_, err := os.Stat(at)
		if err != nil {
			return fmt.Errorf("the %s at %s is not readable: %w", what, at, err)
		}
	}

	return nil
}

// StoreDir is where layers live for this sandbox.
//
// **A host directory, because every caller opens it here.** The CLI opens the
// blob store, the action cache and the profile store against this before
// anything boots, and `SAVE ARTIFACT` reads the staged artifact off it with an
// ordinary `os.Lstat`. A guest path would name a directory nothing on the host
// writes to - and it would *resolve*, so the blobs would land somewhere and the
// guest would find none of them.
//
// The guest's own layers are elsewhere and stay there: they live on the block
// device it formats, which the host cannot open while the guest holds it (see
// `guest.EnvStoreInVM`). On a backend with a shared filesystem those two are the
// same directory; here they cannot be, because Firecracker has no virtio-fs.
// What still has to cross - blobs in, exports out - crosses as a stream and not
// as a mount.
//
// Under the user's cache directory by default, because the store is a cache: it
// is worth having because the next build reads it, and a temporary directory
// would be correct and worthless.
func (f *Firecracker) StoreDir() string {
	if f.Store != "" {
		return f.Store
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		// No cache directory to resolve it from. Answering "" would be worse
		// than any guess: "" is the working directory, so the caller joining
		// "layers" onto it fills the user's checkout instead (E965's neighbour
		// in Native.root).
		cache = os.TempDir()
	}

	f.Store = filepath.Join(cache, "earthbuild", "fc-store")

	return f.Store
}

// NetBytes says how much the guest's network has carried.
//
// For the stall note: a build that has stopped making progress reads very
// differently depending on whether its network is still moving. Aggregate
// rather than per-connection, because that is what the stack exposes - and it
// is enough to separate a slow fetch from a dead one.
func (f *Firecracker) NetBytes() (sent, received uint64) {
	if f.own == nil || f.own.net == nil {
		return 0, 0
	}

	return f.own.net.BytesSent(), f.own.net.BytesReceived()
}

// consolePath is where this sandbox's guest writes its console.
//
// **In the sandbox's own directory, because a console shared is a console that
// lies.** It was in the store directory - one fixed path for the machine - and
// `os.Create` truncates, so every guest a build started wrote over the previous
// one's account of itself. A run that started thirty-two guests in sequence
// kept one file, and a failure quoting it attributed one guest's last words to
// another. The symptom was a console reading "earth-vmboot: ready" beneath a
// guest that had just failed its handshake.
//
// The trade is that it goes when the sandbox does, where the old path outlived
// it. Acceptable now that a failure carries the tail in its own text: the file
// was only ever read to explain a failure, and the explanation now travels with
// the failure instead of waiting in a directory for somebody to think of it.
func (f *Firecracker) consolePath() (string, error) {
	dir, err := f.dir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "console.log"), nil
}

// ConsoleTail is the end of the guest's console, for a failure to quote.
//
// The guest's own account of itself. A microVM that boots, announces itself
// ready and then does not answer has said why here and nowhere else - the
// engine sees only a connection that went unanswered.
func (f *Firecracker) ConsoleTail() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.console == nil {
		return ""
	}

	return consoleTail(f.console.Name())
}

// OwnAgent says this sandbox brings its own agent.
//
// The agent is built into the initramfs by `tools/mkguest`, so `$EARTH_GUESTD`
// and the binary beside the engine are files this backend never opens. Saying
// so keeps the staleness note off a run it cannot describe - see guestNoteFor.
func (f *Firecracker) OwnAgent() bool { return true }

// EnvDurableStore makes the guest's drives honour its flushes.
//
// **Off by default, because a layer store is a cache.** Firecracker's `Unsafe`
// cache does not pass a flush to the host, which is faster and safe so long as
// the guest unmounts before the VMM stops: the writes have already been issued,
// and it is the ordering the flush would impose that is lost. A guest killed
// mid-write leaves metadata half-old and half-new, which XFS reports as
// `structure needs cleaning` rather than replaying.
//
// The cure for that is a clean unmount and recovery when there was not one, not
// a journal flush on every write of a cache that can be rebuilt. This exists
// for somebody who would rather have the guarantee than the speed - a shared
// machine, or a store expensive enough to refill that losing it beats the
// write cost.
const EnvDurableStore = "EARTH_VM_DURABLE_STORE"

// driveCache is the cache mode for the guest's drives.
func driveCache() string {
	switch os.Getenv(EnvDurableStore) {
	case "", "0", "false", "no":
		return "Unsafe"
	default:
		return "Writeback"
	}
}

// CPUs is how many processors a step actually has.
//
// **Asked, because the host's core count is the wrong number here.** The guest
// is given four vCPUs by default and the machine starting it may have
// thirty-two; a build that runs one step per host core then puts thirty-two of
// them inside this. See EnvVMCPUs.
func (f *Firecracker) CPUs() int { return orDefault(f.VCPUs, defaultCPUs()) }

// Confines reports that a step's writes are held to its own layer.
func (f *Firecracker) Confines() bool { return true }

// Start boots the guest and returns the protocol connection to its agent.
func (f *Firecracker) Start(ctx context.Context) (_ Conn, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.cmd != nil {
		return nil, fmt.Errorf("this sandbox is already running")
	}

	dir, err := f.dir()
	if err != nil {
		return nil, err
	}

	cfg := filepath.Join(dir, "vm.json")
	vsock := filepath.Join(dir, "guest.vsock")
	f.vsockAt = vsock

	// **Before anything is written**, because the claim is what says this
	// machine is not already running a guest on that device - and the export
	// device beside it belongs to the same sandbox.
	f.release, err = claimStore(f.StoreImage)
	if err != nil {
		return nil, err
	}

	// **Given back on every failure from here on, because a Start that fails
	// leaves nothing for Stop to be called on.** The claim is taken before the
	// machine exists - which is the point of it - so the paths that give up
	// between here and a running VMM used to return holding the device for the
	// rest of the process's life. Two of them stopped the sandbox and released
	// it; five did not, and a single corpus run in a single process was refused
	// 25 times with `in use by this build itself`.
	//
	// A deferred release rather than one before each return: the next failure
	// added between here and there gets it too, which is exactly how the five
	// came to be missing it.
	defer func() {
		if err != nil {
			f.releaseStore()
		}
	}()

	err = f.makeExportDevice(dir)
	if err != nil {
		return nil, err
	}

	f.attachNet()

	err = f.writeConfig(cfg, vsock)
	if err != nil {
		return nil, err
	}

	argv := []string{f.Binary, "--no-api", "--config-file", cfg,
		"--api-sock", filepath.Join(dir, "fc.sock")}

	// **Through the shim when the engine provides the network itself**, which
	// is the default: the VMM has to run inside a network namespace that has a
	// tap in it, and neither the namespace nor the tap can be made by a process
	// that is already running. See NetShimMain.
	//
	// Not CommandContext: the context ends the *build*, and a VM killed by it
	// dies before Stop can take its sockets away. Stop is the only thing that
	// ends this process.
	//nolint:gosec,noctx // the argv is this package's; the context reason is above
	cmd := osexec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var back *net.UnixConn

	if f.ownNet {
		var shimErr error

		back, shimErr = f.throughNetShim(cmd, argv)
		if shimErr != nil {
			return nil, shimErr
		}

		// Where this machine answers requests for a socket on its tap, for a
		// build that finds it already running. See NetFDCommand.
		cmd.Env = append(os.Environ(), EnvNetFDs+"="+netFDAt(dir))
	}

	// **The guest's console goes to a file, not to the terminal.** It is where
	// earth-vmboot and the kernel report a boot that does not reach the agent,
	// so it cannot be discarded - and it is three hundred lines of kernel
	// initialisation and unimplemented-port complaints per build, so it cannot
	// go where the author is reading either. Kept, named when something goes
	// wrong, and quiet when nothing does.
	// In the store rather than beside the sockets, because the sockets go when
	// the sandbox stops and the account of a failed boot is worth reading after
	// the build that failed.
	at, err := f.consolePath()
	if err != nil {
		return nil, fmt.Errorf("make room for the guest's console: %w", err)
	}

	console, err := os.Create(at)
	if err != nil {
		return nil, fmt.Errorf("make room for the guest's console: %w", err)
	}

	f.console = console
	cmd.Stdout, cmd.Stderr = console, console

	err = cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("start firecracker: %w", err)
	}

	f.cmd = cmd
	f.gone = make(chan struct{})

	if back != nil {
		err = f.takeNetFrom(back)
		if err != nil {
			_ = f.stopLocked()

			return nil, err
		}
	}

	// **Waited for, so a VMM that stops is noticed.** Nothing else reaps it -
	// Stop kills the process group - and without this a guest that reset itself
	// leaves every dial to time out rather than to answer at once.
	go func(gone chan struct{}) {
		_ = cmd.Wait()

		close(gone)
	}(f.gone)

	conn, err := f.dialGuest(ctx, vsock)
	if err == nil {
		f.conn = conn
	}

	if err != nil {
		// The console is the only account of why: a guest that cannot mount its
		// store says so there and nowhere else, and without this the failure is
		// a connection that never arrived and no reason anywhere.
		why := fmt.Errorf("%w%s", err, consoleTail(console.Name()))

		// **A store that did not come back from an unclean stop is a state, not
		// a mystery.** Every guest after the first will refuse the same device,
		// so a build that does not recognise it fails identically for ever. See
		// storeUnmountable, which reads the guest's own words.
		if storeUnmountable(why) {
			why = fmt.Errorf("%w%s", why, brokenStoreHint(f.StoreImage))
		}

		_ = f.stopLocked()

		return nil, why
	}

	return conn, nil
}

// consoleTail is the end of the guest's console, for a failure to quote.
//
// **The end, and a bounded amount of it.** The start is the kernel finding its
// devices, which is the same every time; whatever went wrong is last. A guest
// that failed early may also have written nothing at all, and saying so is
// better than an empty quotation.
func consoleTail(at string) string {
	b, err := os.ReadFile(at) //nolint:gosec // a path this package made
	if err != nil {
		return "\n  its console could not be read: " + err.Error()
	}

	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return "\n  its console at " + at + " is empty, so it did not reach its own first line"
	}

	// **The guest's own last word first.** Resetting the machine prints another
	// dozen kernel lines after `earth-vmboot` has explained itself, so a plain
	// tail shows memory being freed and not the mount that failed. The kernel's
	// lines are context; this one is the diagnosis.
	out := ""

	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], guestSays) {
			out = "\n  the guest said: " + strings.TrimSpace(lines[i])

			break
		}
	}

	if len(lines) > consoleLines {
		lines = lines[len(lines)-consoleLines:]
	}

	return out + "\n  the last of its console (" + at + "):\n    " +
		strings.Join(lines, "\n    ")
}

// guestSays is how PID 1 prefixes its own lines, which is what separates the
// guest's diagnosis from the kernel's running commentary.
const guestSays = "earth-vmboot:"

// consoleLines is how much of the console a failure quotes. Enough for a mount
// failure and its context, short of pasting a kernel boot into an error.
const consoleLines = 20

// guestBootArgs is the kernel command line every guest boots with.
//
// **`transparent_hugepage=always`, because the kernel we build defaults to
// madvise and nothing madvises.** The config firecracker publishes sets
// CONFIG_TRANSPARENT_HUGEPAGE_MADVISE, so a guest process is given 2 MiB pages
// only if it asks - and a Go compiler, which is what this engine spends its
// time running, never does. Every allocation it makes is then backed by 4 KiB
// pages, and every TLB miss walks a full page table inside a guest whose walks
// are themselves nested.
//
// That is where the measurements point. Against the namespace backend on one
// box and one build: a tight CPU loop at parity, reading files at parity, and
// two thousand process creations 21% slower in the guest - the penalty lands
// exactly where page tables are walked and nowhere else.
//
// This asks nothing of the machine the build runs on, which is the point of
// choosing it over the alternatives: the host's own THP mode is global and
// needs root, and hugetlbfs needs a pool reserved with root that no other
// process can then use. The kernel command line is ours.
//
// **Restored on its own, and not to be carried off again.** It was written
// beside a microVM reuse experiment and went out with the revert of it, sharing
// none of its mechanism and none of its measurements. E978 says so at length,
// because the test below is the mechanical guard and a wholesale revert takes
// the test with it.
func guestBootArgs(net, settings string) string {
	return strings.TrimSpace(strings.Join([]string{
		"console=ttyS0", "loglevel=5", "reboot=k", "panic=1", "pci=off",
		"transparent_hugepage=always",
		net, settings,
	}, " "))
}

// writeConfig states the whole machine in one file, which is what `--no-api`
// takes.
//
// Firecracker's schema requires `drives` even when empty, and rejects a
// configuration missing it with a message about JSON rather than about devices.
func (f *Firecracker) writeConfig(at, vsock string) error {
	type object map[string]any

	cfg := object{
		"boot-source": object{
			"kernel_image_path": f.Kernel,
			"initrd_path":       f.Initrd,
			// `pci=off` because there is no PCI bus; the console is the only way
			// a guest that fails early can say so. The network settings ride
			// here because the command line is the only channel into a guest
			// that exists before the guest is running - see vmboot.Net.
			// `loglevel=5` prints errors and warnings and drops the
			// informational chatter, which is three hundred lines of a guest
			// enumerating its own hardware. It is not only noise: the kernel
			// and PID 1 write to one serial port, so a printk lands *inside*
			// the guest's own line - `earth-vmboot: mo[ 0.359858] kvm-guest:
			// ...` - and the one message a reader came for arrives cut in half.
			// XFS reports a bad superblock at warning level, so what matters
			// still comes through.
			"boot_args": guestBootArgs(f.net.BootArgs(),
				vmboot.EncodeEnv(guestSettings())),
		},
		"drives": []object{},
		"vsock": object{
			"guest_cid": guestCID,
			"uds_path":  vsock,
		},
		"machine-config": object{
			"vcpu_count":   f.CPUs(),
			"mem_size_mib": orDefault(f.MemoryMiB, defaultMemory()),
		},
	}

	drives := []object{}

	// **Order is the device order**: the guest names them `/dev/vda`, `/dev/vdb`
	// in the order they appear here, and `vmboot` mounts the first as its store
	// and writes exports to the second. A configuration listing only the export
	// device would have the guest format it as a store, which is a build that
	// destroys the thing it was about to hand back.
	if f.StoreImage != "" {
		drives = append(drives, object{
			"drive_id":       "store",
			"path_on_host":   f.StoreImage,
			"is_root_device": false,
			"is_read_only":   false,
			// **Fast by default, because a layer store is a cache.** See
			// EnvDurableStore: the flush a journal relies on is what makes a
			// kill survivable, and the answer is to unmount cleanly on the way
			// out rather than to pay for durability on every write.
			"cache_type": driveCache(),
			// **io_uring rather than a thread per request.** Firecracker's
			// default block engine is Sync, which serves each request on a host
			// thread; Async submits through io_uring. The difference lands
			// where this backend spends real time - the export phase is 0.504s
			// of a 3.6s hot build and is mostly writing an artifact through
			// this device.
			//
			// Host-side and guest-transparent: the guest sees an ordinary block
			// device either way, so no kernel config and nothing in the agent
			// has an opinion about it.
			"io_engine": "Async",
		})

		drives = append(drives, object{
			"drive_id":       "exports",
			"path_on_host":   f.exports,
			"is_root_device": false,
			"is_read_only":   false,
			// The same, for the same reason: a drive whose durability depends
			// on which one it is is a drive somebody will get wrong.
			"cache_type": driveCache(),
			"io_engine":  "Async",
		})
	}

	cfg["drives"] = drives

	// **An entropy device, because Firecracker gives a guest none unless asked.**
	// Its own documentation states the consequence: applications block on
	// `/dev/random` or `getrandom(2)`, or take what they need before the pool is
	// seeded and generate weak key material. A build fetches over TLS on almost
	// every step, so this is not a corner of the workload.
	//
	// No rate limiter. One is available and is for a host running many guests
	// that might starve each other; this host runs one per build.
	cfg["entropy"] = object{}

	// **Only where there is a tap.** A guest with an interface and no peer is
	// slower to fail than one with no interface: it waits out a connect timeout
	// per fetch rather than saying at once that nothing resolves.
	if f.net.Wanted() {
		cfg["network-interfaces"] = []object{{
			"iface_id":      "eth0",
			"host_dev_name": f.tap,
			"guest_mac":     vmboot.MACFor(f.net.Address.Addr()),
		}}
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("build the machine configuration: %w", err)
	}

	err = os.WriteFile(at, b, 0o600)
	if err != nil {
		return fmt.Errorf("write the machine configuration: %w", err)
	}

	return nil
}

func orDefault(v, fallback int) int {
	if v <= 0 {
		return fallback
	}

	return v
}

// dialGuest connects to the agent through Firecracker's vsock multiplexer.
//
// The host end is a unix socket on which one writes `CONNECT <port>` and reads
// `OK <assigned>`; everything after that line is the guest's stream. Retried
// because the socket appears when the VMM starts and the *agent* binds its port
// a moment later - a connection made in between is refused, and refusing to
// retry would make a working guest look broken.
func (f *Firecracker) dialGuest(ctx context.Context, vsock string) (Conn, error) {
	return dialPort(ctx, vsock, vmboot.VsockPort, f.gone)
}

// dialPort retries until the guest answers, the deadline passes, or the VMM
// stops.
//
// **`gone` is what turns a thirty-second wait into an immediate answer.** A
// guest that cannot mount its store says so and resets, Firecracker exits, and
// every dial after that is `connection refused` - a verdict, not a "not yet".
// Retrying it to the deadline delays the diagnosis the guest has already
// written by half a minute.
func dialPort(ctx context.Context, vsock string, port uint32, gone <-chan struct{}) (Conn, error) {
	deadline := time.Now().Add(guestBootTimeout)

	var last error

	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		select {
		case <-gone:
			return nil, fmt.Errorf("the guest stopped before it answered on vsock port %d: %w",
				port, last)
		default:
		}

		conn, err := tryDial(vsock, port)
		if err == nil {
			return conn, nil
		}

		last = err

		time.Sleep(guestPollInterval)
	}

	return nil, fmt.Errorf("the guest did not answer on vsock port %d within %s: %w",
		port, guestBootTimeout, last)
}

const (
	guestBootTimeout  = 30 * time.Second
	guestPollInterval = 20 * time.Millisecond
)

func tryDial(vsock string, port uint32) (Conn, error) {
	c, err := net.Dial("unix", vsock)
	if err != nil {
		return nil, err
	}

	_, err = fmt.Fprintf(c, "CONNECT %d\n", port)
	if err != nil {
		_ = c.Close()

		return nil, err
	}

	// One line, read a byte at a time: anything buffered past the newline is the
	// guest's own stream, and a bufio.Reader would swallow it.
	greeting, err := readLine(c)
	if err != nil {
		_ = c.Close()

		return nil, err
	}

	if len(greeting) < 2 || greeting[:2] != "OK" {
		_ = c.Close()

		return nil, fmt.Errorf("firecracker refused the vsock connection: %q", greeting)
	}

	return c, nil
}

// greetingPatience bounds the wait for `OK <n>`.
//
// **A dial that succeeds is not a guest that is there.** Firecracker's
// multiplexer socket exists for as long as the VMM process does, and the VMM
// outlives a guest that halted - so a panicked guest leaves a socket that
// accepts connections and answers nothing. Without this the read blocks for
// ever and the retry loop's own deadline never gets a turn: a guest handed an
// unformatted store device printed a perfectly good diagnosis and then hung the
// build that could have shown it.
//
// Short, because the answer comes from the VMM rather than from the guest: it
// is a local socket write and a local read, not a boot.
const greetingPatience = 2 * time.Second

// readLine reads one line without reading past it. See tryDial.
func readLine(c net.Conn) (string, error) {
	// Cleared before returning, so the caller's connection is left as it was
	// found: this is the protocol channel, and a deadline left on it would
	// expire in the middle of somebody's build.
	err := c.SetReadDeadline(time.Now().Add(greetingPatience))
	if err != nil {
		return "", fmt.Errorf("set a deadline on the guest's greeting: %w", err)
	}

	defer func() { _ = c.SetReadDeadline(time.Time{}) }()

	return readLineNow(c)
}

func readLineNow(c net.Conn) (string, error) {
	var (
		out [64]byte
		n   int
	)

	for n < len(out) {
		_, err := c.Read(out[n : n+1])
		if err != nil {
			return string(out[:n]), err
		}

		if out[n] == '\n' {
			return string(out[:n]), nil
		}

		n++
	}

	return string(out[:n]), fmt.Errorf("no newline in the first %d bytes", len(out))
}

// releaseStore gives the store device back, once and safely twice.
//
// Called both from the deferred failure path in Start and from stopLocked, and
// those overlap: a Start that stops the sandbox on its way out releases there
// and returns an error, and the defer then runs. Idempotent, so the second call
// is the no-op it should be rather than a double close.
func (f *Firecracker) releaseStore() {
	if f.release == nil {
		return
	}

	f.release()
	f.release = nil
}

// Stop ends the guest and removes what this sandbox made.
func (f *Firecracker) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.stopLocked()
}

func (f *Firecracker) stopLocked() error {
	if f.stopped {
		return nil
	}

	f.stopped = true

	f.releaseStore()

	// Cleared here so a PlaceBlob racing a Stop is refused with "not running"
	// rather than dialling a socket that is about to be removed - which fails
	// as ECONNREFUSED, retries for thirty seconds, and reports a boot timeout
	// for a guest that was deliberately stopped.
	f.vsockAt = ""

	if f.cmd != nil && f.cmd.Process != nil {
		f.shutDownLocked()

		// The group, not the leader: a VMM leaves helpers behind, and a signal
		// to the leader alone leaves them holding the sockets open.
		//
		// A backstop now rather than the method: `shutDownLocked` has usually
		// left nothing to kill, and a signal to a process that has gone is
		// harmless.
		_ = syscall.Kill(-f.cmd.Process.Pid, syscall.SIGKILL)
	}

	if f.own != nil {
		f.own.Close()
		f.own = nil
	}

	if f.console != nil {
		_ = f.console.Close()
		f.console = nil
	}

	if f.unlock != nil {
		f.unlock()
		f.unlock = nil
	}

	if f.tmp != "" {
		err := os.RemoveAll(f.tmp)
		if err != nil {
			return fmt.Errorf("remove the sandbox directory: %w", err)
		}
	}

	return nil
}

// dir is where this sandbox keeps its sockets, made on first use.
func (f *Firecracker) dir() (string, error) {
	if f.Root != "" {
		return f.Root, os.MkdirAll(f.Root, 0o755)
	}

	if f.tmp != "" {
		return f.tmp, nil
	}

	// **Before making one of our own**, so a build cleans up after the builds
	// that were killed before it. Nothing else ever removed these: every
	// ordinary path is tidy and a SIGKILL runs none of them. See
	// sweepSandboxes, which uses a lock rather than age so a running build's
	// directory is never taken.
	sweepSandboxes(os.TempDir())

	// **Short, because a unix socket path is 108 bytes** and the API socket, the
	// vsock and the guest's own suffix all hang off this. A path under a long
	// temporary directory binds nothing and reports it as an invalid argument.
	tmp, err := os.MkdirTemp("", sandboxPrefix)
	if err != nil {
		return "", fmt.Errorf("make a directory for the sandbox: %w", err)
	}

	// Held for as long as this process lives, which is what tells the next
	// build's sweep that this one is not abandoned.
	release, err := holdSandbox(tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)

		return "", fmt.Errorf("claim the sandbox directory %s: %w", tmp, err)
	}

	f.unlock = release
	f.tmp = tmp

	return tmp, nil
}

// PlaceBlob makes a blob on this machine readable by the guest, and says where
// the guest will find it.
//
// **Copied rather than shared, because there is nothing to share.** Firecracker
// has no virtio-fs, so a host path means nothing inside the guest; the bytes go
// over a vsock channel of their own and land on the device the guest formatted.
// A sandbox that *can* share a filesystem implements `GuestPath` instead and
// copies nothing, which is why both exist.
//
// The credential stays here, which is the reason the host pushes rather than the
// guest fetching: `engine/image` reads the machine's credential store, and a
// guest that fetched for itself would put a registry password inside the
// boundary this sandbox exists to be.
func (f *Firecracker) PlaceBlob(ctx context.Context, host string) (string, error) {
	fi, err := os.Stat(host)
	if err != nil {
		return "", fmt.Errorf("read the blob at %s: %w", host, err)
	}

	src, err := os.Open(host)
	if err != nil {
		return "", fmt.Errorf("open the blob at %s: %w", host, err)
	}

	defer func() { _ = src.Close() }()

	f.mu.Lock()
	vsock := f.vsockAt
	f.mu.Unlock()

	if vsock == "" {
		return "", fmt.Errorf("this sandbox is not running, so %s cannot be"+
			" placed in it", filepath.Base(host))
	}

	// A connection per blob. The build fetches an image's layers at once and
	// each gets its own, which is what makes them independent - one that fails
	// mid-blob closes its own channel and leaves the others alone. Firecracker
	// multiplexes them onto the one device.
	conn, err := dialPort(ctx, vsock, vmboot.BulkPort, f.gone)
	if err != nil {
		return "", fmt.Errorf("open a bulk channel for %s: %w", filepath.Base(host), err)
	}

	defer func() { _ = conn.Close() }()

	name := filepath.Base(host)

	err = bulk.SendBlob(conn, name, src, fi.Size())
	if err != nil {
		return "", err
	}

	// **Closed before returning, because the guest writes on end of stream.**
	// The receiver renames the blob into place when the channel ends, so a
	// caller told the path while the connection is still open would look for a
	// file that is still a temporary one.
	err = conn.Close()
	if err != nil {
		return "", fmt.Errorf("finish the bulk channel for %s: %w", name, err)
	}

	return f.GuestBlob(name), nil
}

// GuestBlob is where the guest sees a blob this sandbox placed.
//
// **The guest's store, never this machine's.** The two are different
// directories here and only here: the host's holds blobs, the action cache and
// staged exports, and the guest's is a block device the host cannot open at
// all. What this returns is handed straight to the guest as the path to unpack,
// so naming the host's store produced a guest reporting `no such file or
// directory` for bytes it was holding.
func (f *Firecracker) GuestBlob(name string) string {
	return path.Join(vmboot.StoreAt, "blobs", name)
}

// exportSize is how large the export device is made.
//
// **Sparse, so the number is a ceiling and not a cost**: the file occupies what
// is written to it. It bounds one artifact rather than a build's worth, because
// the device is rewritten from its start for each.
const exportSize = 64 << 30

// makeExportDevice creates the device an artifact leaves the guest on.
//
// Beside the sockets, and remade for each sandbox: it holds one artifact at a
// time and nothing about it is worth keeping between runs.
func (f *Firecracker) makeExportDevice(dir string) error {
	at := filepath.Join(dir, "exports.img")

	dev, err := os.OpenFile(at, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("make the export device: %w", err)
	}

	defer func() { _ = dev.Close() }()

	err = dev.Truncate(exportSize)
	if err != nil {
		return fmt.Errorf("size the export device: %w", err)
	}

	f.exports = at

	return nil
}

// FetchExport brings a staged artifact out of the guest, into a directory on
// this machine.
//
// **The path goes down a socket and the artifact comes back on a device.** They
// are two different things: a question small enough to be a line, and an answer
// that may be gigabytes. The device carries a tar stream rather than a
// filesystem, so nothing on this side mounts metadata a sandbox wrote - see
// vmboot.ExportDev.
func (f *Firecracker) FetchExport(ctx context.Context, guestPath, into string) error {
	f.exporting.Lock()
	defer f.exporting.Unlock()

	f.mu.Lock()
	vsock, dev := f.vsockAt, f.exports
	f.mu.Unlock()

	if vsock == "" {
		return fmt.Errorf("this sandbox is not running, so %s cannot be"+
			" fetched from it", guestPath)
	}

	n, err := f.askForExport(ctx, vsock, guestPath)
	if err != nil {
		return err
	}

	src, err := os.Open(dev)
	if err != nil {
		return fmt.Errorf("read the export device: %w", err)
	}

	defer func() { _ = src.Close() }()

	err = bulk.UnpackTree(io.LimitReader(src, n), into)
	if err != nil {
		return fmt.Errorf("unpack %s from the export device: %w", guestPath, err)
	}

	return nil
}

// askForExport tells the guest what to write and reads how much it wrote.
func (f *Firecracker) askForExport(ctx context.Context, vsock, guestPath string) (int64, error) {
	conn, err := dialPort(ctx, vsock, vmboot.ExportPort, f.gone)
	if err != nil {
		return 0, fmt.Errorf("open the export channel for %s: %w", guestPath, err)
	}

	defer func() { _ = conn.Close() }()

	_, err = fmt.Fprintf(conn, "%s\n", guestPath)
	if err != nil {
		return 0, fmt.Errorf("ask for %s: %w", guestPath, err)
	}

	line, err := readLine(conn.(net.Conn))
	if err != nil {
		return 0, fmt.Errorf("the guest did not answer for %s: %w", guestPath, err)
	}

	if !strings.HasPrefix(line, "OK ") {
		return 0, fmt.Errorf("the guest could not stage %s: %s",
			guestPath, strings.TrimPrefix(line, "ERR "))
	}

	n, err := strconv.ParseInt(strings.TrimPrefix(line, "OK "), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("the guest answered %q for %s, which is not a size",
			line, guestPath)
	}

	return n, nil
}

// GuestStore is where the guest keeps what this sandbox asks it about.
//
// The other half of E971: `StoreDir` is this machine's and the guest cannot open
// it, so anything named *to* the guest - a placed blob, a staged export - is
// named from here.
func (f *Firecracker) GuestStore() string { return vmboot.StoreAt }

// attachNet works out how this guest reaches the network, and says once when it
// cannot.
//
// **Said once, at start, rather than left to a step.** A sandbox with no route
// still builds everything that does not fetch; what it must not do is let a
// `RUN apk add` fail on a name that will not resolve, which reads as a broken
// mirror rather than as a machine with no network.
func (f *Firecracker) attachNet() {
	asked := os.Getenv(EnvTap)

	// **The default is a network the engine makes itself**, with no privilege
	// and nothing installed: a user namespace of its own, a tap inside it, and
	// a userspace TCP/IP stack on this side of it. See userNet.
	if asked == "" {
		f.ownNet, f.tap = true, tapName
		f.net = (&userNet{}).guestConfig()

		return
	}

	if strings.EqualFold(asked, "off") {
		return
	}

	// A device somebody made as root, for a machine that wants the guest on its
	// real network rather than behind a stack in this process.
	f.tap = asked

	net, why := guestNet()
	if why != "" {
		fmt.Fprintf(os.Stderr, "earthbuild: this microVM has no network: %s\n"+
			"  steps that fetch will fail; set %s=off to stop being told\n", why, EnvTap)

		return
	}

	f.net = net
}

// shutDownLocked asks the guest to stop, and waits for it.
//
// **Killing the VMM loses the store.** A guest's writes live in its own page
// cache until something flushes them, and SIGKILL to Firecracker takes the
// machine away mid-flight: the layers this build unpacked never reach the
// device, and the next build asks whether the store holds them, is told no, and
// rebuilds everything. That is not a slow cache, it is no cache at all - every
// step of every microVM build missed, for exactly this reason.
//
// Closing the agent's connection is the whole mechanism. The agent reads its
// protocol from stdin, so end-of-stream ends it; `earth-vmboot` runs the agent
// rather than exec'ing it, so it regains control, halts, and the kernel syncs
// its filesystems on the way down. Firecracker exits when the guest resets.
//
// Bounded, because a guest that will not stop must not hold a build open: past
// the wait the caller's SIGKILL takes it, and the store is then as good as it
// was before this existed.
func (f *Firecracker) shutDownLocked() {
	if f.conn != nil {
		_ = f.conn.Close()
		f.conn = nil
	}

	if f.gone == nil {
		return
	}

	select {
	case <-f.gone:
	case <-time.After(shutdownPatience):
		fmt.Fprintf(os.Stderr, "earthbuild: the microVM did not stop within %s,"+
			" so it is being killed\n"+
			"  what it had not yet written to its store is lost, and the next"+
			" build will rebuild it\n", shutdownPatience)
	}
}

// shutdownPatience is how long a guest gets to flush and halt. An unmount of a
// journalled filesystem holding a build's worth of layers, not a boot.
const shutdownPatience = 10 * time.Second

// Full explains a failure that ran out of room on this sandbox's store device.
//
// Answered by the sandbox because only it knows the store is a device at all:
// the executor sees an ENOSPC from a guest and has no way to tell a directory
// somebody can make room in from an image somebody has to remake.
func (f *Firecracker) Full(err error) string { return vmFullHint(err, f.StoreImage) }

// EnvVMCPUs and EnvVMMemory size the guest.
//
// **Because the defaults are one machine's guess about another's work.** Four
// vCPUs and two gigabytes run a step comfortably and a build of several at once
// not at all - and the parallelism follows the vCPUs, so raising one raises
// both. A machine with cores to spare should say so.
const (
	EnvVMCPUs   = "EARTH_VM_CPUS"
	EnvVMMemory = "EARTH_VM_MEMORY_MIB"
)

// envInt is a setting as a number, or zero for absent and for anything that is
// not one. A typo bounds how fast the build goes and nothing about what it
// produces, so it takes the default rather than stopping the build.
func envInt(name string) int {
	n, err := strconv.Atoi(os.Getenv(name))
	if err != nil || n <= 0 {
		return 0
	}

	return n
}

// WriteConfigForTest writes the machine configuration a test wants to inspect.
//
// **Exported for a test rather than tested through a running guest**, because
// the properties that matter here are ones a working guest cannot demonstrate:
// a machine with no entropy device still boots, and a key generated without
// seeded randomness looks exactly like a key.
func (f *Firecracker) WriteConfigForTest(at, vsock string) error { return f.writeConfig(at, vsock) }
