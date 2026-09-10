//go:build linux

package exec

import (
	"fmt"
	"net"
	"os"
	osexec "os/exec"
	"strconv"

	"github.com/EarthBuild/earthbuild/engine/fdpass"
	"golang.org/x/sys/unix"
)

// NetShimCommand selects the shim that gives a microVM its network.
//
// **A re-exec, because the namespaces have to exist before the process does.**
// `CLONE_NEWUSER|CLONE_NEWNET` are applied by the clone that starts a child, so
// nothing can put *itself* in a new network namespace and then make a device in
// it. Go cannot run code between clone and exec either, which is why this is a
// second entry point into the same binary rather than a function.
const NetShimCommand = "vm-net"

// tapName is the device the guest's network interface is attached to.
//
// Fixed, and private to a namespace nothing else can see: the shim makes one
// namespace per sandbox, so there is exactly one tap in it and no name to
// collide with.
const tapName = "tap0"

// shimFD is where the shim finds the socket it hands the packet channel back
// on. Three, because 0-2 are the standard streams and `ExtraFiles` starts here.
const shimFD = 3

// NetShimMain is the child: it makes the guest's tap, hands the host a way to
// see what the guest sends, and becomes the VMM.
//
// **Root here is root nowhere else.** The parent maps this process's uid to 0
// inside a user namespace of its own, which is what makes `TUNSETIFF` and
// `AF_PACKET` permitted - both need CAP_NET_ADMIN or CAP_NET_RAW *in the user
// namespace owning the network namespace*, and this one owns a namespace with
// nothing in it but a tap. Outside, the process is the invoking user and can do
// nothing it could not do before.
//
// The layout, which is the part worth stating once:
//
//	firecracker ─fd end─▸ tap0 ◂─kernel end─ AF_PACKET ─fd─▸ the host's netstack
//
// The VMM takes the tap's *file descriptor* end for its virtio-net device, so
// the host cannot also hold it - a second `TUNSETIFF` on the same device is
// EBUSY. What the host gets instead is a packet socket bound to the tap's
// kernel end, which sees every frame the guest sends and can inject every frame
// it should receive.
func NetShimMain(args []string) {
	err := netShim(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth %s: %v\n", NetShimCommand, err)
		os.Exit(1)
	}
}

func netShim(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("nothing to run after making the guest's network")
	}

	err := makeTap(tapName)
	if err != nil {
		return err
	}

	sock, err := packetSocket(tapName)
	if err != nil {
		return err
	}

	out, err := fdpass.ConnFromFD(shimFD)
	if err != nil {
		return fmt.Errorf("the channel back to the engine on fd %d: %w", shimFD, err)
	}

	err = fdpass.SendFile(out, sock)
	if err != nil {
		return fmt.Errorf("hand the packet channel back: %w", err)
	}

	// Closed before the exec, so the engine sees end-of-stream rather than
	// waiting on a descriptor the VMM inherited and will never write to.
	_ = out.Close()
	_ = sock.Close()

	// **Left behind, because a tap cannot be opened from outside its
	// namespace.** This process is about to become the VMM, and after that
	// nothing in the namespace can be asked for anything. A build that finds
	// this machine already running needs a socket on its tap and has no way to
	// make one; that is what this answers. See NetFDCommand.
	//
	// Started before the exec and not waited for: it is a child of a process
	// that is about to be replaced, so when the machine goes it is orphaned and
	// reaped like any other. A machine nobody will rejoin leaves one that
	// nobody asks, which costs a process and no decisions.
	startNetFDServer(tapName)

	// **Exec rather than run**, so the VMM *is* this process: the network
	// namespace lives as long as a process is in it, and a shim that waited
	// would be a second process to signal, reap and get wrong.
	path, err := osexec.LookPath(args[0])
	if err != nil {
		return fmt.Errorf("find %s: %w", args[0], err)
	}

	return unix.Exec(path, args, os.Environ())
}

// makeTap creates the device the VMM will attach to, and leaves it behind.
//
// **Persistent, because this process is about to become somebody else.** The
// device would otherwise vanish with the descriptor at exec, and the VMM would
// find nothing to attach to. Persisting it is safe for the reason the name is:
// the namespace goes when the VMM does, and takes the device with it.
func makeTap(name string) error {
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open /dev/net/tun: %w"+
			"\n  a microVM's network needs it, and a container without the device"+
			" node cannot make one", err)
	}

	defer func() { _ = unix.Close(fd) }()

	req, err := unix.NewIfreq(name)
	if err != nil {
		return fmt.Errorf("name the tap %s: %w", name, err)
	}

	// IFF_NO_PI: bare Ethernet frames, with none of the four-byte header the
	// tun driver otherwise prepends. Both ends here speak L2 and neither wants
	// it.
	req.SetUint16(unix.IFF_TAP | unix.IFF_NO_PI)

	err = unix.IoctlIfreq(fd, unix.TUNSETIFF, req)
	if err != nil {
		return fmt.Errorf("make the tap %s: %w"+
			"\n  this needs CAP_NET_ADMIN in the user namespace owning the network"+
			" namespace, which is what the engine's re-exec arranges", name, err)
	}

	err = unix.IoctlSetInt(fd, unix.TUNSETPERSIST, 1)
	if err != nil {
		return fmt.Errorf("keep the tap %s past this process: %w", name, err)
	}

	return bringUp(name)
}

