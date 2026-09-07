//go:build linux

// Command earth-vmboot is PID 1 inside a Firecracker microVM.
//
// It exists so `earth-guestd` needs no VM-specific code at all. The agent speaks
// its protocol over stdin and stdout; a microVM has neither, and Firecracker's
// only channel that is not the serial console is vsock. This prepares the guest,
// waits for the host on a fixed vsock port, and hands the accepted connection to
// the agent as its stdio.
//
// **Firecracker has no virtio-fs**, deliberately - its device model is block,
// net, vsock, balloon and rng. So the layer store arrives as a block device
// rather than a share, which is the better shape anyway: the guest formats it,
// and so has reflinks even where the host's own filesystem has none (E971).
package main

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/cmd/earth-vmboot/vmboot"
	"github.com/EarthBuild/earthbuild/engine/bulk"
	"golang.org/x/sys/unix"
)

// storeDev is the block device carrying the layer store, and storeAt is where
// the agent expects to find it.
// mounted records whether the store was actually mounted, so `halt` puts down
// what exists and stays quiet about what does not. A package-level value
// because PID 1 is one process doing one thing, and threading it through the
// two functions that care would be ceremony.
var mounted bool

const (
	storeDev = "/dev/vda"
	storeAt  = vmboot.StoreAt
)

func main() {
	err := run()
	if err != nil {
		// PID 1 returning panics the kernel, which reports the panic and not
		// the cause. Say the cause on the console the host is reading, then
		// stop deliberately.
		fmt.Fprintf(os.Stderr, "earth-vmboot: %v\n", err)
		halt()
	}

	halt()
}

func run() error {
	err := prepare()
	if err != nil {
		return err
	}

	saySettings()
	sayStore()

	err = writeResolver()
	if err != nil {
		// Not fatal: a guest with no resolver still builds everything that does
		// not fetch, and the host has already said whether it has a network.
		fmt.Fprintf(os.Stderr, "earth-vmboot: no resolver: %v\n", err)
	}

	// Started before the agent, because the host may push a blob before it
	// sends its first request: the two channels are independent and the host
	// has no way to know when this one is listening.
	go serveBulk(filepath.Join(storeAt, "blobs"))
	go serveExports()

	conn, err := waitForHost()
	if err != nil {
		return err
	}

	defer conn.Close()

	return serve(conn)
}

// prepare gives the guest the filesystems the agent assumes.
func prepare() error {
	for _, d := range []string{"/proc", "/sys", "/dev", "/tmp", storeAt} {
		err := os.MkdirAll(d, 0o755)
		if err != nil {
			return fmt.Errorf("make %s: %w", d, err)
		}
	}

	_ = unix.Mount("proc", "/proc", "proc", 0, "")
	_ = unix.Mount("sysfs", "/sys", "sysfs", 0, "")
	_ = unix.Mount("tmpfs", "/tmp", "tmpfs", 0, "")

	// **cgroup2, or nested runtimes cannot start.** The agent looks for
	// `/sys/fs/cgroup/cgroup.controllers` to decide whether a step may run a
	// daemon, and sysfs alone does not provide it: the guest reported "this
	// machine is not on cgroups v2" and `WITH DOCKER` was unavailable in every
	// microVM build.
	//
	// Best-effort, like the mounts above it: a guest that cannot mount this
	// still runs every step that is not a nested runtime, and the agent already
	// says which that is.
	_ = os.MkdirAll("/sys/fs/cgroup", 0o755)
	_ = unix.Mount("cgroup2", "/sys/fs/cgroup", "cgroup2", 0, "")

	// **Without devtmpfs there are no device nodes.** The kernel finds the disk
	// and logs `virtio_blk virtio0: [vda]`, but an initramfs has an empty /dev,
	// so mounting it fails with ENOENT - which reads as "wrong filesystem" and
	// is nothing of the kind. Two rounds were spent reformatting an image that
	// was never the problem.
	err := unix.Mount("devtmpfs", "/dev", "devtmpfs", 0, "")
	if err != nil {
		return fmt.Errorf("mount devtmpfs: %w", err)
	}

	err = unix.Mount(storeDev, storeAt, "xfs", 0, "")
	mounted = err == nil

	if err != nil {
		return fmt.Errorf("mount the layer store from %s: %w"+
			"\n  the host makes this device and formats it; an unformatted one"+
			" arrives here as an invalid argument", storeDev, err)
	}

	return nil
}

