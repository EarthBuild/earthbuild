package guest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// startFacts is what is known about a step that could not be started.
//
// Gathered rather than reasoned about. Two sightings of `fork/exec /bin/sh:
// operation not permitted` have produced three hypotheses and no evidence,
// because the message names the binary - which, when the error is EPERM, is the
// one thing that is not the problem.
type startFacts struct {
	// Euid is who tried.
	Euid int
	// Root is the filesystem the step was to run against.
	Root string
	// Binary is the path asked for, and BinaryMode what was found at it inside
	// Root - a mode string, or a short phrase saying why there is none.
	Binary     string
	BinaryMode string
	// Confined says whether chroot and the namespace flags were applied, which
	// decides whether they are candidates for having been refused.
	Confined bool
	// Caps is the process's effective capability mask, where the system reports
	// one. Empty elsewhere.
	Caps string
}

// startHint explains a step that could not be started at all.
//
// Empty for anything that is not one of the three kernel answers worth telling
// apart, because a hint under every failure is a hint nobody reads.
func startHint(err error, f startFacts) string {
	switch {
	case errors.Is(err, syscall.EPERM):
		return permHint(f)

	case errors.Is(err, syscall.ENOENT):
		return fmt.Sprintf(
			"\n  %s: %s"+
				"\n  the image does not have this program - a shell is not guaranteed to exist"+
				"\n  in a base image, and `RUN` without one needs the exec form",
			f.Binary, f.BinaryMode)

	case errors.Is(err, syscall.EACCES):
		return fmt.Sprintf(
			"\n  %s is %s and this process is euid %d, so it may not be executed",
			f.Binary, f.BinaryMode, f.Euid)

	default:
		return ""
	}
}

// permHint is the EPERM case, which is the one that has been seen and the one
// the message is least helpful about.
func permHint(f startFacts) string {
	var b strings.Builder

	b.WriteString("\n  the kernel refused to start it, which is not a statement about the binary")

	if f.BinaryMode != "" {
		fmt.Fprintf(&b, "\n  %s is %s, so the binary is there and executable", f.Binary, f.BinaryMode)
	}

	if !f.Confined {
		b.WriteString("\n  no isolation was applied to this step, so chroot and the namespace" +
			"\n  flags are not what was refused")

		return b.String()
	}

	b.WriteString("\n  this step is confined: it chroots and unshares mount, pid, uts and ipc," +
		"\n  and each of those returns EPERM without the capability for it")

	fmt.Fprintf(&b, "\n  euid %d", f.Euid)

	if f.Caps != "" {
		fmt.Fprintf(&b, ", %s", f.Caps)
	}

	return b.String()
}

// binaryMissing is what a start hint says of a command that is not in the root.
//
// Written here and read by the tests that assert the hint - the one phrase in
// this struct a caller matches on, because it is the difference between "your
// image lacks this program" and every other reason a step failed to start.
const binaryMissing = "not found"

// collectStartFacts looks at what is there, once, when a step has already
// failed to start.
//
// Cheap and best-effort: everything it cannot answer is left empty rather than
// guessed at, because a diagnostic that invents a fact is worse than one that
// is short.
func collectStartFacts(argv []string, root string, confined bool) startFacts {
	f := startFacts{Euid: os.Geteuid(), Root: root, Confined: confined}

	if len(argv) == 0 {
		return f
	}

	f.Binary = argv[0]

	// Inside the root, because that is where the step would have run - the same
	// path on the guest's own filesystem is a different file or none.
	at := filepath.Join(root, filepath.Clean("/"+f.Binary))

	fi, err := os.Lstat(at)

	switch {
	case err != nil:
		f.BinaryMode = binaryMissing

	case fi.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(at)
		if err != nil {
			f.BinaryMode = "a symlink that cannot be read"

			break
		}

		f.BinaryMode = "a symlink to " + target

	default:
		f.BinaryMode = fi.Mode().String()
	}

	f.Caps = effectiveCaps()

	return f
}

// effectiveCaps reads the process's capability mask where the system publishes
// one, and returns the empty string where it does not.
func effectiveCaps() string {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return ""
	}

	for line := range strings.SplitSeq(string(b), "\n") {
		if strings.HasPrefix(line, "CapEff:") {
			return strings.Join(strings.Fields(line), " ")
		}
	}

	return ""
}
