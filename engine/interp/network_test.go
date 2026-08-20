package interp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `RUN --network=none` cuts the step off, and reaches the step's identity.
//
// The mechanism was already built and disconnected: `guest.isolate` takes a
// `dropNet` and adds CLONE_NEWNET when it is set, `Server.DropNet` carries it,
// and **nothing anywhere set either**. Meanwhile the flag was refused as an
// engine gap. Written and unreachable, with a refusal in front of it.
//
// It reaches the key for the reason `--no-cache` and `--with-docker` do: the
// same command with and without the network is a different request - one
// resolves a dependency, the other fails to - and a cache that could not tell
// them apart would serve one for the other. The reflection guard in
// engine/core insists on this; the claim is written here too because a guard
// that would notice is not the same as a decision somebody made.
func TestNetworkNoneCutsTheStepOffAndReachesTheKey(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN connected-step
    RUN --network=none isolated-step
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var checked int

	for _, n := range p.Graph.Nodes() {
		switch {
		case strings.Contains(n.Meta.Description, "isolated-step"):
			checked++

			if !n.Op.NoNetwork {
				t.Error("a --network=none step is not marked as cut off")
			}
		case strings.Contains(n.Meta.Description, "connected-step"):
			checked++

			if n.Op.NoNetwork {
				t.Error("an ordinary step was cut off from the network")
			}
		}
	}

	if checked != 2 {
		t.Errorf("found %d of the 2 steps", checked)
	}
}

// Any other value is still refused, by name.
//
// `none` is the only value docs/earthfile/earthfile.md gives - the heading is
// literally `--network=none` - so accepting `--network=host` would be inventing
// a meaning for it. The dangerous direction, too: a build asking for host
// networking and silently getting an isolated namespace fails in a way that
// looks like a broken mirror.
func TestAnotherNetworkValueIsStillRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --network=host fetch\n", testMain)
	if err == nil {
		t.Fatal("--network=host was accepted")
	}

	if !errors.Is(err, interp.ErrRefused) {
		t.Errorf("the refusal is not in the refused family:\n%s", err)
	}

	if !strings.Contains(err.Error(), "host") {
		t.Errorf("the refusal does not name the value it could not take:\n%s", err)
	}
}
