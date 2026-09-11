package cli

import (
	"fmt"
	"maps"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// hostInputsOfBuild is 𝑅 for a whole build, or why it cannot be had.
//
// **Read off the build record rather than gathered separately.** Every step's
// observation is already there in full, put there by the scheduler under the
// same usability gate L2 applies (`usableObservation`) - so a second
// accumulator would be a second opinion about which steps were watched, which
// is the defect this engine keeps a key guard for. Placements sit beside the
// observations for the same reason.
//
// A build is many steps and the record is about all of them: what any step read
// from the checkout is an input to the build, whichever step read it, and a key
// derived from one step's reads would skip on a change another step would have
// seen.
func hostInputsOfBuild(
	rec *core.Record, contexts map[string]bool, root string,
) ([]hostInput, error) {
	if rec == nil {
		return nil, fmt.Errorf("%w: this build kept no record", ErrNotDerivable)
	}

	why := gapIn(rec)
	if why != "" {
		return nil, fmt.Errorf("%w: %s", ErrNotDerivable, why)
	}

	merged := core.Observation{
		Reads:    map[string]ir.NodeID{},
		Listings: map[string]ir.NodeID{},
	}

	var places []core.Placement

	for _, step := range rec.Steps {
		places = append(places, step.Placements...)

		if !step.Observed {
			continue
		}

		maps.Copy(merged.Reads, step.Observation.Reads)
		maps.Copy(merged.Listings, step.Observation.Listings)

		merged.Negative = append(merged.Negative, step.Observation.Negative...)
	}

	return hostInputsFrom(contexts, places, merged, root)
}

// gapIn is the first reason this build cannot be keyed, or empty.
//
// **A step that ran and was not usefully watched is the gap**, and the
// discriminator is that it ran. A step served from cache watched nothing
// because it did nothing; a `FROM` watched nothing because there was nothing to
// watch. Treating either as a gap would mean no build ever earns a key.
//
// The first reason rather than all of them, and never cleared: a build with one
// unwatched step among fifty is a build whose key would be wrong, and the
// forty-nine that reported cleanly say nothing about the one that did not.
func gapIn(rec *core.Record) string {
	for _, step := range rec.Steps {
		if !executed(step.Outcome) {
			continue
		}

		switch {
		case !step.Observed:
			return stepName(step) + " ran and was not watched"

		case step.Observation.Incomplete:
			return fmt.Sprintf("%s ran and its tracer missed something: %v",
				stepName(step), step.Observation.Why)
		}
	}

	return ""
}

// stepName is what to call a step in a reason somebody reads.
func stepName(step core.StepRecord) string {
	if step.Meta.Source != "" {
		return step.Meta.Source
	}

	if step.Ident != "" {
		return step.Ident
	}

	return "a step"
}

// refreshable says whether this build saw enough to write a new record.
//
// **A build where anything came from cache did not observe what those steps
// would have read**, so the inputs it gathered are a subset of the build's. A
// subset is exactly the shape that skips on a change nobody accounted for, so
// such a build leaves the existing record alone: the old one still names the
// right paths, and a build that actually changes something will run every step
// that matters and refresh it then.
func refreshable(rec *core.Record) bool {
	if rec == nil {
		return false
	}

	for _, step := range rec.Steps {
		if !executed(step.Outcome) {
			return false
		}
	}

	return len(rec.Steps) > 0
}

// executed says a step actually ran, which is the only kind that can have
// watched anything.
//
// Two outcomes mean it ran: a plain miss, and one whose result was not captured
// - the step still executed and still read what it read, and treating the
// second as "did not run" would let a build with an uncaptured step write a
// record missing its reads.
func executed(o core.Outcome) bool {
	return o == core.OutcomeMiss || o == core.OutcomeUncaptured
}
