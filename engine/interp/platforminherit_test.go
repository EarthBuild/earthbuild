package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestATargetInheritsThePlatformItStandsOn.
//
// **A step's platform decides which machines may run it.** `FROM +common`
// carries the referenced target's directory, environment, user and image
// configuration - and not its platform, which was never considered. So a target
// standing on an `amd64` base is labelled with the *driver's* architecture,
// placement believes the label, and a machine that natively runs the step's
// real content is ruled ineligible.
//
// Measured on a two-machine fleet: writing `--platform` on every `FROM` by hand
// took the same build from 1 delegated step to 7 (E-F1). A heterogeneous fleet
// cannot be given work it is eligible for until the label is true.
func TestATargetInheritsThePlatformItStandsOn(t *testing.T) {
	t.Parallel()

	src := "VERSION 0.8\n" +
		"common:\n" +
		"    FROM --platform=linux/amd64 alpine:3.22\n" +
		"main:\n" +
		"    FROM +common\n" +
		"    RUN true\n"

	for _, n := range nodesOf(t, src) {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		if got := n.Platform.String(); !strings.Contains(got, "amd64") {
			t.Errorf("a step standing on an amd64 base is labelled %q, so a"+
				" native amd64 worker is ineligible for it", got)
		}
	}
}

// TestAWrittenPlatformReachesTheStepsThatFollow.
//
// The written one wins by having been obeyed already: it is passed into the
// reference, the referenced target builds for it, and it comes back as the
// platform the caller inherits.
//
// **Not the same as overriding the reference.** A target that pins its own
// platform keeps it - `FROM --platform=linux/arm64 +amd64Target` gets amd64
// content - and labelling the caller arm64 there would recreate exactly the bug
// above, a step described as something its base is not.
func TestAWrittenPlatformReachesTheStepsThatFollow(t *testing.T) {
	t.Parallel()

	src := "VERSION 0.8\n" +
		"common:\n" +
		"    FROM alpine:3.22\n" +
		"main:\n" +
		"    FROM --platform=linux/arm64 +common\n" +
		"    RUN true\n"

	for _, n := range nodesOf(t, src) {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		if got := n.Platform.String(); !strings.Contains(got, "arm64") {
			t.Errorf("a step whose FROM names a platform is labelled %q", got)
		}
	}
}

// nodesOf is every node in a plan, reachable from its root.
func nodesOf(t *testing.T, src string) []*ir.Node {
	t.Helper()

	p, err := interp.Build(src, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var (
		out  []*ir.Node
		seen = map[ir.NodeID]bool{}
		walk func(*ir.Node)
	)

	walk = func(n *ir.Node) {
		if n == nil || seen[n.ID()] {
			return
		}

		seen[n.ID()] = true
		out = append(out, n)

		for _, in := range n.Inputs {
			walk(in)
		}
	}

	walk(p.Graph.Root)

	return out
}
