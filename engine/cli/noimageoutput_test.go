package cli

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestNoImageOutputWritesNothing.
//
// **Two output knobs, not one.** `--no-output` withholds `SAVE ARTIFACT ... AS
// LOCAL`; `--no-image-output` withholds the `SAVE IMAGE` half, and a build may
// well want one without the other - push an image without leaving a copy on
// the machine that built it, or produce artifacts while declining to write a
// multi-gigabyte layout nobody asked for.
//
// Upstream's version skips *loading into a local daemon*. This engine writes an
// OCI layout instead, which is the same act against the same filesystem: a
// write to somebody's machine that the build did not have to make.
//
// Asserted the way TestNoOutputExportsNothing is, with a nil executor. Without
// the guard this reaches `e.Sandbox()` on a nil executor and panics, so
// returning cleanly is proof that nothing was looked up - a stronger claim than
// "no layout appeared" and one that needs no sandbox to make.
func TestNoImageOutputWritesNothing(t *testing.T) {
	t.Parallel()

	images := []interp.Image{{Ref: "example.com/x:latest", Source: "Earthfile:5"}}

	err := writeImages(context.Background(), Options{NoImageOutput: true},
		nil, nil, nil, images, nil)
	if err != nil {
		t.Fatalf("with image output off, writing still tried to do something: %v", err)
	}
}
