package fsclone_test

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fsclone"
)

// What comes out is what went in, whichever way the kernel did it.
//
// The saving is invisible from here - a reflink and a copy hold the same bytes,
// which is the point - so what a test can hold the implementation to is that
// the destination is right and the report is honest.
func TestACloneCopiesEveryByte(t *testing.T) {
	t.Parallel()

	want := make([]byte, 1<<20)
	if _, err := rand.Read(want); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "src")

	if err := os.WriteFile(src, want, 0o600); err != nil {
		t.Fatal(err)
	}

	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}

	defer in.Close()

	dst := filepath.Join(dir, "dst")

	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}

	defer out.Close()

	if !fsclone.Range(in, out, int64(len(want))) {
		if runtime.GOOS == "linux" {
			t.Fatal("the kernel declined a same-filesystem copy of a regular file")
		}

		t.Skip("no kernel-side copy on ", runtime.GOOS)
	}

	if err := out.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("the clone holds %d bytes of a wanted %d", len(got), len(want))
	}
}

// A size larger than the source is refused rather than reported as a whole
// copy: a short clone taken for a complete one is a truncated layer.
func TestAShortSourceIsNotReportedWhole(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")

	if err := os.WriteFile(src, []byte("four"), 0o600); err != nil {
		t.Fatal(err)
	}

	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}

	defer in.Close()

	out, err := os.Create(filepath.Join(dir, "dst"))
	if err != nil {
		t.Fatal(err)
	}

	defer out.Close()

	if fsclone.Range(in, out, 4096) {
		t.Error("a source of 4 bytes was reported as a copy of 4096")
	}
}
