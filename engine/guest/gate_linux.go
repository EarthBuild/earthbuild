//go:build linux

package guest

import (
	"fmt"
	"os"
	"slices"
	"strconv"

	"golang.org/x/sys/unix"
)

// gateVar names the descriptor the parent releases this process through.
const gateVar = "EARTH_GUEST_ID_GATE"

// WaitForIDs blocks until this process's user namespace has been mapped, then
// **re-executes this binary** so the mapping takes effect.
//
// The re-exec is the whole point and is not obvious. A parent cannot write
// `/proc/pid/uid_map` for a range until the child exists, so the mapping
// necessarily lands after the child has exec'd - and **a process's capabilities
// are computed at exec**. A guest that exec'd while unmapped is `nobody` with no
// capabilities, and gains none when the map is written; it then cannot mount its
// own overlay, which is exactly how the first attempt at this failed (E104).
//
// Executing again once the mapping is in place recomputes them, and the second
// image starts as uid 0 with the full set. It is what runc's `nsexec` and
// podman's re-exec do, and the hand-reproduction that proved it was an accident:
// `sh -c "read; mount ..."` works because `mount` is a separate binary, so the
// shell's fork-and-exec happens after the map.
//
// Only when the gate variable is set, and it is removed before the second exec -
// so the new image runs the ordinary path and nothing here can loop.
func WaitForIDs() error {
	fd := os.Getenv(gateVar)
	if fd == "" {
		return nil
	}

	n, err := strconv.Atoi(fd)
	if err != nil {
		return fmt.Errorf("%s=%q is not a descriptor", gateVar, fd)
	}

	f := os.NewFile(uintptr(n), "id-gate")
	if f == nil {
		return fmt.Errorf("%s=%d names no descriptor", gateVar, n)
	}

	// One byte, or EOF if the parent gave up. Either way the wait is over: a
	// parent that failed to map reports its own error, and this process failing
	// to mount afterwards would only repeat it less clearly.
	var b [1]byte

	_, _ = f.Read(b[:])
	_ = f.Close()

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find this binary to re-execute: %w", err)
	}

	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return len(kv) > len(gateVar) && kv[:len(gateVar)+1] == gateVar+"="
	})

	err = unix.Exec(self, os.Args, env)

	return fmt.Errorf("re-execute %s with the mapping in place: %w", self, err)
}
