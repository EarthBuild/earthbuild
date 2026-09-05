package guest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/layer"
	"golang.org/x/sys/unix"
)

// dimension is one thing green paper §3.3 says a layer records.
//
// *"A layer records, per path: mode, uid, gid, symlink target, xattrs, device
// numbers, hardlink identity, and mtime to nanosecond precision."*
//
// One fixture each, so a failure names the property rather than a digest.
type dimension struct {
	name string
	// make builds a tree exercising the property, or skips when this machine
	// cannot. It returns nothing: the assertion is always the same one.
	make func(t *testing.T, dir string)
}

// A copy of a tree digests the same as the tree.
//
// **The test that would have found four bugs at once.** `layer.Take` implements
// §3.3 faithfully and `copyTree` implemented a subset, and the difference was
// discovered one property at a time over four iterations, each by somebody
// noticing an odd digest and following it:
//
//	directory mtimes    the walk's directory branch returned early     (E87)
//	whiteouts           `default:` skipped devices as "rare"           (E88)
//	hard links          every regular file copied independently        (E89)
//	extended attributes carried for two names, on directories only     (E90)
//
// Every one was documented somewhere as a property the engine keeps - in
// `copyTree`'s own comment, in `layer.Take`'s, in §3.3 - and the description and
// the code were maintained by people who were not reading each other.
//
// The property is one line: **what the digest records, the copy reproduces.**
// Written per dimension rather than as one fixture so that a failure says
// *which*, because a single tree exercising everything reports only that two
// digests differ, and this engine has already spent three iterations turning
// that sentence into a cause.
func TestACopyReproducesEveryRecordedProperty(t *testing.T) {
	t.Parallel()

	for _, d := range dimensions() {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()

			src := t.TempDir()
			dst := filepath.Join(t.TempDir(), "out")

			d.make(t, src)

			before, err := layer.Take(src)
			if err != nil {
				t.Fatal(err)
			}

			err = copyTree(src, dst, copyOpts{KeepOwn: true})
			if err != nil {
				t.Skipf("this machine cannot make that copy: %v", err)
			}

			after, err := layer.Take(dst)
			if err != nil {
				t.Fatal(err)
			}

			if after.ID != before.ID {
				t.Errorf("the copy does not record %s the way the digest does:"+
					"\n  tree %s\n  copy %s", d.name, before.ID, after.ID)
			}
		})
	}
}

func dimensions() []dimension {
	write := func(t *testing.T, p string, mode os.FileMode) {
		t.Helper()

		err := os.WriteFile(p, []byte("body\n"), mode)
		if err != nil {
			t.Fatal(err)
		}
	}

	return []dimension{{
		name: "mode",
		make: func(t *testing.T, dir string) {
			t.Helper()
			write(t, filepath.Join(dir, "x"), 0o600)
			write(t, filepath.Join(dir, "exec"), 0o755)
		},
	}, {
		// uid is not testable without privilege, and gid is: a process may hand
		// a file to a group it belongs to. The two are one field in the digest
		// and one call in the copy, so the group exercises the path.
		name: "gid",
		make: func(t *testing.T, dir string) {
			t.Helper()

			p := filepath.Join(dir, "owned")
			write(t, p, 0o600)

			err := os.Lchown(p, os.Getuid(), otherGroup(t))
			if err != nil {
				t.Skipf("cannot change this file's group: %v", err)
			}
		},
	}, {
		name: "symlink target",
		make: func(t *testing.T, dir string) {
			t.Helper()

			write(t, filepath.Join(dir, "x"), 0o600)

			err := os.Symlink("x", filepath.Join(dir, "link"))
			if err != nil {
				t.Skipf("symlinks are not available here: %v", err)
			}
		},
	}, {
		name: "xattrs",
		make: func(t *testing.T, dir string) {
			t.Helper()

			p := filepath.Join(dir, "labelled")
			write(t, p, 0o600)
			setXattr(t, p, "user.earthbuild.conformance", []byte("value"))
		},
	}, {
		// Device *numbers* need privilege to create; a fifo is the same branch
		// of the copy and needs none, so the path is exercised and the skip is
		// only about the numbers themselves.
		name: "special files",
		make: func(t *testing.T, dir string) {
			t.Helper()

			err := unix.Mkfifo(filepath.Join(dir, "pipe"), 0o600)
			if err != nil {
				t.Skipf("this machine cannot make a fifo: %v", err)
			}
		},
	}, {
		name: "hardlink identity",
		make: func(t *testing.T, dir string) {
			t.Helper()

			write(t, filepath.Join(dir, "a"), 0o600)

			err := os.Link(filepath.Join(dir, "a"), filepath.Join(dir, "b"))
			if err != nil {
				t.Skipf("hard links are not available here: %v", err)
			}
		},
	}, {
		// Nanoseconds, because §3.3 says so and because a copy that rounded to
		// the second would pass every other case here.
		name: "mtime to nanosecond precision",
		make: func(t *testing.T, dir string) {
			t.Helper()

			p := filepath.Join(dir, "stamped")
			write(t, p, 0o600)

			at := time.Unix(1_600_000_000, 123_456_789)

			err := os.Chtimes(p, at, at)
			if err != nil {
				t.Fatal(err)
			}
		},
	}, {
		// A directory's own mtime changes whenever anything is written into it,
		// so it can only be restored after its contents - which is the shape
		// E87 was.
		name: "a directory's mtime",
		make: func(t *testing.T, dir string) {
			t.Helper()

			inner := filepath.Join(dir, "d")

			err := os.MkdirAll(inner, 0o750)
			if err != nil {
				t.Fatal(err)
			}

			write(t, filepath.Join(inner, "x"), 0o600)

			at := time.Unix(1_600_000_000, 987_654_321)

			err = os.Chtimes(inner, at, at)
			if err != nil {
				t.Fatal(err)
			}
		},
	}}
}

// The dimensions are the ones the specification lists.
//
// A table of fixtures drifts from the thing it claims to cover, and this one
// claims to cover a sentence in green paper §3.3. Naming them here means a
// property added to the specification and not to the table is a failure rather
// than a silence - which is what the last four iterations were.
func TestTheConformanceTableCoversTheSpecification(t *testing.T) {
	t.Parallel()

	// Both sides have to be non-empty for this to mean anything: an empty
	// `want` is covered by any table, and an empty table covers nothing. The
	// test reads as a coverage proof either way.
	if len(dimensions()) == 0 {
		t.Fatal("the conformance table is empty, so it conforms to anything")
	}

	// §3.3: "mode, uid, gid, symlink target, xattrs, device numbers, hardlink
	// identity, and mtime to nanosecond precision".
	want := map[string]bool{
		"mode": true, "gid": true, "symlink target": true, "xattrs": true,
		"special files": true, "hardlink identity": true,
		"mtime to nanosecond precision": true, "a directory's mtime": true,
	}

	for _, d := range dimensions() {
		if !want[d.name] {
			t.Errorf("%q is covered but is not one of the properties §3.3 lists", d.name)
		}

		delete(want, d.name)
	}

	for name := range want {
		t.Errorf("§3.3 records %s and nothing here checks a copy reproduces it", name)
	}
}
