//go:build linux

package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	osexec "os/exec"
	"path"
	"path/filepath"
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
	// the guest keeps reflinks on a host that may have none.
	StoreImage string

	// Root is where this sandbox keeps its sockets. A temporary directory when
	// empty.
	Root string

	// Store is where the *host* keeps this sandbox's blobs, action cache and
	// staged exports. Not the guest's layers, which are on StoreImage: the two
	// are one directory only where a filesystem is shared, and none is here.
	Store string

	// VCPUs and MemoryMiB size the guest. Zero takes the defaults below.
	VCPUs     int
	MemoryMiB int

	mu      sync.Mutex
	cmd     *osexec.Cmd
	tmp     string
	vsockAt string
	stopped bool
}

// Defaults sized to run one step rather than to be generous: a VM is per worker,
// so these are paid once, but a guest that swaps is slower than no VM at all.
const (
	defaultVCPUs     = 4
	defaultMemoryMiB = 2048

	// guestCID is the guest's vsock address. 2 is the host and 0-2 are
	// reserved, so 3 is the first a guest may have.
	guestCID = 3
)

// NewFirecracker returns a sandbox with the defaults and the environment's
// overrides.
func NewFirecracker() *Firecracker {
	return &Firecracker{
		Binary:     envOr("EARTH_FIRECRACKER", "firecracker"),
		Kernel:     os.Getenv("EARTH_VM_KERNEL"),
		Initrd:     os.Getenv("EARTH_VM_INITRD"),
		StoreImage: os.Getenv("EARTH_VM_STORE"),
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

// Confines reports that a step's writes are held to its own layer.
func (f *Firecracker) Confines() bool { return true }

// Start boots the guest and returns the protocol connection to its agent.
func (f *Firecracker) Start(ctx context.Context) (Conn, error) {
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

	err = f.writeConfig(cfg, vsock)
	if err != nil {
		return nil, err
	}

	// Not CommandContext: the context ends the *build*, and a VM killed by it
	// dies before Stop can take its sockets away. Stop is the only thing that
	// ends this process.
	//nolint:gosec,noctx // the argv is this package's; the context reason is above
	cmd := osexec.Command(f.Binary, "--no-api", "--config-file", cfg,
		"--api-sock", filepath.Join(dir, "fc.sock"))

	// The guest's console, which is where earth-vmboot and the kernel report a
	// boot that does not reach the agent. Without it a failure to mount the
	// store is a connection that never arrives and no reason anywhere.
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	err = cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("start firecracker: %w", err)
	}

	f.cmd = cmd

	conn, err := dialGuest(ctx, vsock)
	if err != nil {
		_ = f.stopLocked()

		return nil, err
	}

	return conn, nil
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
			// a guest that fails early can say so.
			"boot_args": "console=ttyS0 reboot=k panic=1 pci=off",
		},
		"drives": []object{},
		"vsock": object{
			"guest_cid": guestCID,
			"uds_path":  vsock,
		},
		"machine-config": object{
			"vcpu_count":   orDefault(f.VCPUs, defaultVCPUs),
			"mem_size_mib": orDefault(f.MemoryMiB, defaultMemoryMiB),
		},
	}

	if f.StoreImage != "" {
		cfg["drives"] = []object{{
			"drive_id":       "store",
			"path_on_host":   f.StoreImage,
			"is_root_device": false,
			"is_read_only":   false,
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
func dialGuest(ctx context.Context, vsock string) (Conn, error) {
	return dialPort(ctx, vsock, vmboot.VsockPort)
}

func dialPort(ctx context.Context, vsock string, port uint32) (Conn, error) {
	deadline := time.Now().Add(guestBootTimeout)

	var last error

	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		conn, err := tryDial(vsock, port)
		if err == nil {
			return conn, nil
		}

		last = err

		time.Sleep(guestPollInterval)
	}

	return nil, fmt.Errorf("the guest did not answer on vsock port %d within %s: %w"+
		"\n  its console is on this process's stderr, and a guest that cannot"+
		" mount its store says so there", port, guestBootTimeout, last)
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

// readLine reads one line without reading past it. See tryDial.
func readLine(c net.Conn) (string, error) {
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

	// Cleared here so a PlaceBlob racing a Stop is refused with "not running"
	// rather than dialling a socket that is about to be removed - which fails
	// as ECONNREFUSED, retries for thirty seconds, and reports a boot timeout
	// for a guest that was deliberately stopped.
	f.vsockAt = ""

	if f.cmd != nil && f.cmd.Process != nil {
		// The group, not the leader: a VMM leaves helpers behind, and a signal
		// to the leader alone leaves them holding the sockets open.
		_ = syscall.Kill(-f.cmd.Process.Pid, syscall.SIGKILL)
		_ = f.cmd.Wait()
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

	// **Short, because a unix socket path is 108 bytes** and the API socket, the
	// vsock and the guest's own suffix all hang off this. A path under a long
	// temporary directory binds nothing and reports it as an invalid argument.
	tmp, err := os.MkdirTemp("", "earth-fc")
	if err != nil {
		return "", fmt.Errorf("make a directory for the sandbox: %w", err)
	}

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
	conn, err := dialPort(ctx, vsock, vmboot.BulkPort)
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

	return path.Join(f.StoreDir(), "blobs", name), nil
}
