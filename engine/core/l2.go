package core

import (
	"context"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Profiles remembers what a step read last time, so that Κ₂ can be computed
// before the step runs rather than only after it.
//
// Keyed by step *class* (StepClass): the operation, its ambient state and its
// platform, with no inputs at all. That is the whole trick. Any key including
// the inputs - the chain key, or node identity - changes the moment the base
// changes, so the profile would be missing in exactly the situation L2 exists
// for. The class is what stays put while the base moves.
//
// A profile is a hint. It may be absent, stale, or wrong, and the consistency
// check (green paper 4.7) is what makes acting on it safe.
type Profiles interface {
	Get(class Key) (Observation, bool)
	Put(class Key, obs Observation)
}

// ViewSource answers questions about a stack without materialising it.
//
// This is not an optimisation, it is the point. Verifying a prediction costs a
// lookup per path the prediction names; materialising to verify would cost the
// mount we are trying to avoid, and L2 would be slower than the rebuild it
// replaces.
type ViewSource interface {
	View(ctx context.Context, stack []ir.NodeID) (BaseView, error)
}

// tryL2 attempts the observed-input lookup, green paper (4.3).
//
// It returns a result only when a prediction exists, still describes the base,
// and names an entry that verifies. Any of those failing yields no result and
// the step runs - never an error, because L2 is an optimisation and a broken
// optimisation must degrade to work rather than to failure.
//
// An implementation that skips this entirely is conforming: slower, never
// wrong.
func (s *Scheduler) tryL2(ctx context.Context, n *ir.Node, base, refs []ir.NodeID) (Entry, bool) {
	if s.Profiles == nil || s.Views == nil {
		return Entry{}, false
	}

	pred, ok := s.Profiles.Get(StepClass(n))
	if ok {
		// **Told to the executor whether or not this lookup succeeds.** The
		// prediction is fetched here for a cache question, and it answers a
		// second one for nothing: what to assemble a base *out of*, should the
		// step have to run. Left unsaid, `wouldPrime` is false and the step
		// gets its base whole however little of it it opens - which is why
		// lazy materialisation was reachable only on a fleet worker, where the
		// assignment's hints fill the same field.
		//
		// Advice, not identity: `Meta` is not hashed and there is a test that
		// says so (E301).
		n.Meta.ReadsPredicted = PredictedReads(pred)
	}

	if !ok {
		// Nothing recorded for this class of step. Ordinary on a first build and
		// a defect on a later one, so it is counted - but only for a step with a
		// base, because one without has nothing to be predicted *about* and
		// counting those made the number noise (E218, E223).
		if len(base) > 0 {
			s.Stats.L2Unpredicted++
			s.noteUnpredicted(n)
		}

		return Entry{}, false
	}

	// A prediction that names nothing agrees with every base, so the check below
	// would reduce to "is there an entry under the empty-observation key" - and
	// at S6 that entry comes from a machine this one did not write (A5). The
	// publish side refuses to create such a key; this refuses to trust one.
	//
	// Two independent halves on purpose. A check that holds only while its
	// counterpart holds is one refactor away from being nothing at all.
	if !ObservesSomething(n, base, pred) {
		// A prediction naming nothing agrees with every base, so it is refused
		// rather than trusted. Counted apart from a stale one: this step will
		// never be reusable, while a stale prediction is one that stopped
		// describing *this* base.
		s.Stats.L2Empty++

		return Entry{}, false
	}

	view, err := s.Views.View(ctx, base)
	if err != nil {
		return Entry{}, false // cannot check, so cannot use
	}

	if why := WhyStale(pred, view); why != "" {
		s.Stats.L2Stale++

		// The first one, kept: every stale prediction in a build is usually the
		// same path for the same reason, and a build that printed one line per
		// step would bury the answer it is trying to give. A count without a
		// cause is what this replaces (E127).
		s.noteStale(why)

		return Entry{}, false
	}

	// Through the same read path as L1, because `--no-cache` is about the build
	// and not about which tier of the cache it happens to trust (E462).
	e, hit := Lookup(s.cacheToRead(), s.Blobs, s.Trusted, DeriveObservedKey(n, refs, pred))
	if !hit {
		// The prediction still describes the base and no result was stored under
		// the key it implies. That is the interesting miss: everything the tier
		// needs was true and there was nothing to serve.
		s.Stats.L2Unstored++

		return Entry{}, false
	}

	return e, true
}

// PredictedReads is what a profile says a class of step reads, in the order a
// fragment request wants them.
//
// Sorted, because these reach a request for part of a layer and a request that
// varied with map iteration order would ask for the same paths under different
// names - which is a cache miss dressed as a fetch.
func PredictedReads(pred Observation) []string {
	if len(pred.Reads) == 0 {
		return nil
	}

	return sortedKeys(pred.Reads)
}
