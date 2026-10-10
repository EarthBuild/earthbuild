package layer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A capture narrowed to declared paths holds those and nothing else.
//
// **The point is not a smaller layer, it is a stabler key.** A step that says
// what it produces stops carrying what it merely disturbed - a fingerprint
// file, a log, a timestamp in an intermediate - so two runs that produce the
// same artefact become the same layer, without the tool having been fixed.
func TestACaptureCanBeNarrowedToWhatWasDeclared(t *testing.T) {
	t.Parallel()

	build := func(noise string) layer.Capture {
		t.Helper()

		dir := t.TempDir()

		for p, body := range map[string]string{
			"target/release/app":  "the binary",
			"target/.fingerprint": noise,
			"target/debug/junk":   noise,
			"logs/build.log":      noise,
		} {
			at := filepath.Join(dir, p)
			if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(at, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		c, err := layer.TakeIgnoring(dir, layer.Only([]string{"target/release/app"}))
		if err != nil {
			t.Fatal(err)
		}

		return c
	}

	first, second := build("one"), build("two")

	if first.Content != second.Content {
		t.Errorf("two runs differing only outside what was declared produced"+
			"\n  %v and %v - which is the nondeterminism declaring outputs exists"+
			"\n  to keep out of the key", first.Content, second.Content)
	}

	if first.Bytes == 0 {
		t.Error("the declared path was not captured at all")
	}
}

// Declaring nothing keeps everything, which is what every step does today.
func TestDeclaringNothingKeepsEverything(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	whole, err := layer.Take(dir)
	if err != nil {
		t.Fatal(err)
	}

	same, err := layer.TakeIgnoring(dir, layer.Only(nil))
	if err != nil {
		t.Fatal(err)
	}

	if whole.Content != same.Content {
		t.Errorf("declaring nothing changed the capture: %v against %v",
			same.Content, whole.Content)
	}
}

// A declared directory brings what is under it.
func TestADeclaredDirectoryBringsItsContents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, p := range []string{"keep/a.txt", "keep/deep/b.txt", "drop/c.txt"} {
		at := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(at, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	c, err := layer.TakeIgnoring(dir, layer.Only([]string{"keep"}))
	if err != nil {
		t.Fatal(err)
	}

	m, err := layer.ManifestIn(dir, layer.IDMap{}, layer.IDMap{})
	if err != nil {
		t.Fatal(err)
	}

	_ = m

	if c.Bytes == 0 {
		t.Fatal("nothing was captured")
	}

	// The dropped file must not be there: compare against a tree that never
	// had it.
	bare := t.TempDir()

	for _, p := range []string{"keep/a.txt", "keep/deep/b.txt"} {
		at := filepath.Join(bare, p)
		if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(at, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := layer.Take(bare)
	if err != nil {
		t.Fatal(err)
	}

	if c.Content != want.Content {
		t.Errorf("a narrowed capture is %v and the same tree without the"+
			" undeclared file is %v", c.Content, want.Content)
	}
}

// A sibling sharing a prefix is not inside a declared output.
//
// **The prefix bug, which a path comparison invites.** `target` and
// `target-old` share five characters and nothing else, so a rule matching on
// the string alone captures a directory the step never declared - and the
// author who wrote `--output=target` gets whatever else happens to start that
// way, silently, and back into the key.
//
// The separator is the whole of the fix and the whole of the test.
func TestASiblingSharingAPrefixIsNotInside(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, p := range []string{"target/app", "target-old/stale", "targetish"} {
		at := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(at, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := layer.TakeIgnoring(dir, layer.Only([]string{"target"}))
	if err != nil {
		t.Fatal(err)
	}

	// The same tree holding only what was declared.
	bare := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bare, "target"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(bare, "target", "app"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	want, err := layer.Take(bare)
	if err != nil {
		t.Fatal(err)
	}

	if got.Content != want.Content {
		t.Errorf("declaring `target` captured %v where `target` alone is %v"+
			"\n  a sibling whose name merely starts the same way was taken as"+
			"\n  being inside it", got.Content, want.Content)
	}
}
