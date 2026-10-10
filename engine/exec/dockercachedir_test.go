package exec

import (
	"path/filepath"
	"strings"
	"testing"
)

// A shared cache's directory is derived from its name, and the name is not
// trusted here.
//
// **The interpreter already checks it** (E358), and that check protects the
// author: it names the file and the flag, at the line that wrote it. This one
// protects the machine, and they are not the same job.
//
// A step assignment arrives from a driver this worker did not write (A5, C.3).
// `DockerCache` crosses that wire, so by the time the executor composes a path
// from it the value is a **peer's claim**, and `../../..` in it is a directory
// outside the store that a build step then gets a daemon writing into (E360).
func TestACacheDirectoryIsNotComposedFromAPeersClaim(t *testing.T) {
	t.Parallel()

	for _, id := range []string{
		"../escape", "a/b", "/absolute", ".", "..", "with space", "",
		strings.Repeat("x", 200), "a\x00b",
	} {
		_, err := dockerCacheDir("/store", id)
		if err == nil {
			t.Errorf("a daemon cache called %q was given a directory", id)
		}
	}
}

// A name that passed the interpreter gets a directory under the store.
func TestACacheDirectoryIsUnderTheStore(t *testing.T) {
	t.Parallel()

	got, err := dockerCacheDir("/store", "layers")
	if err != nil {
		t.Fatalf("%v", err)
	}

	if !strings.HasPrefix(filepath.Clean(got), filepath.Clean("/store")+"/") {
		t.Errorf("a cache directory %q is not under the store it belongs to", got)
	}
}

// Two names are two caches, and one name is one.
//
// The whole of what `--cache-id` promises: blocks naming the same cache see each
// other's images, and blocks naming different ones do not.
func TestOneNameIsOneCacheAndTwoAreTwo(t *testing.T) {
	t.Parallel()

	one, err := dockerCacheDir("/store", "shared")
	if err != nil {
		t.Fatalf("%v", err)
	}

	again, err := dockerCacheDir("/store", "shared")
	if err != nil {
		t.Fatalf("%v", err)
	}

	other, err := dockerCacheDir("/store", "private")
	if err != nil {
		t.Fatalf("%v", err)
	}

	if one != again {
		t.Errorf("one name gave two directories: %q and %q", one, again)
	}

	if one == other {
		t.Errorf("two names gave one directory: %q", one)
	}
}
