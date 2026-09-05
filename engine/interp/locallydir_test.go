package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestLocallyRunsInTheEarthfilesOwnDirectory.
//
// **A container's WORKDIR is a path in the container.** `LOCALLY` moves the
// commands after it onto the invoking machine, and the working directory did not
// move with them: `WORKDIR /test` followed by `LOCALLY` produced a host step
// asked to `chdir test`, a directory nobody has, and
// `tests/if.earth+test-switch-locally` failed there.
//
// The directory a host step starts in is the one holding the Earthfile, which is
// what makes `WORKDIR test-locally` after a `LOCALLY` mean a directory beside it
// (tests/for.earth+test-for-ls-locally).
func TestLocallyRunsInTheEarthfilesOwnDirectory(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8

FROM alpine:3.22
WORKDIR /test

main:
    LOCALLY
    RUN echo hello
`, "main")
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	found := false

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpHost {
			continue
		}

		found = true

		if n.Op.Dir != "" {
			t.Errorf("the host step runs in %q; a container's WORKDIR is a path"+
				" in the container and does not follow the build onto this machine",
				n.Op.Dir)
		}
	}

	if !found {
		t.Fatal("LOCALLY produced no host step")
	}
}

// A WORKDIR written *after* LOCALLY does apply, and is relative to the
// Earthfile - which is the whole point of resetting rather than forbidding.
func TestAWorkdirAfterLocallyStillApplies(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8

FROM alpine:3.22
WORKDIR /test

main:
    LOCALLY
    WORKDIR sub
    RUN echo hello
`, "main")
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpHost && n.Op.Dir != "sub" {
			t.Errorf("the host step runs in %q, want sub", n.Op.Dir)
		}
	}
}
