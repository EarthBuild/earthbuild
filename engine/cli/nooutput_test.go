package cli

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestNoOutputExportsNothing.
//
// **`--ci` means `--no-output --strict`, and the native engine had neither**, so
// this repository's own CI lines - `earth --ci +unit-test` and the rest - could
// not be run by it at all: "flag provided but not defined: -ci".
//
// Strict is what the native engine already is, by refusing what it cannot
// reproduce (I10). No-output is the half that had to be built, and it belongs at
// the one place that writes: `exportAll`.
//
// Asserted by passing a nil executor and a nil scheduler. With the artifact
// exported, this reaches `s.StackFor` and dies; returning cleanly is proof that
// nothing was touched, which is a stronger claim than "the file was absent" and
// needs no sandbox to make.
func TestNoOutputExportsNothing(t *testing.T) {
	t.Parallel()

	plan := &interp.Plan{Artifacts: []interp.Artifact{{
		Path: "/bundle", LocalDest: "out", Source: "Earthfile:3",
	}}}

	err := exportAll(context.Background(), Options{NoOutput: true}, nil, nil, plan)
	if err != nil {
		t.Fatalf("with output off, exporting still tried to do something: %v", err)
	}
}