// listenVsock binds one vsock port and returns the listening descriptor.
func listenVsock(port uint32, backlog int) (int, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return -1, fmt.Errorf("vsock socket: %w", err)
	}

	err = unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port})
	if err != nil {
		return -1, fmt.Errorf("bind vsock port %d: %w", port, err)
	}

	err = unix.Listen(fd, backlog)
	if err != nil {
		return -1, fmt.Errorf("listen on vsock port %d: %w", port, err)
	}

	return fd, nil
}

// serveBulk takes blob bytes from the host for as long as the guest runs.
//
// **Here rather than in the agent**, because this is what mounted the device
// the blobs land on: the agent finds them afterwards by path, which is what it
// does on every backend that shares a filesystem with its host. Nothing about
// the agent knows a VM is involved, which is the point of this binary.
//
// Best-effort and loud: a guest that cannot take blobs still runs steps, and
// what it cannot do is fail silently - the symptom of that is a build that
// stops at its first FROM saying the store holds no layer, which is the failure
// this exists to fix and reads as an empty store rather than as a lost channel.
func serveBulk(at string) {
	fd, err := listenVsock(vmboot.BulkPort, bulkBacklog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth-vmboot: no bulk channel: %v\n", err)

		return
	}

	for {
		conn, _, err := unix.Accept(fd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "earth-vmboot: bulk accept: %v\n", err)

			return
		}

		// **One goroutine per connection, because the host opens one per
		// layer.** `materialiseImageInGuest` places every layer of an image at
		// once - that is the overlap the whole path exists for - so serving
		// them one at a time gives the sum where the design says the maximum,
		// and past the backlog the kernel starts refusing connections outright.
		// The blobs are independent: separate names, separate temporary files,
		// one directory.
		go func(c int) {
			f := os.NewFile(uintptr(c), "bulk")
			defer func() { _ = f.Close() }()

			n, recvErr := bulk.ReceiveBlobs(f, at)
			if recvErr != nil {
				fmt.Fprintf(os.Stderr, "earth-vmboot: bulk channel: %v\n", recvErr)
			}

			// **Said out loud, on the console the host is already reading.**
			// What arrived is the one fact that separates "the channel never
			// carried it" from "the store lost it", and without it the two look
			// identical from outside: a guest reporting `no such file` for a
			// blob the host believes it sent.
			fmt.Fprintf(os.Stderr, "earth-vmboot: %d blob(s) into %s\n", n, at)
		}(conn)
	}
}

// bulkBacklog is how many placements may be waiting to be served.
//
// A layer per connection and an image is rarely more than a dozen, so this is
// slack rather than a limit - the connections are served as they arrive. It
// matters only in the instant between the host dialling every layer at once and
// the accept loop getting round to them.
const bulkBacklog = 64

// waitForHost blocks until the host connects on the vsock port.
func waitForHost() (*os.File, error) {
	fd, err := listenVsock(vmboot.VsockPort, 1)
	if err != nil {
		return nil, err
	}

	// Said on the console before blocking, so a host whose connection never
	// arrives can tell "the guest is not ready" from "the guest is waiting".
	fmt.Println("earth-vmboot: ready")

	conn, _, err := unix.Accept(fd)
	if err != nil {
		return nil, fmt.Errorf("accept on vsock: %w", err)
	}

	return os.NewFile(uintptr(conn), "vsock"), nil
}

