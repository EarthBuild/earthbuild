package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// A file a step deletes stays deleted.
//
// Found while chasing why a stored layer does not re-digest to its own name
// (E86, E87), and it is not a digest problem at all. `copyTree`'s last branch
// reads:
//
//	default:
//	    // Devices and fifos need privilege and rarely appear in a delta.
//	    // Skipped rather than failed, and named so the omission is deliberate.
//
// **An overlayfs whiteout is a character device**, mode 0, 0. It is how the
// upper layer of an overlay records that something below it was removed, and it
// is the single most common entry in the delta of any step that cleans up after
// itself. `commit` copies the delta into the store with that function, so every
// deletion a step made was dropped on the way in - and the layer that arrived
// said nothing had been removed.
//
// Measured before this test was written:
//
//	RUN echo x > /marker.txt
//	RUN rm /marker.txt
//	RUN if [ -e /marker.txt ]; then echo STILL-THERE; else echo GONE; fi
//
//	STILL-THERE
//
// The comment was right that they need privilege and wrong that they rarely
// appear, and the "deliberate" omission silently discarded a step's work.
// `rm -rf /var/cache/apk/*` is the shape this appears in, in most of the
// Earthfiles anybody writes.
func TestAFileAStepDeletesStaysDeleted(t *testing.T) { // not parallel: boots a VM
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	dir := t.TempDir()

	// Three steps, so the deletion is committed as a layer of its own and read
	// back from the store by the step that checks it. Deleting and checking in
	// one RUN would pass without the layer ever being stored, which is the only
	// place this goes wrong.
	err := os.WriteFile(filepath.Join(dir, testEarthfile), []byte(`VERSION 0.8

probe:
    FROM alpine:3.22
    RUN echo x > /marker.txt && mkdir -p /d && echo y > /d/inner.txt
    RUN rm /marker.txt && rm -rf /d
    RUN { if [ -e /marker.txt ]; then echo file:STILL-THERE; else echo file:GONE; fi; \
          if [ -e /d ]; then echo dir:STILL-THERE; else echo dir:GONE; fi; } > /r.txt
    SAVE ARTIFACT /r.txt AS LOCAL r.txt
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer

	err = cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testProbe, Out: &log, Platform: testPlatform(),
	})

	// No longer skipped anywhere. A store that cannot hold a device node holds
	// a `.wh.` marker instead - the spelling every registry already uses - and
	// the materialiser turns it back into what overlayfs reads, on storage
	// inside the VM where mknod works (E94).
	//
	// This test going from SKIP to PASS on a macOS host is the whole point of
	// that change: it is what "a build that deletes something" costs, and it
	// cost every Earthfile containing `rm`.

	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}

	body, err := os.ReadFile(filepath.Join(dir, "r.txt")) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("no artifact: %v\n%s", err, log.String())
	}

	got := string(body)

	// Both, because a whiteout and an opaque directory are two different
	// records: removing a file writes a character device beside it, and
	// removing a whole directory marks the replacement opaque with an xattr.
	// An implementation that handled one would pass a test that checked one.
	for _, want := range []string{"file:GONE", "dir:GONE"} {
		if !strings.Contains(got, want) {
			t.Errorf("a deletion did not survive the layer store: wanted %q, got:\n%s", want, got)
		}
	}
}

// A deletion that cannot be recorded fails the build, and names what was lost.
//
// The half a macOS host actually reaches, and the one that matters most: until
// this iteration the copy dropped the whiteout and reported success, so a build
// that deleted something produced an image that still contained it - a wrong
// artefact, silently, from a build that said it worked.
//
// The refusal has to name three things, because the cause is three layers from
// the Earthfile line that provoked it: what was deleted, that a deletion is
// stored as a device node, and that the store is a shared host directory which
// has none. A reader given only "operation not permitted" would look at the
// step.
func TestADeletionThatCannotBeRecordedFailsLoudly(t *testing.T) { // not parallel: boots a VM
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, testEarthfile), []byte(`VERSION 0.8

probe:
    FROM alpine:3.22
    RUN echo x > /marker.txt
    RUN rm /marker.txt
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer

	err = cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testProbe, Out: &log, Platform: testPlatform(),
	})
	if err == nil {
		// Which is the correct outcome on a store that can record one, and this
		// test has nothing to say there.
		t.Skip("this store can record a deletion, so there is no refusal to check")
	}

	for _, want := range []string{"deletes", "device node", "shared into the sandbox"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%v", want, err)
		}
	}
}
