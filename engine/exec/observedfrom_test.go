package exec

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

type obsHandle struct{ obs core.Observation }

func (h obsHandle) Root() string                   { return "" }
func (h obsHandle) Delta() string                  { return "" }
func (h obsHandle) Release() error                 { return nil }
func (h obsHandle) Observations() core.Observation { return h.obs }

// A step reports an observation when there is one to report.
//
// The guest records what a copy looked at and carries it across the wire
// (E119), and nothing set `Result.Observed` - so the record was made, carried,
// and dropped on arrival. This is the decision that stops that, and it is here
// rather than in the guest deliberately: the guest reports *what it saw*, and
// whether that amounts to an observation of the step is a question about the
// whole step.
//
// **A lossy observation is still reported.** `Observed` and `Incomplete` are
// different questions - "did anyone watch" and "did they see everything" - and
// collapsing them here would throw away the distinction the scheduler needs to
// refuse for the right reason, and to say so in a build's record.
func TestAHandleWithSomethingToSayIsObserved(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		obs  core.Observation
		want bool
	}{{
		name: "nothing seen is not an observation",
		obs:  core.Observation{Reads: map[string]ir.NodeID{}},
		want: false,
	}, {
		name: "a read is",
		obs:  core.Observation{Reads: map[string]ir.NodeID{"/w": {1}}},
		want: true,
	}, {
		// The one that is easy to drop, and fatal to drop: a step that looked
		// for something and did not find it observed the base exactly as much
		// as one that read a file (green paper §3.4, I3).
		name: "so is a lookup that found nothing",
		obs:  core.Observation{Negative: []string{"/nowhere"}},
		want: true,
	}, {
		name: "a listing is",
		obs:  core.Observation{Listings: map[string]ir.NodeID{"/inc": {2}}},
		want: true,
	}, {
		name: "a lossy observation is still reported, and still lossy",
		obs:  core.Observation{Reads: map[string]ir.NodeID{"/w": {1}}, Incomplete: true},
		want: true,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			obs, ok := observedFrom(obsHandle{obs: tc.obs})
			if ok != tc.want {
				t.Errorf("reported observed=%v, want %v", ok, tc.want)
			}

			if obs.Incomplete != tc.obs.Incomplete {
				t.Error("the admission of loss was altered on the way through")
			}
		})
	}
}
