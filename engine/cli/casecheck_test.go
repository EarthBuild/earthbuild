package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The layer store's case sensitivity is asked of the store, not the platform.
//
// A Mac may have a case-sensitive volume and a Linux machine a case-insensitive
// mount; the question is about this directory. It matters because a step's
// filesystem is an overlay whose *lower* layers come from this store and whose
// upper layer lives in the sandbox - so on a case-insensitive store, `/BIN/SH`
// resolves and a file the step writes as `Foo` does not answer to `foo`. A build
// then behaves one way for files from an image and another for files it made,
// which is how `examples/next-js` panics inside a TypeScript compiler that
// probes exactly that.
func TestCaseSensitivityIsAskedOfTheStore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Whatever this filesystem is, the probe must agree with it.
	err := os.WriteFile(filepath.Join(dir, testProbe), []byte("l"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	upper := filepath.Join(dir, "PROBE")
	err = os.WriteFile(upper, []byte("u"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dir, testProbe))
	if err != nil {
		t.Fatal(err)
	}

	want := string(b) == "l"

	if got := caseSensitiveStore(dir); got != want {
		t.Errorf("the store reports case-sensitive=%v, want %v", got, want)
	}
}

// The probe leaves nothing behind, because it runs against a directory the user
// keeps.
func TestTheProbeCleansUpAfterItself(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	caseSensitiveStore(dir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 0 {
		t.Errorf("the probe left %d files in the build cache", len(entries))
	}
}

// A directory that cannot be written to is not reported as either: an answer
// invented here would be worse than none.
func TestAnUnwritableStoreIsNotGuessedAt(t *testing.T) {
	t.Parallel()

	if caseSensitiveStore(filepath.Join(t.TempDir(), "does-not-exist")) {
		t.Error("a store that could not be probed was reported as case-sensitive")
	}
}

// The note is about every directory an image is unpacked into, not just one.
//
// The image cache and the layer store are the same directory by default, and
// `EARTH_IMAGE_CACHE_DIR` separates them - which is a sensible thing to do,
// because an image is identical for every project on the machine while a layer
// store belongs to one build cache. Separate them and only the store was
// probed.
//
// The failure that follows names the wrong thing. `earthbuild/dind` cannot be
// unpacked onto a case-insensitive volume, and with the store moved to a
// case-sensitive one the build still failed - saying "a case-sensitive volume
// for the build cache is the way round it" while the build cache already was
// one. A diagnosis that names a directory that is not at fault is worse than
// none.
func TestTheNoteCoversTheImageCacheToo(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	images := t.TempDir()

	if caseSensitiveStore(store) {
		t.Skip("this filesystem is case-sensitive, so there is nothing to warn about")
	}

	var out strings.Builder

	warnCaseInsensitive(&out,
		cacheDir{path: store, env: testCacheDirEnv},
		cacheDir{path: images, env: "EARTH_IMAGE_CACHE_DIR"})

	if !strings.Contains(out.String(), images) {
		t.Errorf("the note does not mention the image cache:\n%s", out.String())
	}

	if !strings.Contains(out.String(), store) {
		t.Errorf("the note does not mention the store:\n%s", out.String())
	}
}

// One directory named once, when the two are the same - which is the default.
func TestTheNoteDoesNotSayItTwice(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if caseSensitiveStore(dir) {
		t.Skip("this filesystem is case-sensitive, so there is nothing to warn about")
	}

	var out strings.Builder

	warnCaseInsensitive(&out,
		cacheDir{path: dir, env: testCacheDirEnv},
		cacheDir{path: dir, env: "EARTH_IMAGE_CACHE_DIR"})

	if n := strings.Count(out.String(), "case-insensitive filesystem"); n != 1 {
		t.Errorf("one directory produced %d notes:\n%s", n, out.String())
	}
}

// The variable the note tells you to set is the variable the engine reads.
//
// The note ends in a command the reader runs, so the name in that command and
// the name in the `os.Getenv` that acts on it are one fact written in two
// places. Diverge them and the remedy silently does nothing: the reader exports
// a variable nobody looks at, the build behaves exactly as before, and the note
// prints again.
//
// Not hypothetical. The note told people to set EARTH_CACHE_DIR while warning
// about the image cache, which EARTH_IMAGE_CACHE_DIR moves - so following it
// changed nothing for the directory that was at fault (E27).
func TestTheNoteNamesTheVariableTheEngineReads(t *testing.T) {
	for _, tc := range []struct {
		env  string
		read func() (string, error)
	}{
		{envCacheDir, storeDir},
		{envImageCacheDir, imageCacheDir},
	} {
		t.Run(tc.env, func(t *testing.T) {
			want := t.TempDir()

			t.Setenv(tc.env, want)

			got, err := tc.read()
			if err != nil {
				t.Fatal(err)
			}

			if got != want {
				t.Errorf("setting %s put the directory at %q, want %q", tc.env, got, want)
			}
		})
	}
}
