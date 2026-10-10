package fleet

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A declaration survives delegation.
//
// **A stack element need not be a layer.** An image contributing only
// configuration - environment, working directory, user, entrypoint - is held as
// a declaration (§3.2a), and `core.Result` carries two fields for it. The pair
// is the point: a zero identity means "declares nothing", which is a fact about
// the image, and `Declared` false means "nobody looked", which is a fact about
// the answer. Read as the same, a FROM serves a stack with no declaration and
// the step above it runs without the environment its image sets.
//
// The wire carried neither. Measured on two machines - an arm64 driver and an
// amd64 worker, both building `golang:1.27-alpine` pinned by digest:
//
//	solo    PATH=/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:...
//	fleet   PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:...
//
// `apk` is on the default path and kept working; `go` lives at
// /usr/local/go/bin and vanished. The build failed at `RUN go telemetry off`
// with exit 127 - which is neither a refusal (I10) nor a degrade to a miss
// (I11), but a delegated step running against a base that was not all there.
//
// Round-tripped through both halves, because fixing one is indistinguishable
// from fixing neither: `replyOf` is what the worker sends and `resultOf` is
// what the driver believes.
func TestADeclarationSurvivesDelegation(t *testing.T) {
	t.Parallel()

	declares := ir.NodeID{9, 8, 7}

	got := resultOf(replyOf(core.Result{
		Layer:    ir.NodeID{1},
		Content:  ir.NodeID{2},
		Declares: declares,
	}))

	if got.Declares != declares {
		t.Errorf("a delegated result came back declaring %v, want %v"+
			"\n  the step above this base runs without the environment its image sets",
			got.Declares, declares)
	}
}

// A step that declares nothing still says nothing.
//
// The zero identity is a fact about the image - "this declares nothing" - and
// must survive the trip unchanged rather than becoming something.
func TestAStepThatDeclaresNothingStillDeclaresNothing(t *testing.T) {
	t.Parallel()

	if got := resultOf(replyOf(core.Result{Layer: ir.NodeID{1}})); got.Declares != (ir.NodeID{}) {
		t.Errorf("a step declaring nothing came back declaring %v", got.Declares)
	}
}
