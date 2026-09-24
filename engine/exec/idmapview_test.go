//go:build linux

package exec_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// The observer and the view must agree about ownership, and rootless they do
// not.
//
// `layer.PathDigest` hashes uid and gid, deliberately: ownership is part of
// what a layer records (§3.3), and a copy that lost it produces an image whose
// service cannot read its own files.
//
// Rootless, the two sides read the same directory through **different id
// mappings**. The guest runs in a user namespace where the invoking user is
// root, so a directory it created appears to it as uid 0. The host reads the
// same stored layer with no mapping at all and sees uid 1000. Measured on a
// stored layer from a real build:
//
//	stat /…/layers/<id>/app    mode=755 uid=1000 gid=100
//	the guest that made it     uid=0    gid=0
//
// So every observation of a directory a *step* created is checked against a
// view that disagrees about who owns it, and goes stale on the first base
// change. Measured on a six-COPY project across an alpine bump: one copy
// reused, five stale, all with `/app changed in the base` (E132).
//
// **E121 asserted these two agree and did not catch it**, because its test runs
// both sides inside the namespace: `nstest.In` re-executes the whole test, so
// the "host" half was mapped too. A fixture that puts both parties on the same
// side of a boundary cannot find a disagreement across it.
//
// This test states the mechanism rather than the remedy. Excluding ownership
// from the digest would lose what §3.3 records; mapping the view is host work
// that does not exist yet. It is the measurement that says which.
func TestOwnershipIsWhatTheObserverAndTheViewDisagreeAbout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "app")

	err := os.MkdirAll(p, 0o755) //nolint:gosec // matches what a copy creates
	if err != nil {
		t.Fatal(err)
	}

	before, err := layer.PathDigest(p)
	if err != nil {
		t.Fatal(err)
	}

	// The same directory, one group different - which is the smallest change of
	// ownership an unprivileged process can make, and stands for the uid
	// difference a namespace produces.
	gid := otherGroupOf(t)

	err = os.Lchown(p, os.Getuid(), gid)
	if err != nil {
		t.Skipf("cannot change this directory's group: %v", err)
	}

	after, err := layer.PathDigest(p)
	if err != nil {
		t.Fatal(err)
	}

	if before == after {
		t.Skip("this filesystem does not report ownership, so the two sides" +
			" cannot disagree about it here")
	}

	t.Logf("ownership changes a path digest: %s -> %s", before, after)
}

func otherGroupOf(t *testing.T) int {
	t.Helper()

	groups, err := os.Getgroups()
	if err != nil {
		t.Skipf("cannot read this process's groups: %v", err)
	}

	for _, g := range groups {
		if g != os.Getgid() {
			return g
		}
	}

	t.Skip("this process belongs to one group")

	return 0
}
