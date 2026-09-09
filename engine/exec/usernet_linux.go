//go:build linux

package exec

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	osexec "os/exec"
	"syscall"
	"time"

	"github.com/containers/gvisor-tap-vsock/pkg/types"
	"github.com/containers/gvisor-tap-vsock/pkg/virtualnetwork"

	"github.com/EarthBuild/earthbuild/cmd/earth-vmboot/vmboot"
	"github.com/EarthBuild/earthbuild/engine/fdpass"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/guestd"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/mat/overlay"
	"github.com/EarthBuild/earthbuild/engine/timing"
)

// The addresses a guest sees when the engine provides its own network.
//
// **Chosen, not discovered, because nothing else is on this network.** The
// subnet exists inside one sandbox and is reached by nothing but its own guest,
// so there is no host to collide with and no reason to make it a setting. The
// range is the one gvisor-tap-vsock uses by default, which keeps it recognisable
// to anyone who has debugged rootless Podman.
const (
	userNetSubnet  = "192.168.127.0/24"
	userNetGateway = "192.168.127.1"
	userNetGuest   = "192.168.127.2"
	userNetMask    = "255.255.255.0"

	// userNetMTU is the frame size on the tap. 1500 because the packets leave
	// through the host's ordinary sockets, and a guest that thinks it has more
	// produces fragments nobody reassembles.
	userNetMTU = 1500
)

// userNet is a network the engine provides itself, with no privilege and
// nothing installed.
//
// **The whole point is that a build must not need CAP_NET_ADMIN.** Making a tap
// on the host needs it, and so does addressing and routing one - which is why
// the alternative is a machine somebody prepared as root, once, and remembers
// to prepare again after a reboot. Here the engine makes a user namespace of its
// own, is root inside it, and makes the tap there; nothing outside that
// namespace changes.
//
// The traffic leaves through ordinary sockets opened by this process, as this
// user. There is no NAT, no forwarding and no firewall rule, because there is no
// kernel networking involved at all past the tap: a userspace TCP/IP stack
// terminates the guest's connections and opens its own.
type userNet struct {
	net  *virtualnetwork.VirtualNetwork
	sock *os.File

	stop context.CancelFunc
	done chan struct{}
}

// guestConfig is what the guest is told at boot, which has to agree with what
// the stack answers to.
func (u *userNet) guestConfig() vmboot.Net {
	return vmboot.Net{
		Address: mustPrefix(userNetGuest, userNetMask),
		Gateway: mustAddr(userNetGateway),
		// The stack answers DNS on the gateway address, so a guest needs no
		// resolver of the host's and no route to one.
		DNS: mustAddr(userNetGateway),
	}
}

// startUserNet brings up the stack on a packet socket the shim handed back.
//
// Started before the guest is spoken to and stopped with the sandbox: the guest
// will ARP for its gateway within a second of booting, and a stack that started
// afterwards would answer the retry rather than the request.
func startUserNet(sock *os.File) (*userNet, error) {
	cfg := &types.Configuration{
		Debug:             false,
		MTU:               userNetMTU,
		Subnet:            userNetSubnet,
		GatewayIP:         userNetGateway,
		GatewayMacAddress: gatewayMAC,
		DNSSearchDomains:  nil,
		Forwards:          map[string]string{},
		NAT:               map[string]string{},
		GatewayVirtualIPs: []string{},
	}

	n, err := virtualnetwork.New(cfg)
	if err != nil {
		_ = sock.Close()

		return nil, fmt.Errorf("start the guest's network: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	u := &userNet{net: n, sock: sock, stop: cancel, done: make(chan struct{})}

	go func() {
		defer close(u.done)

		// Bare L2 frames with one read to a frame, which is what a packet
		// socket gives and what this protocol expects. The error is the
		// sandbox stopping in every ordinary case.
		err := n.AcceptBess(ctx, &framedConn{f: sock})
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "earthbuild: the guest's network stopped: %v\n", err)
		}
	}()

	return u, nil
}

// Close stops the stack and releases the packet socket.
func (u *userNet) Close() {
	if u == nil {
		return
	}

	u.stop()
	_ = u.sock.Close()

	select {
	case <-u.done:
	case <-time.After(2 * time.Second):
	}
}

// gatewayMAC is the hardware address the guest ARPs to. Locally administered,
// and fixed so a guest that remembers one across a reboot is not surprised.
const gatewayMAC = "5a:94:ef:e4:0c:dd"

// framedConn is a packet socket seen as a connection.
//
// **One read is one frame**, which is the whole reason this exists: `net.Conn`
// is a byte stream to most callers and a datagram channel to this one, and the
// packet socket is the latter. `net.FileConn` refuses the descriptor outright -
// it is a socket the runtime's poller does not recognise - so the file is used
// directly and the deadline methods are the honest no-ops of something that is
// closed to stop it.
type framedConn struct{ f *os.File }

func (c *framedConn) Read(p []byte) (int, error)  { return c.f.Read(p) }  //nolint:wrapcheck // a pass-through
func (c *framedConn) Write(p []byte) (int, error) { return c.f.Write(p) } //nolint:wrapcheck // a pass-through
func (c *framedConn) Close() error                { return c.f.Close() }  //nolint:wrapcheck // a pass-through

