//go:build linux

package exec

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sys/unix"

	"github.com/EarthBuild/earthbuild/cmd/earth-vmboot/vmboot"
)

// vmDigest names this machine after what it is, so the next build can find it
// and can never mistake it for one built differently.
//
// The same rule the Apple backend names its VMs by, and for the same reasons -
// see sandboxDigest. What is in here is what the machine *is*: the kernel it
// boots, the initramfs it runs, the device it mounts, its size, its network,
// and the settings the host sends across on the kernel command line. What is
// not in here is what a build asks it to do, which is the request.
func (f *Firecracker) vmDigest() string {
	parts := []string{
		f.Kernel, f.Initrd, f.StoreImage,
		strconv.Itoa(f.CPUs()), strconv.Itoa(orDefault(f.MemoryMiB, defaultMemory())),
		os.Getenv(EnvTap),
	}

	// The settings that cross into the guest decide how it behaves, and a guest
	// is found by name: leave them out and changing one appears to do nothing
	// until every running machine has been stopped by hand.
	return sandboxDigest(append(parts, guestSettings()...)...)
}

// recordMatches reports whether a record names a live machine of this shape.
//
// Ownership is asked separately by the caller, because reading /proc is the
// part that cannot be exercised without a firecracker to read.
func recordMatches(rec vmRecord, want string) bool {
	return rec.Vsock != "" && rec.Digest == want && vmAlive(rec)
}

// vmStartLock serialises find-or-boot for one store device.
//
// **Two builds starting together would otherwise both find nothing and both
// boot**, which is the two-guests-one-filesystem fault the store claim exists
// to prevent - arriving by a different road. Held only across the decision, not
// for the life of the machine: what protects the device afterwards is the claim
// the machine itself holds.
func vmStartLock(store string) (release func(), err error) {
	if store == "" {
		return func() {}, nil
	}

	at := store + ".start"

	lock, err := os.OpenFile(at, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", at, err)
	}

	// Blocking, unlike the store claim: what is being waited for here is a
	// decision, which is milliseconds, and never a build.
	err = flockWithinBlocking(lock, vmStartPatience)
	if err != nil {
		_ = lock.Close()

		return nil, fmt.Errorf("take the start lock on %s: %w", at, err)
	}

	return func() { _ = lock.Close() }, nil
}

// vmStartPatience bounds the wait for another build's decision. Generous
// against a decision and far short of a build, so a machine that is booting is
// waited for and a wedged one is not waited for indefinitely.
const vmStartPatience = 60 * time.Second

// flockWithinBlocking waits for an exclusive lock, giving up after within.
func flockWithinBlocking(f *os.File, within time.Duration) error {
	start := time.Now()

	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}

		if time.Since(start) >= within {
			return err //nolint:wrapcheck // the caller names the file
		}

		time.Sleep(storeClaimPoll)
	}
}

// haltRecorded stops the machine a record names, and waits for it to go.
//
// Used where a build wants a machine other than the one that is running: the
// device holds one filesystem, so the old machine has to be gone before the new
// one mounts it. A machine that will not stop is reported rather than raced.
func haltRecorded(rec vmRecord) error {
	if !vmAlive(rec) {
		return nil
	}

	// The VMM traps this and exits, which is how earth-vmboot's guest resets -
	// the store is unmounted on the way out, so the next machine finds it
	// consistent.
	_ = unix.Kill(rec.PID, unix.SIGTERM)

	deadline := time.Now().Add(vmHaltPatience)
	for time.Now().Before(deadline) {
		if !vmAlive(rec) {
			return nil
		}

		time.Sleep(storeClaimPoll)
	}

	_ = unix.Kill(rec.PID, unix.SIGKILL)

	// **Killed only after asking, and reported.** A microVM's store is attached
	// with the host's page cache answering its flushes, so a machine that did
	// not unmount leaves a filesystem the next guest may refuse. See
	// storeUnmountable.
	fmt.Fprintf(os.Stderr, "earthbuild: the guest holding %s did not stop when"+
		" asked and was killed\n  its store may need checking; see"+
		" EARTH_VM_DURABLE_STORE\n", rec.Vsock)

	return nil
}

// vmHaltPatience is how long a machine gets to unmount and go.
const vmHaltPatience = 15 * time.Second

