package cli

import (
	"context"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestTheResolverReadsThePinsOnDisk.
//
// **The mechanism was tested and the wiring was not.** `image.Pins` has tests of
// its own, and so does reading the window out of the environment, and both would
// pass with the resolver looking in the wrong directory, or never asking, or
// asking with the platform spelt the other way (E703).
//
// So this goes at the seam: a pin is written where the resolver should look, and
// the resolver is asked for it. Nothing here reaches a registry - the reference
// names a host that does not exist - so an answer can only have come from disk.
// Wired wrongly, this fails by trying to resolve and returning the error, which
// is exactly the failure worth catching.
//
// Not parallel: it sets the environment, which the runtime refuses in a
// parallel test.
func TestTheResolverReadsThePinsOnDisk(t *testing.T) {
	const (
		ref = "no-such-registry.invalid/library/thing:1"
		to  = "no-such-registry.invalid/library/thing@sha256:" +
			"1111111111111111111111111111111111111111111111111111111111111111"
	)

	dir := t.TempDir()

	t.Setenv(envImageCacheDir, dir)
	t.Setenv(image.EnvPinTTL, "10m")

	// Written the way a previous build would have left it, through the same
	// type the resolver uses - the point of the test is the *directory* and the
	// key agreeing, not the file format.
	image.NewPins(dir, time.Hour).Put(ref, exec.DefaultPlatform(), to)

	got, err := (&engine{}).imageResolver(context.Background())(ref, "")
	if err != nil {
		t.Fatalf("the resolver went to the network for a reference it had a pin for: %v", err)
	}

	if got != to {
		t.Errorf("resolved to %q, want the pin %q", got, to)
	}
}

// TestTheResolverIgnoresPinsWhenThereIsNoWindow.
//
// Off has to be off at the seam too, or the setting would only appear to work:
// a pin left by a build that had a window must not be used by one that does not.
//
// Not parallel, as above.
func TestTheResolverIgnoresPinsWhenThereIsNoWindow(t *testing.T) {
	const ref = "no-such-registry.invalid/library/thing:1"

	dir := t.TempDir()

	t.Setenv(envImageCacheDir, dir)
	t.Setenv(image.EnvPinTTL, "")

	image.NewPins(dir, time.Hour).Put(ref, exec.DefaultPlatform(),
		"no-such-registry.invalid/library/thing@sha256:"+
			"1111111111111111111111111111111111111111111111111111111111111111")

	_, err := (&engine{}).imageResolver(context.Background())(ref, "")
	if err == nil {
		t.Error("with no window the resolver answered from a pin anyway;" +
			" turning the setting off must turn the behaviour off")
	}
}
