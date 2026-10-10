//go:build linux

package exec

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/EarthBuild/earthbuild/engine/fdpass"
)

// NetFDCommand selects the process that hands out sockets for a machine's tap.
//
// **Because a tap cannot be opened from outside its namespace, and a descriptor
// can be passed out of one.** A machine that outlives the build which started
// it is reachable by a later build only through what it left behind: the guest
// on its vsock, the store on its device, and - for the network - this. There is
// no file for a tap. `/dev/net` holds `tun`, `/sys/class/net` outside the
// namespace lists the host's own interfaces, and the device exists as an
// interface reachable through `TUNSETIFF` from inside and nowhere else.
//
// So something stays inside. It needs no network of its own, which is as well:
// the namespace it sits in has no route anywhere, which is the whole of why the
// stack cannot live there either (E980).
const NetFDCommand = "vm-net-fds"

// EnvNetFDs is where the shim leaves the socket that answers for its tap.
//
// Passed rather than derived: the shim is handed the VMM's argv and nothing
// else about the sandbox, and reconstructing the path from `--config-file`
// would make two places agree by coincidence.
const EnvNetFDs = "EARTH_VM_NET_FDS"

// netFDAt is where a sandbox's tap answers from.
func netFDAt(dir string) string { return dir + "/net.fds" }

// NetFDMain is the process that stays in the namespace.
//
// Started by the shim before it becomes the VMM, so it is a child of a process
// that is about to be replaced - it outlives that replacement, and when the VMM
// goes it is orphaned and reaped by init like any other. It holds nothing but a
// listening socket and the name of a device.
func NetFDMain(args []string) {
	err := netFDServer(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth %s: %v\n", NetFDCommand, err)
		os.Exit(1)
	}
}

func netFDServer(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("expected a socket path and a device, got %q",
			strings.Join(args, " "))
	}

	at, device := args[0], args[1]

	// Removed first, because a machine that boots where one died finds the
	// old socket still there and `bind` refuses it - which reads as a machine
	// that will not start rather than as a name nobody is answering.
	_ = os.Remove(at)

	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: at, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen for requests for %s: %w", device, err)
	}

	defer func() { _ = ln.Close() }()

	// **Ends with the machine, and nothing else can end it.** This is started
	// by the shim, so its parent is the process that becomes the VMM; when that
	// goes, this is re-parented to init. With reuse off the build's process
	// group is killed and takes this with it, but a machine that outlives its
	// build is never killed that way - so without this, one server accumulates
	// per machine, holding a namespace open, for as long as the host is up.
	//
	// The parent is noted now, while it is still the process that started this,
	// and watched from there. See endWithParent.
	go endWithParent(ln, os.Getppid())

	return serveNetFDs(ln, func() (*os.File, error) { return packetSocket(device) })
}

// endWithParent closes the listener once the machine that started this has
// gone.
//
// **Watching the parent that was, not the parent that is.** The first version
// asked whether `getppid` had become 1, on the reasoning that an orphan is
// re-parented to init. That is only true where nothing else has claimed the
// job: a systemd user session is a child subreaper, so an orphan there is
// re-parented to *it*, `getppid` never returns 1, and the server never ends.
// Measured on a NixOS host - one server left behind per machine, accumulating
// for as long as the host was up.
//
// The parent's pid is noted at startup, while it is still the shim that started
// this, and `kill(pid, 0)` asks whether it is there. The shim becomes the VMM
// by `exec`, which keeps the pid, so the number is the machine's for its whole
// life.
//
// Closing rather than exiting, so the accept loop returns and the socket is
// unlinked on the way out - a name left behind is a later build dialling
// something nobody answers, which it reads as a machine that is there.
func endWithParent(ln *net.UnixListener, parent int) {
	for range time.Tick(orphanCheck) {
		if parent <= 1 || unix.Kill(parent, 0) != nil {
			_ = ln.Close()

			return
		}
	}
}

// serveNetFDs answers each caller with a socket of its own.
//
// **One per caller, not one shared.** A build serves the tap for as long as it
// runs and closes what it held when it ends; handing two builds the same
// descriptor would let the first one's close take the second one's network
// away.
//
// `make` is a parameter so the answering can be tested without a tap, which
// needs a namespace this process is not in when the test runs.
func serveNetFDs(ln *net.UnixListener, make func() (*os.File, error)) error {
	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			// The listener closed, which is how this ends: the machine is
			// going and there is nobody left to answer.
			return nil //nolint:nilerr // see above
		}

		answer(conn, make)
	}
}

// answer gives one caller a descriptor, or tells it why not.
//
// **A refusal is sent rather than dropped**, because from the other end a
// machine that could not make a socket and a machine that has gone look
// identical - and one of those is a defect while the other is a reason to boot.
func answer(conn *net.UnixConn, make func() (*os.File, error)) {
	defer func() { _ = conn.Close() }()

	sock, err := make()
	if err != nil {
		_, _ = conn.Write([]byte(netFDRefused + err.Error()))

		return
	}

	// Closed here whatever happens: the caller has its own copy once the
	// message is sent, and this end holding one keeps the socket alive after
	// the build that asked for it has gone.
	defer func() { _ = sock.Close() }()

	err = fdpass.SendFile(conn, sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth %s: hand over a socket: %v\n", NetFDCommand, err)
	}
}

// orphanCheck is how often this asks whether its machine has gone. Coarse: the
// cost of noticing late is one idle process, and the cost of asking often is a
// wakeup per second per machine.
const orphanCheck = 5 * time.Second

// netFDRefused prefixes the reason a machine could not make one, so a caller
// reading a message rather than a descriptor knows which it has.
const netFDRefused = "no: "

// DialNetFD asks a machine for a socket on its tap.
//
// The other half of NetFDCommand: called by a build that found a machine
// already running and needs to serve its network for as long as the build
// lasts.
func DialNetFD(at string) (*os.File, error) {
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: at, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("ask %s for a socket on its tap: %w"+
			"\n  a machine that has stopped leaves the name and nobody answering", at, err)
	}

	defer func() { _ = conn.Close() }()

	sock, err := fdpass.RecvFile(conn)
	if err != nil {
		return nil, fmt.Errorf("no socket came back from %s: %w", at, err)
	}

	return sock, nil
}