// attach joins the machine the register names, when it is the one wanted.
//
// Reports whether it joined. Every way of not joining is "no": a register that
// names nothing, a machine that has gone, one built differently, one that will
// not answer its socket. The caller's next move is the same in all of them -
// boot one - and that is the right move whatever went wrong here.
//
// Called with f.mu held, by Start. The handshake is not done here. Start returns a connection and the executor
// greets the guest over it, so a machine that is up but wedged is discovered
// there, where the recovery already lives: Stop, then Remove, then Start again.
func (f *Firecracker) attach(want string) (Conn, bool) {
	if !mayAttach() {
		return nil, false
	}

	rec, ok := readVMRecord(f.StoreImage)
	if !ok {
		return nil, false
	}

	if !recordMatches(rec, want) || !vmIsOurs(rec) {
		// A machine that is running but is not this one holds the device this
		// build needs, so it has to go before the new one mounts it.
		if vmAlive(rec) && vmIsOurs(rec) {
			_ = haltRecorded(rec)
		}

		forgetVMRecord(f.StoreImage)

		return nil, false
	}

	conn, err := tryDial(rec.Vsock, vmboot.VsockPort)
	if err != nil {
		// It is recorded, and its process is alive, and it will not talk. Take
		// it away rather than leave the next build to find the same thing.
		_ = haltRecorded(rec)
		forgetVMRecord(f.StoreImage)

		return nil, false
	}

	// **The network before anything is said to the guest.** A joined machine
	// has a tap nobody has been servicing since the last build ended; this
	// build serves it for as long as it runs. Left out, the machine works until
	// the first step fetches, which is a long way from here - and is exactly
	// how the first attempt at reuse failed.
	f.attachNet()

	netErr := f.netForAttached(filepath.Dir(rec.Vsock))
	if netErr != nil {
		fmt.Fprintf(os.Stderr, "earthbuild: the machine already running cannot"+
			" give this build its network, so it is being replaced: %v\n", netErr)

		_ = conn.Close()
		_ = haltRecorded(rec)
		forgetVMRecord(f.StoreImage)

		return nil, false
	}

	// **No lock taken here: Start holds it.** Go's mutexes are not reentrant,
	// so locking would deadlock the caller - which is exactly what it did, and
	// the symptom was a corpus run that stopped after two targets with no
	// error to read.
	f.attached = true
	f.vsockAt = rec.Vsock
	f.exports = rec.Exports
	f.conn = conn

	f.reuses.Add(1)

	return conn, true
}

// Boots reports how many machines this sandbox started, and Reuses how many it
// joined. Stated as numbers because "one machine per session" is a claim a test
// can check and a comment cannot.
func (f *Firecracker) Boots() int { return int(f.boots.Load()) }

// Reuses reports how many running machines this sandbox joined rather than
// replaced.
func (f *Firecracker) Reuses() int { return int(f.reuses.Load()) }

// EnvReuse offers a machine that outlives the build that started it.
//
// **Offered, not imposed**, in the shape EARTH_VM itself is offered. A reused
// machine is a weaker boundary than a fresh one - a guest serving a second
// build carries the first's kernel state and page cache, though not its agent,
// which is a new process per build, nor its steps, which run in their own
// overlays. That is a trade worth having and not worth taking unasked.
//
// It is also new, and the last attempt at it tore a store. Off until it has run
// against more than the machine it was written on.
const EnvReuse = "EARTH_VM_REUSE"

// mayAttach reports whether a machine can be joined at all on this host.
//
// **The network is the question, and it is answered differently now.** By
// default this engine provides the guest's network itself: a tap in a namespace
// the shim made, and a userspace stack this process runs. That stack cannot
// outlive the build - it is in the build - and a machine left with a tap nobody
// services is one whose next guest cannot resolve a name.
//
// It does not have to outlive it (E981). Between builds the guest is idle and a
// tap with no reader drops nothing anybody sent; what is needed is that a later
// build can *get* a socket for the tap, which is what the server the shim
// leaves inside the namespace answers. See NetFDCommand.
//
// So both networks can be rejoined now: one somebody else made and services,
// and one this engine made and can be handed back. What is left is whether the
// operator asked.
func mayAttach() bool {
	switch os.Getenv(EnvReuse) {
	case "", "0", "false", "no":
		return false
	default:
		return true
	}
}

// netForAttached gives this build a stack on the tap of a machine it joined.
//
// **The build serves the network for as long as it runs.** Nothing serves it in
// between, and nothing needs to: the guest is waiting on its vsock and sends
// nothing. What this cannot do is fail quietly - a joined machine whose network
// is not picked up looks exactly like a working one until a step fetches, which
// is a long way from here.
func (f *Firecracker) netForAttached(dir string) error {
	if !f.ownNet {
		// The tap is somebody else's and is serviced by whoever made it.
		return nil
	}

	sock, err := DialNetFD(netFDAt(dir))
	if err != nil {
		return fmt.Errorf("take over the network of the machine already"+
			" running: %w", err)
	}

	u, err := startUserNet(sock)
	if err != nil {
		return err
	}

	f.own = u

	return nil
}

// keptOpen is the descriptors a machine inherits so that what they hold lasts
// as long as it does.
//
// **A lock is released by the last descriptor closing, whoever holds it.** The
// store claim and the sandbox directory's claim are taken by the build that
// boots a machine, and a machine outliving that build would otherwise be left
// with an unclaimed device and a directory the next build's sweep is entitled
// to delete. Handed to the VMM, they are released by the one event that means
// the machine is finished with: the VMM exiting.
//
// Nils are dropped rather than passed. A sandbox with no store device has no
// claim to hand over, and an ExtraFiles entry that is nil is a closed
// descriptor in the child, which the VMM would find where it expected a socket.
func keptOpen(files ...*os.File) []*os.File {
	out := make([]*os.File, 0, len(files))

	for _, f := range files {
		if f != nil {
			out = append(out, f)
		}
	}

	return out
}
