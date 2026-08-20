//go:build linux

package trace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
)

// pathMax bounds a path read out of another process.
//
// `PATH_MAX`. A path longer than this cannot be passed to the syscalls being
// traced, so a run of bytes without a terminator inside it is not a path - it is
// a pointer into something that was never one, and reading further would only
// make the mistake bigger.
const pathMax = 4096

// chunk is how much is read at a time.
//
// Not the whole of pathMax at once, because the address may sit near the end of
// a mapping: a four-kilobyte read spanning into unmapped memory fails entirely
// rather than returning what it could, and most paths are shorter than one of
// these anyway.
const chunk = 256

// errUnreadable says a path could not be recovered, without saying it is absent.
//
// The distinction is the whole of I3's safety. A path this engine failed to read
// is not a path that was not read - the step opened *something* - so a caller
// must declare the observation incomplete rather than record one fewer read.
var errUnreadable = errors.New("the path argument could not be read")

// pathAt reads a NUL-terminated path out of a stopped process.
//
// No `unsafe`: `/proc/<pid>/mem` is an ordinary file whose offsets are the
// target's addresses, so this is a bounded `ReadAt` and nothing more. That it is
// possible at all is why the tracer needs no ptrace and no privilege - the
// engine is the step's parent, which is what Yama's default `ptrace_scope=1`
// asks for.
//
// **The caller must revalidate the notification after this returns.** A pid can
// be recycled between a notification arriving and this read completing, and
// nothing here can tell the difference; `stillRunning` is what turns "the read
// finished" into "the read was of the right process". Checking before instead
// would prove the target was alive a moment ago, which is the wrong side of the
// race.
func pathAt(pid uint32, addr uint64) (string, error) {
	f, err := os.Open(procRoot + "/" + strconv.FormatUint(uint64(pid), 10) + "/mem")
	if err != nil {
		// The process is gone, or this engine may not read it. Neither says
		// anything about what the path was.
		return "", fmt.Errorf("%w: open the target's memory: %w", errUnreadable, err)
	}

	defer func() { _ = f.Close() }()

	return readPathFrom(f, addr)
}

// readPathFrom is the reading, separated from where it reads.
//
// `/proc/<pid>/mem` is an `io.ReaderAt` whose offsets are addresses, and nothing
// below cares that they are. Split out so that stopping at the terminator -
// neither before it nor past it - can be asserted against a buffer, where a
// wrong answer cannot be blamed on the target process, the pid or the kernel.
func readPathFrom(r io.ReaderAt, addr uint64) (string, error) {
	var out []byte

	for len(out) < pathMax {
		buf := make([]byte, min(chunk, pathMax-len(out)))

		n, err := r.ReadAt(buf, int64(addr)+int64(len(out)))

		// A short read is still data. `ReadAt` reports `io.EOF` at the end of a
		// mapping and `EIO` past one, and in both cases the bytes it did return
		// may already contain the terminator - so what was read is examined
		// before the error is believed.
		if i := bytes.IndexByte(buf[:n], 0); i >= 0 {
			return string(append(out, buf[:i]...)), nil
		}

		out = append(out, buf[:n]...)

		if err != nil {
			if errors.Is(err, io.EOF) && n > 0 {
				continue
			}

			return "", fmt.Errorf("%w: at %#x after %d bytes: %w",
				errUnreadable, addr, len(out), err)
		}
	}

	// pathMax bytes and no terminator. This is not a long path; it is an address
	// that was never one, and a truncation would be recorded as a real read.
	return "", fmt.Errorf(
		"%w: no terminator in %d bytes at %#x, so the argument is not a path",
		errUnreadable, pathMax, addr)
}

// pathArg is the index of the argument naming a path, per syscall.
//
// The fiddly part of the whole tracer. An index one out reads a `flags` word as
// an address and produces a path made of whatever was there - no error, a
// plausible-looking string, and a prediction keyed on it. The `*at` forms take a
// directory descriptor first and the older forms do not, which is the whole of
// the difference and exactly the thing to get wrong.
//
// Asserted against the syscalls themselves rather than against a manual page:
// the test makes each call and checks the path that comes back is the one it
// passed.
func pathArg(nr int32) (int, bool) {
	i, ok := pathArgs[nr]

	return i, ok
}

// pathOf recovers the path a trapped syscall was given.
//
// Which argument holds it is per-syscall and the table is the fiddly part: an
// index one out reads a `flags` word as an address and yields a path made of
// whatever happened to be there. Every entry is asserted against the real
// syscall in readpath_linux_test.go rather than against the manual page.
func pathOf(n seccompNotif) (string, error) {
	i, ok := pathArg(n.Data.NR)
	if !ok {
		return "", fmt.Errorf("%w: syscall %d takes no path this engine knows of",
			errUnreadable, n.Data.NR)
	}

	return pathAt(n.Pid, n.Data.Args[i])
}
