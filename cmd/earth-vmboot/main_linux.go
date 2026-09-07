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

	"github.com/EarthBuild/earthbuild/cmd/earth-vmboot/vmboot"
	"golang.org/x/sys/unix"
)

// storeDev is the block device carrying the layer store, and storeAt is where
// the agent expects to find it.
const (
	storeDev = "/dev/vda"
	storeAt  = "/store"
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

// waitForHost blocks until the host connects on the vsock port.
func waitForHost() (*os.File, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}

	err = unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: vmboot.VsockPort})
	if err != nil {
		return nil, fmt.Errorf("bind vsock port %d: %w", vmboot.VsockPort, err)
	}

	err = unix.Listen(fd, 1)
	if err != nil {
		return nil, fmt.Errorf("listen on vsock: %w", err)
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
