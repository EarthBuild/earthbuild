package image_test

import (
	"os"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestAPinIsRememberedBetweenBuilds.
//
// **A no-op build is 96% resolving tags.** `plan` is 0.664s of a 0.69s build
// with nothing to do, and all of it is one token exchange and one manifest fetch
// per reference, over the network, before a single step runs (E550, E703). The
// answers do not change between two builds a minute apart, and nothing
// remembered them: the token cache is per process and dies with it.
//
// Across processes, so it goes on disk beside the challenges - which is where
// "who issues tokens for this registry" already lives, and for the same reason.
func TestAPinIsRememberedBetweenBuilds(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pins := image.NewPins(dir, time.Hour)

	if _, ok := pins.Get("alpine:3.21", "linux/arm64"); ok {
		t.Fatal("an empty cache answered")
	}

	pins.Put("alpine:3.21", "linux/arm64", "alpine@sha256:aaa")

	// A different process, same directory: this is the whole point.
	got, ok := image.NewPins(dir, time.Hour).Get("alpine:3.21", "linux/arm64")
	if !ok {
		t.Fatal("a pin written by one build was not there for the next")
	}

	if got != "alpine@sha256:aaa" {
		t.Errorf("remembered %q", got)
	}
}

// TestAPinIsKeyedOnTheReferenceAndThePlatform.
//
// The same tag on two platforms names two manifests. Collapsing them would pin
// one platform's image for both, which is the mistake `Plan.pin`'s own memo is
// keyed to avoid.
func TestAPinIsKeyedOnTheReferenceAndThePlatform(t *testing.T) {
	t.Parallel()

	pins := image.NewPins(t.TempDir(), time.Hour)
	pins.Put("alpine:3.21", "linux/arm64", "alpine@sha256:arm")

	if _, ok := pins.Get("alpine:3.21", "linux/amd64"); ok {
		t.Error("a pin for one platform answered for another")
	}

	if _, ok := pins.Get("alpine:3.20", "linux/arm64"); ok {
		t.Error("a pin for one tag answered for another")
	}
}

// TestAStalePinIsNotUsed.
//
// **The staleness window is the whole of the trade.** A tag that moves is not
// noticed until the pin expires, so the window has to be short enough to be
// defensible and is why this is off unless asked for.
func TestAStalePinIsNotUsed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	image.NewPins(dir, time.Hour).Put("alpine:3.21", "linux/arm64", "alpine@sha256:aaa")

	// Read back with a window that has already closed.
	if _, ok := image.NewPins(dir, time.Nanosecond).Get("alpine:3.21", "linux/arm64"); ok {
		t.Error("a pin older than the window was used")
	}
}

// TestNoWindowMeansNoCache.
//
// Off is off: a zero window must not read a pin somebody left behind, or
// turning the setting off would not turn the behaviour off.
func TestNoWindowMeansNoCache(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	image.NewPins(dir, time.Hour).Put("alpine:3.21", "linux/arm64", "alpine@sha256:aaa")

	off := image.NewPins(dir, 0)

	if _, ok := off.Get("alpine:3.21", "linux/arm64"); ok {
		t.Error("a zero window still answered from the cache")
	}

	// And writes nothing, so it cannot prime a cache for a later build that
	// does have a window.
	off.Put("alpine:3.20", "linux/arm64", "alpine@sha256:bbb")

	if _, ok := image.NewPins(dir, time.Hour).Get("alpine:3.20", "linux/arm64"); ok {
		t.Error("a zero window wrote a pin anyway")
	}
}

// TestPinsWithNowhereToLiveWriteNothing.
//
// **An empty directory is not the current one.** The caller hands over wherever
// the image cache is, and falls back to "" when it cannot work that out.
// `filepath.Join("", "pins")` is `pins` - a relative path - so a cache built on
// that answer creates a `pins/` directory wherever the build was started, which
// is somebody's repository.
//
// Not parallel: it changes the working directory to prove nothing lands in it,
// and that is process-wide.
//
//nolint:paralleltest // t.Chdir, which the runtime refuses in a parallel test
func TestPinsWithNowhereToLiveWriteNothing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	pins := image.NewPins("", time.Hour)

	pins.Put("alpine:3.21", "linux/arm64", "alpine@sha256:aaa")

	if _, ok := pins.Get("alpine:3.21", "linux/arm64"); ok {
		t.Error("a cache with nowhere to live answered")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}

		t.Errorf("it wrote %v into the working directory", names)
	}
}
