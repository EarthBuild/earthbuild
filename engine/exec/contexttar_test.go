package exec

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ignore"
)

// TestWhatTheGuestReceivesForACopiedContext.
//
// **The tar is the interface, and nothing asserted it.** A `COPY` stages the
// context into a directory and then packs that directory; the guest never sees
// the directory, only the tar. `TestAStagedContextLeavesOutWhatTheIgnoreFileNames`
// checks the staging, which is the step in the middle - so a change to *how* the
// tar is produced could alter what is in it and pass.
//
// That change is a live proposal. `COPY` costs 0.73ms a file and two thirds of
// it is the host walking the tree twice: once to copy into staging, once to read
// it back for the tar. Packing straight from the context would halve it, and
// would move the ignore-file selection from `copyDirExcluding` into the tar walk
// - which is to say it would move the thing that decides what enters a build
// (E829).
//
// So this pins the answer rather than the route: given a context and an ignore
// file, these are the entries the guest gets. An implementation that produces
// the same set by a shorter path passes; one that quietly widens or narrows what
// is copied does not.
func TestWhatTheGuestReceivesForACopiedContext(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.WriteFile(filepath.Join(root, ".earthlyignore"),
		[]byte("**/skipme-*\nexcluded\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{
		"src/keep.txt",
		"src/nested/also-kept.txt",
		"src/skipme-1/inside.txt",
		"excluded/gone.txt",
	} {
		p := filepath.Join(root, rel)
		mkErr := os.MkdirAll(filepath.Dir(p), 0o750)
		if mkErr != nil {
			t.Fatal(mkErr)
		}

		wErr := os.WriteFile(p, []byte("x"), 0o600)
		if wErr != nil {
			t.Fatal(wErr)
		}
	}

	staged := filepath.Join(t.TempDir(), "staged")

	err = copyDirExcluding(filepath.Join(root, "src"), staged,
		ignore.For(root, filepath.Join(root, "src")))
	if err != nil {
		t.Fatal(err)
	}

	tarball := filepath.Join(t.TempDir(), "context.tar")

	err = packInto(staged, tarball)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(tarball)
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	var names []string

	tr := tar.NewReader(f)

	for {
		h, nErr := tr.Next()
		if nErr == io.EOF {
			break
		}

		if nErr != nil {
			t.Fatalf("reading the packed context: %v", nErr)
		}

		names = append(names, strings.TrimPrefix(h.Name, "./"))
	}

	sort.Strings(names)

	got := strings.Join(names, " ")

	// Directories are carried as well as files: a tar that lists only regular
	// files unpacks into a tree with default modes, and the mode of a directory
	// a build was given is part of what it was given.
	for _, want := range []string{"keep.txt", "nested/also-kept.txt"} {
		if !strings.Contains(got, want) {
			t.Errorf("the guest does not receive %q\n  entries: %s", want, got)
		}
	}

	for _, unwanted := range []string{"skipme-1", "excluded", ".earthlyignore"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the guest receives %q, which the ignore file excludes"+
				"\n  entries: %s", unwanted, got)
		}
	}
}
