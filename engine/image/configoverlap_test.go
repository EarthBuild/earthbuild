package image_test

import (
	"context"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// The configuration blob is fetched while the layers are, not after them.
//
// A pull is a manifest, then the layers, then the image's configuration -
// ENTRYPOINT, ENV, WORKDIR, USER. The configuration's digest is known as soon
// as the manifest is, so nothing about it depends on a layer having arrived,
// yet it was fetched strictly last. On an alpine pull that is a stable 0.12s of
// round trip spent after all the transferring is done (E836).
//
// The reason it was last is worth stating, because it is a real one and this
// does not repeal it: a manifest whose layers cannot be pulled has nothing
// worth configuring. What that buys is one saved HTTP GET on a pull that was
// going to fail anyway, and what it costs is a round trip on every pull that
// succeeds.
//
// **Measured as overlap, not as elapsed time.** A test that asserts a pull got
// faster is a test that fails on a loaded machine. The fake registry counts how
// many blob requests were ever in flight at once, so "these two overlapped" is
// a fact about the requests rather than about the clock.
func TestTheConfigurationIsFetchedWhileTheLayersAre(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{
		layers:    [][]byte{gzipTar(t, "greeting", "hi\n")},
		config:    []byte(`{"config": {"WorkingDir": "/app", "User": "node"}}`),
		blobDelay: 50 * time.Millisecond,
	}

	cfg, err := image.Pull(context.Background(), reg.start(t)+"/library/thing:1",
		t.TempDir(), image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	// Still correct, because an overlap that loses the declaration is not an
	// optimisation.
	if cfg.User != "node" {
		t.Errorf("the pull lost the image's configuration: user is %q", cfg.User)
	}

	if got := reg.peakBlobs(); got < 2 {
		t.Errorf("only %d blob request was ever in flight: the configuration is "+
			"still fetched after the layers rather than beside them", got)
	}
}
