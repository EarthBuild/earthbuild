//go:build linux

package exec

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/EarthBuild/earthbuild/cmd/earth-vmboot/vmboot"
	"github.com/EarthBuild/earthbuild/engine/guest"
)

// SetFill gives this machine a way to answer a step's fault-in.
//
// **The last backend that could not.** A fault travels the wrong way - the guest
// asking the host for a path its base does not have - and every other message
// goes the other. A sandbox that spawns its guest as a child passes a second
// descriptor for it; through a VM there is no descriptor to pass, so the guest
// listens on a socket of its own and the host reaches it over vmboot.FillPort.
//
// Without this a worker in a microVM materialises whole layers instead of the
// paths a step was predicted to read - slower and correct, and what it did until
// now (E305).
//
// Idempotent and order-free: a caller may set this before the machine is up or
// after, and the server starts on whichever happens second.
func (f *Firecracker) SetFill(fill func(handle, path string) error) {
	f.mu.Lock()
	f.fill = fill
	f.mu.Unlock()

	f.serveFills()
}

// serveFills answers fault-ins for as long as the machine is there.
//
// Started once. A second call finds `fills` already true and returns, which is
// what makes it safe to call from Start, from attach and from SetFill without
// any of the three knowing about the others.
func (f *Firecracker) serveFills() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.serveFillsLocked()
}

// serveFillsLocked is serveFills for a caller that already holds the lock.
//
// **Two entry points because Go's mutexes are not reentrant.** `Start` holds the
// lock for its whole body and `attach` runs inside it, so a version that locked
// would deadlock both - which is the mistake `attach` already carries a comment
// about, made again directly underneath it.
func (f *Firecracker) serveFillsLocked() {
	fill, vsock := f.fill, f.vsockAt
	if fill == nil || vsock == "" || f.fills {
		return
	}

	f.fills = true
	gone := f.gone

	go func() {
		conn, err := dialPort(context.Background(), vsock, vmboot.FillPort, gone)
		if err != nil {
			// Said rather than fatal: a machine that cannot answer a fault-in
			// still runs every step, from a base materialised whole.
			fmt.Fprintf(os.Stderr, "earthbuild: this machine cannot answer a"+
				" fault-in, so steps take whole layers: %v\n", err)

			return
		}

		defer func() { _ = conn.Close() }()

		rw, ok := conn.(net.Conn)
		if !ok {
			return
		}

		// Ends when the guest hangs up, which is what closes this without
		// anything having to be told.
		_ = guest.ServeFills(rw, fill)
	}()
}