// serve runs the agent with the host's connection as its stdio.
//
// The agent lives in the initramfs beside this, which is what makes a guest one
// artefact rather than a machine somebody has to provision.
func serve(conn *os.File) error {
	const agent = "/earth-guestd"

	_, err := os.Stat(agent)
	if err != nil {
		return fmt.Errorf("no agent at %s: %w"+
			"\n  it is put in the initramfs beside this binary", agent, err)
	}

	cmd := osexec.Command(agent) //nolint:gosec // a fixed path in our own initramfs
	cmd.Env = agentEnv(os.Environ())
	cmd.Stdin, cmd.Stdout = conn, conn
	// Its diagnostics go to the console rather than down the protocol channel,
	// where they would be read as frames and desynchronise the stream.
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// putStoreDown unmounts the store, and says nothing when there was none.
//
// **Separate from `halt` because it must not be able to skip the reset.** It
// was an early `return` inside `halt`, which skipped the reboot below it: PID 1
// returned, and the kernel panicked with a backtrace in place of the diagnosis
// the guest had already written. A function that can only decline to unmount
// cannot decline to stop the machine.
//
// Silent where nothing was mounted, because that is the one path where the
// console is being read closely: a guest that could not mount its store has
// already said why, and `could not be unmounted: invalid argument` underneath
// reads as a second fault.
func putStoreDown() {
	if !mounted {
		return
	}

	err := unix.Unmount(storeAt, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth-vmboot: the store at %s could not be"+
			" unmounted: %v\n  what this build wrote may not be there for the"+
			" next one\n", storeAt, err)

		return
	}

	fmt.Fprintf(os.Stderr, "earth-vmboot: store unmounted\n")
}

// halt stops the guest rather than letting PID 1 return.
func halt() {
	_ = os.Stdout.Sync()

	// **Unmounted, not merely synced.** The store is a filesystem the *next*
	// boot has to read, and a build's layers are worth nothing if they are not
	// there when it looks: three consecutive builds each captured their work
	// and each found the same 161 layers waiting, because the machine went away
	// before the filesystem was put down.
	//
	// Said out loud either way. A store that could not be unmounted is a store
	// the next build may find short, and that is the difference between a slow
	// cache and a wrong one.
	unix.Sync()

	putStoreDown()

	// **Reset, not power-off.** With `pci=off` there is no ACPI to power the
	// machine down, so `POWER_OFF` falls through to `reboot: System halted` and
	// the VMM keeps running with a stopped guest inside it - a host dialling
	// that machine gets a socket that accepts and never answers. Firecracker
	// traps the i8042 reset and exits, which is the documented way to end a
	// microVM from within.
	_ = unix.Reboot(unix.LINUX_REBOOT_CMD_RESTART)

	// Reached only if the reset did nothing. Returning from PID 1 panics the
	// kernel, which reports the panic rather than the cause said above.
	select {}
}

// agentEnv is the environment the agent runs under.
//
// **The store has to be said.** The guest's own default is
// `/var/lib/earthbuild`, which inside a microVM is the initramfs - a tmpfs the
// size of the guest's memory, holding nothing, and thrown away with the
// machine. The store is the block device mounted at /store and nothing else
// tells the agent so.
//
// What the kernel passed in is kept, because boot arguments are the only way a
// setting reaches a guest at all: there is no shell here and no profile to read.
func agentEnv(boot []string) []string {
	out := append(append([]string{}, boot...), fromCmdline()...)

	// Last, so the store is this guest's own whatever anything else said: it is
	// a fact about this machine rather than a setting.
	return append(out, "EARTH_GUEST_ROOT="+storeAt)
}

// fromCmdline is the settings the host sent, which is every setting the guest
// has.
//
// **The kernel command line is the only channel that exists before the guest
// does.** Reading it here rather than in `run` so that `agentEnv` is the whole
// answer to "what does the agent see", and a test can ask.
func fromCmdline() []string {
	b, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth-vmboot: no settings: %v\n", err)

		return nil
	}

	return vmboot.ParseEnv(string(b))
}

// serveExports answers the host's requests for a staged artifact.
//
// **The path in, a byte count out, and the artifact on the device.** The host
// sends one line - the staged path inside this guest - and reads back `OK <n>`
// or `ERR <why>`; the archive itself is written to the export device, which the
// host reads as a file. Two channels because they are two different things: a
// question small enough to be a line, and an answer that may be gigabytes.
//
// One at a time, because there is one device. `SAVE ARTIFACT` is not on the hot
// path and the alternative - offsets, and a guest and a host agreeing about
// them - is a second allocator to get wrong.
func serveExports() {
	fd, err := listenVsock(vmboot.ExportPort, 4)
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth-vmboot: no export channel: %v\n", err)

		return
	}

	for {
		conn, _, acceptErr := unix.Accept(fd)
		if acceptErr != nil {
			fmt.Fprintf(os.Stderr, "earth-vmboot: export accept: %v\n", acceptErr)

			return
		}

		exportOnce(os.NewFile(uintptr(conn), "exports"))
	}
}

