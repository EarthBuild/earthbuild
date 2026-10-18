package guest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// COPY writes the same time on a file whether or not it arrived in a directory.
//
// `SOURCE_DATE_EPOCH` is an instruction about the whole build, and a build that
// obeyed it for `COPY --dir tree /x` and ignored it for `COPY file /x` is not
// obeying it - it is producing an image whose reproducibility depends on how
// each of its inputs happened to be spelled.
//
// The asymmetry is the same shape as the `SAVE ARTIFACT` one it followed: the
// directory arm goes through `copyTree`, which stamps every entry it writes,
// and the file arm was a second, shorter piece of copying code beside it that
// called `os.Chtimes` with the source's own time. Each looked right on its own.
//
// So this test does not assert a constant. It asserts that the two arms agree,
// which is the property that was actually broken and the one that stays broken
// if somebody adds a third arm.
func TestCopyStampsAFileAndATreeAlike(t *testing.T) {
	t.Parallel()

	const epoch = 1700000000

	// Carried in the request rather than taken from the environment, which is
	// where the clamp now comes from: the guest is a machine that outlives the
	// build that started it, so a per-build instruction it read for itself
	// would be the previous build's (E549). That also lets this run in
	// parallel, which it could not while it set a process-wide variable.
	at := time.Unix(epoch, 0)
	clamped := copyOpts{Clamp: &at}

	dir := t.TempDir()

	// One layer holding the same bytes twice: loose, and inside a directory.
	layer := filepath.Join(dir, "layers", testSrcLayer)

	err := os.MkdirAll(filepath.Join(layer, "tree"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"loose.txt", filepath.Join("tree", "held.txt")} {
		err = os.WriteFile(filepath.Join(layer, p), []byte("payload\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		// A source time that is emphatically not the clamp, so a copy that
		// simply carried the source through cannot pass by coincidence.
		old := time.Unix(1400000000, 0)

		err = os.Chtimes(filepath.Join(layer, p), old, old)
		if err != nil {
			t.Fatal(err)
		}
	}

	root := filepath.Join(dir, "root")

	err = os.MkdirAll(filepath.Join(root, "code"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{LayerDir: dir}
	h := fixedHandle{root: root}

	err = s.copyIn(h, []string{testSrcLayer}, "loose.txt", "/code/", clamped)
	if err != nil {
		t.Fatal(err)
	}

	asDir := clamped
	asDir.AsDir = true

	err = s.copyIn(h, []string{testSrcLayer}, "tree", "/code/", asDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{"a file copied into a directory", filepath.Join("code", "loose.txt")},
		{"a file copied as part of a tree", filepath.Join("code", "tree", "held.txt")},
	} {
		fi, err := os.Stat(filepath.Join(root, tc.path))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		if got := fi.ModTime().Unix(); got != epoch {
			t.Errorf("%s is stamped %d, and the clamp says %d", tc.name, got, epoch)
		}
	}
}

// And with no instruction, both arms keep the time the source had.
//
// The other half of the same property: the clamp is opt-in, so a build that did
// not ask for one must not get a rewritten timestamp from either arm. Without
// this the copy could satisfy the test above by stamping everything with a
// constant, which would break every incremental tool downstream (I8).
func TestCopyKeepsTheSourceTimeWhenNothingSaysOtherwise(t *testing.T) {
	t.Parallel()

	// No clamp in the options at all, which is what a build that has not asked
	// for one sends.
	dir := t.TempDir()
	layer := filepath.Join(dir, "layers", testSrcLayer)

	err := os.MkdirAll(filepath.Join(layer, "tree"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	want := time.Unix(1400000000, 0)

	for _, p := range []string{"loose.txt", filepath.Join("tree", "held.txt")} {
		err = os.WriteFile(filepath.Join(layer, p), []byte("payload\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		err = os.Chtimes(filepath.Join(layer, p), want, want)
		if err != nil {
			t.Fatal(err)
		}
	}

	root := filepath.Join(dir, "root")

	err = os.MkdirAll(filepath.Join(root, "code"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{LayerDir: dir}
	h := fixedHandle{root: root}

	err = s.copyIn(h, []string{testSrcLayer}, "loose.txt", "/code/", copyOpts{})
	if err != nil {
		t.Fatal(err)
	}

	err = s.copyIn(h, []string{testSrcLayer}, "tree", "/code/", copyOpts{AsDir: true})
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		filepath.Join("code", "loose.txt"),
		filepath.Join("code", "tree", "held.txt"),
	} {
		fi, err := os.Stat(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}

		if !fi.ModTime().Equal(want) {
			t.Errorf("%s is stamped %v, and its source said %v", p, fi.ModTime(), want)
		}
	}
}

var _ core.Handle = fixedHandle{}
