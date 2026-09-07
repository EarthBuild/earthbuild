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

	// Started before the agent, because the host may push a blob before it
	// sends its first request: the two channels are independent and the host
	// has no way to know when this one is listening.
	go serveBulk(filepath.Join(storeAt, "blobs"))

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
	fd, err := listenVsock(vmboot.BulkPort, 4)
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

		// One at a time: the host opens one connection and sends every blob of
		// a build down it, and a second would be a second party writing into
		// this guest's store.
		f := os.NewFile(uintptr(conn), "bulk")

		n, err := bulk.ReceiveBlobs(f, at)
		if err != nil {
			fmt.Fprintf(os.Stderr, "earth-vmboot: bulk channel: %v\n", err)
		}

		// **Said out loud, on the console the host is already reading.** What
		// arrived is the one fact that separates "the channel never carried it"
		// from "the store lost it", and without it the two look identical from
		// outside: a guest reporting `no such file` for a blob the host
		// believes it sent.
		fmt.Fprintf(os.Stderr, "earth-vmboot: %d blob(s) into %s\n", n, at)

		_ = f.Close()
	}
}

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

// halt stops the guest rather than letting PID 1 return.
func halt() {
	_ = os.Stdout.Sync()
	_ = unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)

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
	return append(append([]string{}, boot...), "EARTH_GUEST_ROOT="+storeAt)
}