// bringUp sets IFF_UP on the tap, without which the kernel drops what is
// written to it and delivers nothing from it.
func bringUp(name string) error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return fmt.Errorf("open a socket to configure %s: %w", name, err)
	}

	defer func() { _ = unix.Close(fd) }()

	req, err := unix.NewIfreq(name)
	if err != nil {
		return fmt.Errorf("name %s: %w", name, err)
	}

	err = unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, req)
	if err != nil {
		return fmt.Errorf("read the flags of %s: %w", name, err)
	}

	req.SetUint16(req.Uint16() | unix.IFF_UP | unix.IFF_RUNNING)

	err = unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, req)
	if err != nil {
		return fmt.Errorf("bring %s up: %w", name, err)
	}

	return nil
}

// packetSocket is a raw view of everything crossing the tap's kernel end.
//
// `ETH_P_ALL` in network byte order, which is the one fiddly part of AF_PACKET
// and the reason a socket that binds cleanly can still see nothing.
func packetSocket(name string) (*os.File, error) {
	iface, err := netInterfaceIndex(name)
	if err != nil {
		return nil, err
	}

	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_ALL)))
	if err != nil {
		return nil, fmt.Errorf("open a packet socket: %w"+
			"\n  this needs CAP_NET_RAW in the user namespace owning the network"+
			" namespace", err)
	}

	err = unix.Bind(fd, &unix.SockaddrLinklayer{
		Protocol: htons(unix.ETH_P_ALL),
		Ifindex:  iface,
	})
	if err != nil {
		_ = unix.Close(fd)

		return nil, fmt.Errorf("bind a packet socket to %s: %w", name, err)
	}

	return os.NewFile(uintptr(fd), "packet:"+name), nil
}

// netInterfaceIndex is the kernel's number for a device, which is what a packet
// socket binds by. Looked up rather than assumed: the index is per namespace and
// the tap is not the only device in one - loopback is index 1 wherever you go.
func netInterfaceIndex(name string) (int, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return 0, fmt.Errorf("find the tap %s after making it: %w", name, err)
	}

	return iface.Index, nil
}

// htons is host-to-network for the 16-bit protocol field. AF_PACKET wants it
// big-endian even in a struct every other field of which is native.
func htons(v uint16) uint16 { return v<<8 | v>>8 }

// startNetFDServer leaves something in this namespace that can be asked for a
// socket on the tap.
//
// Best effort and silent about not being asked for: a machine whose network
// cannot be rejoined is a machine that has to be booted again, which is what
// every machine did until now. Failing the build over it would trade a working
// guest for a missing optimisation.
func startNetFDServer(device string) {
	at := os.Getenv(EnvNetFDs)
	if at == "" {
		return
	}

	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "earthbuild: no path to leave a network server at: %v\n", err)

		return
	}

	//nolint:gosec,noctx // this binary, and a machine's life is not a context
	srv := osexec.Command(self, NetFDCommand, at, device)
	srv.Stdout, srv.Stderr = os.Stdout, os.Stderr

	// **Everything this process was handed stays behind.** The engine passes
	// the store claim and the sandbox directory's claim as extra descriptors so
	// that the *machine* holds them - a guest that outlives its build has the
	// store mounted the whole time, and a claim released when the build exits
	// would let the next build boot a second guest onto the same filesystem.
	//
	// Extra descriptors arrive with close-on-exec cleared, which is what makes
	// the VMM inherit them and would make this inherit them too. A server
	// holding the store claim keeps it after the machine has gone: the next
	// build is refused by a lock whose owner "is no longer running", which is
	// true, and the reason it is still held is standing right there.
	undo := closeOnExecFrom(3)

	err = srv.Start()

	undo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "earthbuild: this machine cannot be rejoined: %v\n", err)

		return
	}

	// **Not waited for, and deliberately not held.** The wait would have to
	// outlive this process, which is about to exec, and there is nothing
	// sensible to do with the answer: the server ends when the namespace does.
	_ = srv.Process.Release()
}

// closeOnExecFrom marks every open descriptor from `first` upwards
// close-on-exec, and returns a function putting them back as they were.
//
// **For starting one child out of a process whose descriptors belong to
// another.** The shim is handed locks meant for the VMM it is about to become;
// anything else it starts in between must not keep them. Go marks what it opens
// close-on-exec already, so what this finds is exactly what was passed in.
//
// Read from /proc rather than guessed from a count: the engine decides how many
// it passes, and a number agreed in two places is a number that will disagree.
func closeOnExecFrom(first int) (undo func()) {
	names, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		// Nothing to put back, and nothing that can be done about it here. A
		// leaked lock is a later build refused with a message that says which
		// device and by whom, which is a great deal better than this failing.
		return func() {}
	}

	var cleared []int

	for _, e := range names {
		fd, err := strconv.Atoi(e.Name())
		if err != nil || fd < first {
			continue
		}

		bits, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil || bits&unix.FD_CLOEXEC != 0 {
			continue
		}

		_, err = unix.FcntlInt(uintptr(fd), unix.F_SETFD, bits|unix.FD_CLOEXEC)
		if err == nil {
			cleared = append(cleared, fd)
		}
	}

	return func() {
		for _, fd := range cleared {
			bits, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
			if err == nil {
				_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFD, bits&^unix.FD_CLOEXEC)
			}
		}
	}
}
