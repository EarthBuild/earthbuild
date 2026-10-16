package image_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestAMirrorIsAskedBeforeTheOrigin.
//
// **A rate limit is the slowest possible build.** Docker Hub allows an anonymous
// puller 100 manifest requests an hour; a benchmark loop, or an office behind one
// address, exhausts that and every `FROM` then fails outright. CI already fronts
// the daemon with `mirror.gcr.io`, so the buildkit path has an answer to this and
// the native path had none.
func TestAMirrorIsAskedBeforeTheOrigin(t *testing.T) {
	t.Parallel()

	origin := &fakeRegistry{layers: [][]byte{gzipTar(t, "from-origin", "one")}}
	mirror := &fakeRegistry{layers: [][]byte{gzipTar(t, "from-mirror", "one")}}

	originHost := origin.start(t)
	dir := t.TempDir()

	_, err := image.Pull(context.Background(), originHost+"/library/test:1", dir, image.Options{
		Plain:   true,
		Mirrors: map[string][]string{originHost: {mirror.start(t)}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The mirror's copy is what landed, which is the only proof the mirror was
	// used rather than merely configured.
	_, err = os.Stat(filepath.Join(dir, "from-mirror"))
	if err != nil {
		t.Errorf("the mirror was configured and the origin answered anyway: %v", err)
	}

	if origin.manifests != 0 {
		t.Errorf("the origin served %d manifests; a mirror that works must spare it entirely",
			origin.manifests)
	}
}

// TestAMirrorThatRefusesFallsBackToTheOrigin.
//
// A mirror is an optimisation, so it may not be a new way to fail: one that is
// down, rate-limited or does not carry the image has to leave the build exactly
// as it was before the mirror was configured.
func TestAMirrorThatRefusesFallsBackToTheOrigin(t *testing.T) {
	t.Parallel()

	origin := &fakeRegistry{layers: [][]byte{gzipTar(t, "from-origin", "one")}}
	mirror := &fakeRegistry{refuse: true}

	originHost := origin.start(t)
	dir := t.TempDir()

	_, err := image.Pull(context.Background(), originHost+"/library/test:1", dir, image.Options{
		Plain:   true,
		Mirrors: map[string][]string{originHost: {mirror.start(t)}},
	})
	if err != nil {
		t.Fatalf("a refusing mirror broke a pull that would have worked without it: %v", err)
	}

	_, err = os.Stat(filepath.Join(dir, "from-origin"))
	if err != nil {
		t.Errorf("the origin's copy did not land: %v", err)
	}
}
