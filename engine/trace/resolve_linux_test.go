//go:build linux

package trace

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

// A relative path is resolved against the descriptor it was given.
//
// This is the case that makes the tracer usable at all. A compiler run by a step
// opens `include/config.h` relative to a directory it holds open, and recording
// that string would key the step on a name that means a different file in the
// next build - which is not a weaker observation than the absolute path, it is a
// wrong one, and the false hit I3 exists to forbid.
//
// Provoked with a real descriptor rather than by changing directory, so the
// resolution being tested is the `/proc/<pid>/fd/<n>` one and not the working
// directory falling into place by luck.
func TestARelativePathIsResolvedAgainstItsDescriptor(t *testing.T) {
	dir := t.TempDir()

	const name = "child-7b2e.txt"

	err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	dirfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = unix.Close(dirfd) }()

	seen := watch(t, func() {
		fd, err := unix.Openat(dirfd, name, unix.O_RDONLY, 0)
		if err == nil {
			_ = unix.Close(fd)
		}
	})

	want := filepath.Join(dir, name)

	if !seen.saw(unix.SYS_OPENAT, want) {
		t.Errorf("openat with a relative name was not resolved to %q"+
			"\n  seen: %v\n  failures: %v",
			want, seen.pathsFor(unix.SYS_OPENAT), seen.failures(unix.SYS_OPENAT))
	}

	// And the bare name must not have been recorded, which is the failure this
	// resolution exists to prevent rather than merely an untidy result.
	if seen.saw(unix.SYS_OPENAT, name) {
		t.Errorf("the bare relative name %q was recorded; it names a different"+
			" file in a different step", name)
	}
}

// AT_FDCWD resolves against the target's working directory.
//
// The descriptor arrives as a 64-bit word and `AT_FDCWD` is -100, so reading it
// unsigned gives 0xffffffffffffff9c and a lookup of `/proc/<pid>/fd/4294967196`
// - a descriptor no process has. The failure is a resolution error rather than a
// wrong path, which is the safe direction and still a lost observation on every
// ordinary relative open.
func TestAtFdcwdResolvesAgainstTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()

	const name = "cwd-relative-4c9d.txt"

	err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Process-wide, so this test cannot be parallel - and neither can it be,
	// since it installs a filter.
	// t.Chdir puts the working directory back when the test ends, which is
	// what the manual Getwd-and-restore pair here used to do by hand.
	t.Chdir(dir)

	seen := watch(t, func() {
		fd, openatErr := unix.Openat(unix.AT_FDCWD, name, unix.O_RDONLY, 0)
		if openatErr == nil {
			_ = unix.Close(fd)
		}
	})

	// Through EvalSymlinks because a temporary directory is under /tmp, which is
	// a symlink on some systems - and /proc/<pid>/cwd reports the resolved one.
	// Comparing against the unresolved name would fail for a reason that has
	// nothing to do with the code under test.
	want, err := filepath.EvalSymlinks(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}

	if !seen.saw(unix.SYS_OPENAT, want) {
		t.Errorf("a relative openat with AT_FDCWD was not resolved to %q"+
			"\n  seen: %v\n  failures: %v",
			want, seen.pathsFor(unix.SYS_OPENAT), seen.failures(unix.SYS_OPENAT))
	}
}

// An absolute path is left alone, apart from being cleaned.
func TestAnAbsolutePathIsUnchanged(t *testing.T) {
	t.Parallel()

	// Any traced syscall: an absolute path is returned before the descriptor is
	// looked at, so which one it is cannot matter - and asserting that with a
	// call that *does* take a descriptor is the stronger version.
	n := seccompNotif{Data: seccompData{NR: unix.SYS_OPENAT}}

	got, err := resolve(uint32(os.Getpid()), n, "/a/b/../c")
	if err != nil {
		t.Fatal(err)
	}

	if got != "/a/c" {
		t.Errorf("resolve gave %q, want %q", got, "/a/c")
	}
}