func exportOnce(c *os.File) {
	defer func() { _ = c.Close() }()

	asked, err := readAsk(c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth-vmboot: export request: %v\n", err)

		return
	}

	n, err := writeExport(asked)
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth-vmboot: export %s: %v\n", asked, err)
		fmt.Fprintf(c, "ERR %v\n", err)

		return
	}

	fmt.Fprintf(c, "OK %d\n", n)
}

// writeExport packs the staged path onto the export device.
//
// **Synced before the count is reported**, because the host reads the device as
// an ordinary file the moment it has the number: an unsynced write is a host
// reading a hole where the artifact is, which is the same class of race as the
// blob acknowledgement and would be as hard to see.
func writeExport(at string) (int64, error) {
	dev, err := os.OpenFile(vmboot.ExportDev, os.O_WRONLY, 0)
	if err != nil {
		return 0, fmt.Errorf("open the export device: %w", err)
	}

	defer func() { _ = dev.Close() }()

	n, err := bulk.PackTree(at, dev)
	if err != nil {
		return 0, err
	}

	err = dev.Sync()
	if err != nil {
		return 0, fmt.Errorf("flush the export device: %w", err)
	}

	return n, nil
}

// readAsk reads one line, which is the whole request.
//
// A byte at a time and bounded: this is PID 1 reading something from outside,
// and a `bufio.Reader` would happily buffer until it ran out of memory.
func readAsk(c *os.File) (string, error) {
	var (
		out [4096]byte
		n   int
	)

	for n < len(out) {
		_, err := c.Read(out[n : n+1])
		if err != nil {
			return "", fmt.Errorf("read the request: %w", err)
		}

		if out[n] == '\n' {
			return string(out[:n]), nil
		}

		n++
	}

	return "", fmt.Errorf("no newline in the first %d bytes of a request", len(out))
}

// writeResolver puts the host's resolver where a step will find it.
//
// **`/etc/resolv.conf` in the guest, because that is what the agent binds into
// every step.** An image ships none - the runtime is expected to provide one -
// so without this every name lookup in every step fails, each tool with its own
// unrelated-looking error (E931's shape, reached through a different door).
//
// The kernel has already configured the interface from its own `ip=` parameter
// before this runs; the resolver is the one part of that it does not write
// where anything looks for it.
func writeResolver() error {
	b, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return fmt.Errorf("read the kernel command line: %w", err)
	}

	at := vmboot.ParseNet(string(b)).DNS
	if !at.IsValid() {
		return nil
	}

	err = os.MkdirAll("/etc", 0o755)
	if err != nil {
		return fmt.Errorf("make /etc: %w", err)
	}

	err = os.WriteFile("/etc/resolv.conf", []byte("nameserver "+at.String()+"\n"), 0o644)
	if err != nil {
		return fmt.Errorf("write /etc/resolv.conf: %w", err)
	}

	return nil
}

// sayStore reports what the store held when this guest mounted it.
//
// **One line, and it settles a question nothing else can.** The host asks the
// guest what layers it holds and takes "none" for an answer; whether that means
// an empty store, a store that did not survive the last shutdown, or a device
// mounted somewhere else is invisible from outside. From here it is a count.
func sayStore() {
	at := filepath.Join(storeAt, "layers")

	entries, err := os.ReadDir(at)
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth-vmboot: store: no %s yet (%v)\n", at, err)

		return
	}

	fmt.Fprintf(os.Stderr, "earth-vmboot: store: %d layer(s) in %s\n", len(entries), at)
}

// saySettings reports how many settings reached this guest.
//
// **Because not arriving is silent.** A setting the guest never received is not
// an error anywhere: the guest uses its default, the build works, and an A/B
// between two values of it produces one result twice. That is how a whole class
// of them was found to be missing - fifteen settings the agent reads, and a
// microVM was passing none.
//
// A count rather than the values: some are paths and one day one will be a
// secret, and the question this answers is "did they cross".
func saySettings() {
	fmt.Fprintf(os.Stderr, "earth-vmboot: settings: %d from the host\n", len(fromCmdline()))
}
