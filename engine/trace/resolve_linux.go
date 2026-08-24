//go:build linux

package trace

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// deletedSuffix is what the kernel appends to a link naming an unlinked file.
//
// `/proc/<pid>/fd/3 -> /tmp/x (deleted)`. It is not part of the path and it is
// not quoted or escaped, so a file genuinely called `x (deleted)` is
// indistinguishable from an unlinked `x`. The ambiguity is the kernel's; what
// this engine can do is refuse rather than record a path with a parenthetical
// stuck on the end, which would key a step on a file name that never existed.
const deletedSuffix = " (deleted)"

// resolve turns the path a syscall was given into one that means the same thing
// tomorrow.
//
// A relative path is not merely less useful than an absolute one - it is
// **wrong** to record. `lib/foo.h` names different files in different steps, so
// an observation carrying it would match a base where the same relative name
// resolves elsewhere, which is exactly the false hit I3 forbids (§3.4).
//
// Three cases, and the first is the common one:
//
//   - absolute: nothing to do;
//   - relative with `AT_FDCWD`, or a syscall with no descriptor at all: against
//     the target's working directory, `/proc/<pid>/cwd`;
//   - relative with a descriptor: against what that descriptor names,
//     `/proc/<pid>/fd/<n>`.
//
// **Which argument holds the descriptor is derived, not tabulated.** Every
// traced syscall whose path is argument 1 is an `*at` form, and every `*at` form
// takes its directory descriptor as argument 0 - that is what the suffix means.
// A second table would be a second thing to get out of step with the first.
func resolve(pid uint32, n seccompNotif, path string) (string, error) {
	if strings.HasPrefix(path, "/") {
		return filepath.Clean(path), nil
	}

	base, err := baseDir(pid, n)
	if err != nil {
		return "", err
	}

	// `AT_EMPTY_PATH`: the empty path names the descriptor itself, which is the
	// directory just resolved. `fstatat(fd, "", &st, AT_EMPTY_PATH)` is how a
	// program stats something it already holds open.
	if path == "" {
		return base, nil
	}

	return filepath.Join(base, path), nil
}

// baseDir is the directory a relative path in this call is relative to.
func baseDir(pid uint32, n seccompNotif) (string, error) {
	return baseDirVia(os.Readlink, pid, n)
}

// baseDirVia is baseDir with the link resolution handed in.
//
// Split so the `(deleted)` case can be *exercised* rather than described. It
// cannot be provoked: the kernel decides when a `/proc` link gains that suffix,
// and a test racing an unlink to catch it would be flaky in the direction of
// passing. The first version of that test asserted things about `filepath.Join`
// and the constant instead, never reached this function, and stayed green when
// the check was deleted (E208).
func baseDirVia(
	readlink func(string) (string, error), pid uint32, n seccompNotif,
) (string, error) {
	proc := procRoot + "/" + strconv.FormatUint(uint64(pid), 10)

	link := proc + "/cwd"

	// Argument 1 means an `*at` form, which carries its descriptor in argument
	// 0. Anything else is one of the older calls, which have only the working
	// directory to go on.
	if i, ok := pathArg(n.Data.NR); ok && i == 1 {
		// Signed: the descriptor arrives as a 64-bit word and AT_FDCWD is -100,
		// so reading it unsigned gives 0xffffffffffffff9c and a lookup of a
		// descriptor no process has.
		//nolint:gosec // the narrowing is the point, see above
		if fd := int32(uint32(n.Data.Args[0])); fd != unix.AT_FDCWD {
			link = proc + "/fd/" + strconv.FormatInt(int64(fd), 10)
		}
	}

	dir, err := readlink(link)
	if err != nil {
		return "", fmt.Errorf("%w: read %s: %w", errUnreadable, link, err)
	}

	// An unlinked directory. The step is working relative to something with no
	// name any more, so there is no path to record and pretending otherwise
	// would record one that never existed.
	if strings.HasSuffix(dir, deletedSuffix) {
		return "", fmt.Errorf(
			"%w: %s names an unlinked directory (%s), so a path relative to it"+
				" has no name to record", errUnreadable, link, dir)
	}

	return dir, nil
}

// observedPath is the whole of turning a notification into a path worth keying
// on: read it out of the target, then make it absolute.
func observedPath(n seccompNotif) (string, error) {
	raw, err := pathOf(n)
	if err != nil {
		return "", err
	}

	return resolve(n.Pid, n, raw)
}