// A path relative to an unlinked directory is refused, not invented.
//
// The kernel appends ` (deleted)` to a `/proc` link naming something unlinked,
// unquoted and unescaped. Joining a name onto that produces a path with a
// parenthetical in the middle of it - a plausible-looking string naming nothing
// that ever existed, which is worse than no observation because it would be
// recorded as one.
//
// The link resolution is handed in, because the case cannot be provoked: the
// kernel decides when it says `(deleted)`, and a test racing an unlink to catch
// it would be flaky in the direction of passing. The first version of this test
// asserted things about `filepath.Join` and the constant, never reached the code
// at all, and stayed green when the check was deleted (E208).
func TestAPathUnderAnUnlinkedDirectoryIsRefused(t *testing.T) {
	t.Parallel()

	gone := func(string) (string, error) { return "/tmp/build-dir" + deletedSuffix, nil }

	n := seccompNotif{Data: seccompData{NR: unix.SYS_OPENAT}}

	_, err := baseDirVia(gone, uint32(os.Getpid()), n)
	if !errors.Is(err, errUnreadable) {
		t.Errorf("a relative path under an unlinked directory gave %v;"+
			" it must be refused, since joining onto %q names nothing that"+
			" ever existed", err, "/tmp/build-dir"+deletedSuffix)
	}
}

// A directory that is merely *named* like a deleted one still resolves.
//
// The kernel does not quote or escape the suffix, so a directory genuinely
// called `build (deleted)` is indistinguishable from an unlinked `build`. The
// ambiguity is the kernel's and this engine takes the safe side of it - but the
// cost is worth pinning, because it is a real directory whose steps will never
// be observed, and a future reader should find that written down rather than
// deduce it from a build that never gets an L2 hit.
func TestADirectoryNamedLikeADeletedOneIsAlsoRefused(t *testing.T) {
	t.Parallel()

	odd := func(string) (string, error) { return "/src/build (deleted)", nil }

	n := seccompNotif{Data: seccompData{NR: unix.SYS_OPENAT}}

	_, err := baseDirVia(odd, uint32(os.Getpid()), n)
	if !errors.Is(err, errUnreadable) {
		t.Error("a directory named like a deleted one was accepted;" +
			" the two are indistinguishable and the safe side is refusal")
	}
}

// The base is read from the descriptor the call carries, not the working
// directory.
//
// AT_FDCWD is -100 arriving in a 64-bit word, so reading it unsigned gives
// 0xffffffffffffff9c and a lookup of a descriptor no process has. The two paths
// through baseDir are asserted by which link each one asks for.
func TestTheDescriptorDecidesWhichLinkIsRead(t *testing.T) {
	t.Parallel()

	pid := uint32(os.Getpid())
	proc := "/proc/" + strconv.FormatUint(uint64(pid), 10)

	// A variable, because Go folds `uint32(int32(-100))` at compile time and
	// refuses it: a negative constant does not convert to an unsigned type. The
	// kernel has no such scruples - it delivers the descriptor in the low 32
	// bits of a 64-bit word and the sign extension is the whole point here.
	fdcwd := int32(unix.AT_FDCWD)

	for _, tc := range []struct {
		name string
		arg  uint64
		want string
	}{
		{"a real descriptor", 7, proc + "/fd/7"},
		// As the kernel delivers it: the descriptor occupies the low 32
		// bits of a 64-bit word, so AT_FDCWD arrives sign-extended and a
		// reader treating it as unsigned looks for /proc/<pid>/fd/4294967196.
		{"AT_FDCWD", uint64(uint32(fdcwd)), proc + "/cwd"},
	} {
		var asked string

		spy := func(link string) (string, error) {
			asked = link

			return "/somewhere", nil
		}

		n := seccompNotif{Data: seccompData{NR: unix.SYS_OPENAT}}
		n.Data.Args[0] = tc.arg

		_, err := baseDirVia(spy, pid, n)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		if asked != tc.want {
			t.Errorf("%s: read %q, want %q", tc.name, asked, tc.want)
		}
	}
}
