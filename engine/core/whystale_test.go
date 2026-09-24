package core_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A stale prediction says which two digests disagreed.
//
// `WhyStale` named the path, which is what E125 added it for - a count without
// a cause is a tier being invalidated with no way to say by what. The path is
// not enough for the case in front of it: `/bin/cat changed in the base` between
// two bases that differ only in a file nothing reads, and the next question is
// always *which side is wrong* - what the step observed, or what the base holds
// now (E493).
//
// Five hypotheses were eliminated by hand before this was written, and every one
// of them would have been answered in a second by the two numbers.
//
// **The same argument as first-divergence reporting for chain keys** (B.4),
// which this comment already cited while reporting one side of the divergence.
func TestAStalePredictionNamesBothDigests(t *testing.T) {
	t.Parallel()

	observed := ir.NodeID{1, 2, 3}
	now := ir.NodeID{4, 5, 6}

	why := core.WhyStale(
		core.Observation{Reads: map[string]ir.NodeID{"/bin/cat": observed}},
		fakeBase{files: map[string]ir.NodeID{"/bin/cat": now}},
	)

	if !strings.Contains(why, "/bin/cat") {
		t.Fatalf("the reason is %q and does not name the path", why)
	}

	for what, want := range map[string]string{
		"what the step observed": observed.String()[:12],
		"what the base holds":    now.String()[:12],
	} {
		if !strings.Contains(why, want) {
			t.Errorf("the reason is %q and does not say %s (%s)", why, what, want)
		}
	}
}

// A path that is gone is still reported as gone.
//
// The other outcome, and the one that must not start printing a digest that does
// not exist: "gone" and "changed to the zero digest" are different findings, and
// a reader who cannot tell them apart looks for the wrong bug.
func TestAPathGoneFromTheBaseIsNotReportedAsChanged(t *testing.T) {
	t.Parallel()

	why := core.WhyStale(
		core.Observation{Reads: map[string]ir.NodeID{"/bin/cat": {1}}},
		fakeBase{files: map[string]ir.NodeID{}},
	)

	if !strings.Contains(why, "gone from the base") {
		t.Errorf("the reason is %q, and the path is absent rather than different", why)
	}
}