func (c *framedConn) LocalAddr() net.Addr              { return packetAddr{} }
func (c *framedConn) RemoteAddr() net.Addr             { return packetAddr{} }
func (c *framedConn) SetDeadline(time.Time) error      { return nil }
func (c *framedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *framedConn) SetWriteDeadline(time.Time) error { return nil }

type packetAddr struct{}

func (packetAddr) Network() string { return "packet" }
func (packetAddr) String() string  { return tapName }

// mustAddr and mustPrefix parse constants this file owns. A malformed one is a
// programming error rather than a condition, and a build that started with a
// zero address would fail somewhere far from the typo.
func mustAddr(s string) netip.Addr {
	at, err := netip.ParseAddr(s)
	if err != nil {
		panic("earthbuild: " + s + " is not an address: " + err.Error())
	}

	return at
}

func mustPrefix(addr, mask string) netip.Prefix {
	at, m := mustAddr(addr), mustAddr(mask)

	b := m.As4()
	bits := 0

	for i := range 32 {
		if b[i/8]&(1<<(7-i%8)) == 0 {
			break
		}

		bits++
	}

	return netip.PrefixFrom(at, bits)
}

// throughNetShim rewrites the command to run the VMM inside a network namespace
// of the engine's own, and returns the channel the shim answers on.
//
// **The namespaces are the clone's, not the process's.** `CLONE_NEWUSER` and
// `CLONE_NEWNET` take effect when a child is started, so the VMM is launched
// through a second entry point into this binary which makes the tap and then
// becomes the VMM. Mapping this user to 0 inside the new user namespace is what
// makes that permitted, and it grants nothing outside it.
func (f *Firecracker) throughNetShim(cmd *osexec.Cmd, argv []string) (*net.UnixConn, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find this engine's own path to re-exec: %w", err)
	}

	here, there, err := fdpass.SocketPair()
	if err != nil {
		return nil, fmt.Errorf("make the channel the network arrives on: %w", err)
	}

	cmd.Path = self
	cmd.Args = append([]string{self, NetShimCommand}, argv...)

	// Becomes fd 3 in the child, which is where the shim looks. Closed here
	// once the child has it, by the deferred close in takeNetFrom.
	cmd.ExtraFiles = []*os.File{fileOf(there)}

	cmd.SysProcAttr.Cloneflags = syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET
	cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{
		{ContainerID: 0, HostID: os.Getuid(), Size: 1},
	}
	cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{
		{ContainerID: 0, HostID: os.Getgid(), Size: 1},
	}
	// Without this the child cannot write its own gid map, which is a kernel
	// rule about setgroups and not about this engine.
	cmd.SysProcAttr.GidMappingsEnableSetgroups = false

	return here, nil
}

// takeNetFrom receives the packet socket the shim made and starts the stack on
// it.
func (f *Firecracker) takeNetFrom(back *net.UnixConn) error {
	defer func() { _ = back.Close() }()

	err := back.SetReadDeadline(time.Now().Add(netShimPatience))
	if err != nil {
		return fmt.Errorf("set a deadline on the network channel: %w", err)
	}

	sock, err := fdpass.RecvFile(back)
	if err != nil {
		return fmt.Errorf("the guest's network never arrived: %w"+
			"\n  the shim makes it before the VMM starts, so its console says why", err)
	}

	u, err := startUserNet(sock)
	if err != nil {
		return err
	}

	f.own = u

	return nil
}

// netShimPatience bounds the wait for the shim. It makes a tap and a socket and
// says so; anything longer is a shim that failed and said why on the console.
const netShimPatience = 10 * time.Second

// fileOf is a connection as the descriptor a child inherits.
func fileOf(c *net.UnixConn) *os.File {
	f, err := c.File()
	if err != nil {
		return nil
	}

	return f
}

// guestSettings is what this machine passes to its guest.
//
// **The list Apple's backend passes, because the guest is the same agent.** It
// reads fifteen settings and a microVM gave it none: a guest's environment
// comes from its kernel rather than from the process that started the machine,
// so every one of them arrived unset and was silently ignored. That is not a
// failure anyone sees - it is an A/B whose two arms are the same arm, which is
// how it was found.
//
// Only what is set, so the command line carries what somebody asked for and
// nothing else. `EARTH_GUEST_ROOT` is deliberately absent: the store is a fact
// about the machine and `earth-vmboot` states it.
func guestSettings() []string {
	var out []string

	for _, name := range []string{
		guest.EnvIdle,
		guest.EnvTracePin,
		guest.EnvStepShim,
		guest.EnvDentryLimit,
		guest.EnvShareExports,
		guest.EnvCloneLayers,
		image.EnvHashOnUnpack,
		overlay.EnvScratchTmpfs,
		guestd.EnvProfile,
		guestd.EnvStoreFree,
		// **Without this the setting is a knob wired to nothing.** A guest's
		// environment comes from its kernel command line, not from the process
		// that started the VM, so a setting absent from this list is silently
		// ignored inside the VM - which once made fifteen of them look like
		// they had no effect.
		guestd.EnvCollectBudget,
		guestd.EnvProfileMode,
		timing.Env,
	} {
		if v := os.Getenv(name); v != "" {
			out = append(out, name+"="+v)
		}
	}

	return out
}
