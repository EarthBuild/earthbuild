package cli

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func imageNode(ref string) *ir.Node {
	return &ir.Node{
		Op:   ir.Op{Kind: ir.OpImage, Args: []string{ref}},
		Meta: ir.Meta{Source: "Earthfile:2"},
	}
}

// A reference nobody pinned is a caveat: the tag may mean something else by the
// time the skipped job would have run, and the fingerprint cannot see that.
func TestAnUnpinnedReferenceIsACaveat(t *testing.T) {
	t.Parallel()

	for ref, want := range map[string]bool{
		"alpine:3.22": true,
		"alpine@sha256:0123456789012345678901234567890123456789012345678901234567890123": false,
	} {
		plan := &interp.Plan{Graph: &ir.Graph{Root: imageNode(ref)}}

		got := caveatsOf(plan)
		if (len(got) > 0) != want {
			t.Errorf("%s gave caveats %v, wanted any=%v", ref, got, want)
		}
	}
}

// LOCALLY reads the machine, which no fingerprint over the build context can
// describe.
func TestALocallyStepIsACaveat(t *testing.T) {
	t.Parallel()

	plan := &interp.Plan{Graph: &ir.Graph{Root: &ir.Node{
		Op:   ir.Op{Kind: ir.OpHost, Args: []string{"date"}},
		Meta: ir.Meta{Source: "Earthfile:4"},
	}}}

	got := caveatsOf(plan)
	if len(got) != 1 || !strings.Contains(got[0], "LOCALLY") {
		t.Errorf("a LOCALLY step gave %v", got)
	}
}

// **`BUILD +other` is in Also, not in the root's inputs**, and it is still a
// thing the build runs. A fingerprint over the root alone stays equal while a
// BUILD-only dependency changes underneath it, which is a green tick for a job
// that would have failed.
func TestTheFingerprintCoversWhatOnlyBUILDReaches(t *testing.T) {
	t.Parallel()

	root := imageNode("alpine@sha256:aa")

	one := &ir.Graph{Root: root, Also: []*ir.Node{imageNode("busybox@sha256:bb")}}
	two := &ir.Graph{Root: root, Also: []*ir.Node{imageNode("busybox@sha256:cc")}}

	if fingerprintOf(one) == fingerprintOf(two) {
		t.Error("a changed BUILD-only dependency left the fingerprint equal")
	}

	// And the order Also happens to be in is not an input.
	a, b := imageNode("busybox@sha256:bb"), imageNode("busybox@sha256:cc")

	forwards := &ir.Graph{Root: root, Also: []*ir.Node{a, b}}
	backwards := &ir.Graph{Root: root, Also: []*ir.Node{b, a}}

	if fingerprintOf(forwards) != fingerprintOf(backwards) {
		t.Error("the order of Also reached the fingerprint")
	}
}
