package exec

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No entry in a packed context names an absolute path.
//
// `packContextDirect` builds its sub-path as `filepath.Clean("/" + arg)`, which
// is a containment idiom: a leading slash means `..` cannot climb out of the
// context root. `selectedUnder` then walked that same value upwards to add the
// parent directories staging used to create, and emitted them as it found them
// - with the leading slash still on.
//
// The unpacker refuses an absolute entry, and is right to: it is how an archive
// escapes the tree it is unpacked into. So a `COPY --dir inputgraph/*.go
// inputgraph/` in this repository's own Earthfile failed the whole build with
// `unpack-layer: layer entry "/inputgraph/" names an absolute path`, naming
// neither the COPY nor the context (E848).
//
// Found by running the test suite locally rather than in CI, which is what this
// engine exists to make possible.
func TestAPackedContextNamesNothingAbsolute(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "inputgraph", "testdata"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(root, "inputgraph", "testdata", "x.go"), []byte("package x\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	at := filepath.Join(t.TempDir(), "context.tar")

	// The sub-path exactly as packContextDirect builds it.
	err = packContextInto(root, filepath.Clean("/inputgraph/testdata"), nil, at)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(at)
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	var (
		seen  int
		names []string
	)

	r := tar.NewReader(f)

	for {
		h, err := r.Next()
		if err != nil {
			break
		}

		seen++

		names = append(names, h.Name)

		if strings.HasPrefix(h.Name, "/") {
			t.Errorf("entry %q names an absolute path, which the unpacker refuses", h.Name)
		}
	}

	if seen == 0 {
		t.Fatal("the archive is empty, so this asserts nothing")
	}

	// The parent directory is still carried: dropping it would trade one bug
	// for a context missing the directories its files live in.
	var hasParent bool

	for _, n := range names {
		if strings.TrimSuffix(n, "/") == "inputgraph" {
			hasParent = true
		}
	}

	if !hasParent {
		t.Errorf("the parent directory is missing from %v", names)
	}
}
