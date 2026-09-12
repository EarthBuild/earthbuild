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
	rec *core.Record, known core.Profiles, contexts map[string]bool, root string,
) ([]hostInput, error) {
	if rec == nil {
		return nil, fmt.Errorf("%w: this build kept no record", ErrNotDerivable)
	}

	why := gapIn(rec, known)
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

		obs, ok := readsOf(step, known)
		if !ok {
			continue
		}

		maps.Copy(merged.Reads, obs.Reads)
		maps.Copy(merged.Listings, obs.Listings)

		merged.Negative = append(merged.Negative, obs.Negative...)
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

// benign is a kind that reads nothing of the checkout on its own account.
//
// What it produces is named by the chain above it: an image by its reference,
// a context by its digest, a merge and a scratch by their inputs. There is
// nothing for a tracer to have missed, so its presence does not stand between
// a build and a key.
func benign(kind ir.OpKind) bool {
	switch kind {
	case ir.OpImage, ir.OpLocal, ir.OpMerge, ir.OpPackImage, ir.OpScratch:
		return true

	default:
		return false
	}
}

// refuses reports whether no observation could make a build containing this
// kind skippable.
func refuses(kind ir.OpKind) bool { return refusal(kind) != "" }

// refusal says why a kind cannot be keyed, or is empty.
//
// **Not the same question as "was it watched".** A watched kind lacking an
// observation is a gap that a later build could close; these are kinds where
// closing it would change nothing, because what makes them unskippable is not
// missing information.
func refusal(kind ir.OpKind) string {
	switch kind {
	case ir.OpHost:
		// LOCALLY. Nothing watched it - it runs on this machine with no
		// sandbox - but that is the lesser half. It *writes* here too, and a
		// side effect is not an input: a perfect read set would not make
		// skipping it safe, because what it does outside the build's own
		// layers would simply not happen.
		return "runs LOCALLY, on this machine and outside the build:" +
			" skipping it would skip whatever it writes there"

	case ir.OpBuild:
		// Delegated wholesale to a worker, which schedules it itself, so the
		// steps that did the reading are in that build's record and not in
		// this one. Absent until the fleet exists, and named now because the
		// alternative is that it arrives keyable by default.
		return "delegates a target to a worker, whose reads are not in this build's record"

	default:
		return ""
	}
}

// readsOf is what a step read, whether it ran or was served from cache.
//
// **A step's observation is its own reads and not the chain's**, so a step
// served from cache contributes nothing - which is why every cached step used
// to refuse the key, and why on a project with a shared prepare chain the key
// was never recorded at all.
//
// The engine already keeps what a step class read: it is what L2 predicts from.
// A cached step's paths come from there. The digests do not: they are re-read
// from the checkout as every other input is, so a stale profile can contribute
// a stale *set of paths* and never a stale digest - and the step's chain key
// having hit is what says the paths have not moved.
func readsOf(step core.StepRecord, known core.Profiles) (core.Observation, bool) {
	if step.Observed {
		return step.Observation, true
	}

	if known == nil || !watched(step.Kind) {
		return core.Observation{}, false
	}

	return known.Get(step.Class)
}

// gapIn is the first reason this build cannot be keyed, or empty.
//
// **A step that ran, had something to watch, and was not usefully watched is
// the gap** - and so is one served from cache that nobody has a profile for,
// because its reads are then unknown, and unknown is not empty.
func gapIn(rec *core.Record, known core.Profiles) string {
	for _, step := range rec.Steps {
		if why := refusal(step.Kind); why != "" {
			return stepName(step) + " " + why
		}

		if !watched(step.Kind) {
			continue
		}

		if _, ok := readsOf(step, known); !ok {
			if executed(step.Outcome) {
				return stepName(step) + " ran and was not watched"
			}

			return stepName(step) + " came from cache and nothing recorded what it reads"
		}

		if step.Observed && step.Observation.Incomplete {
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
