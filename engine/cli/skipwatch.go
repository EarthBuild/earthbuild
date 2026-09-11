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

	in, err := hostInputsFrom(contexts, places, merged, root)
	if err != nil {
		return nil, err
	}

	// **Copied from the checkout and read none of it.** That is the shape a
	// broken path rewrite takes - the tracer reporting one spelling, the
	// placements recording another, nothing matching - and an empty 𝑅 with a
	// matching shape skips. It can also be honest, and a build that copies a
	// tree and reads nothing from it is rare; refusing it costs a rebuild,
	// believing a broken rewrite costs correctness, and that decides it.
	if len(in) == 0 && placedFromAContext(places, contexts) {
		return nil, fmt.Errorf("%w: this build copied from the checkout and"+
			" nothing it read came from there", ErrNotDerivable)
	}

	return in, nil
}

// placedFromAContext says a copy took something out of the checkout.
func placedFromAContext(places []core.Placement, contexts map[string]bool) bool {
	for _, p := range places {
		if contexts[p.Layer] {
			return true
		}
	}

	return false
}

// watched are the step kinds whose reads decide their result, and so the only
// kinds whose silence is a gap rather than an absence.
//
// A `FROM` pulls an image and a local context is staged: both execute, both
// observe nothing, and neither hides a read. Counting them as gaps would mean
// no build with a base image ever earns a key, which is every build - and it is
// what the first end-to-end run did, refusing every record with "Earthfile:4 ran
// and was not watched" where Earthfile:4 was the FROM.
func watched(kind ir.OpKind) bool {
	return kind == ir.OpExec || kind == ir.OpFile
}

// gapIn is the first reason this build cannot be keyed, or empty.
//
// **A step that ran, had something to watch, and was not usefully watched is
// the gap.** A step served from cache watched nothing because it did nothing.
// Neither is a reason to distrust the key.
//
// The first reason rather than all of them, and never cleared: a build with one
// unwatched step among fifty is a build whose key would be wrong, and the
// forty-nine that reported cleanly say nothing about the one that did not.
func gapIn(rec *core.Record) string {
	for _, step := range rec.Steps {
		if !watched(step.Kind) || !executed(step.Outcome) {
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
		if watched(step.Kind) && !executed(step.Outcome) {
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
