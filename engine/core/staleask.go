package core

import (
	"context"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// StaleAsker is a store that will answer the staleness question itself.
//
// **Because the comparison stops at the first difference and a fetch cannot.**
// WhyStale walks a step's observed reads in order and returns as soon as one has
// changed; on a host reading its own store that is a single lookup for the file
// somebody just edited. A view that has to be fetched has no such luck - every
// path is computed before the first is compared - so a guest holding the store
// on a device did 6299 lookups to answer what the host answered with one. It
// measured 4.0s of a 4.7s build, which was 85% of it.
//
// The interface is the question rather than the evidence, so the work is
// proportional to the answer. What runs on the other side is WhyStale: one
// implementation, wherever the store is.
type StaleAsker interface {
	WhyStaleIn(ctx context.Context, stack []ir.NodeID, obs Observation) (string, error)
}

// whyStaleVia decides whether an entry's observation still describes the base.
//
// Asks the store where it lives when it can answer, and otherwise fetches a
// view and compares here - which is what every store that the host can read
// does, and what this did everywhere before.
func whyStaleVia(
	ctx context.Context, src ViewSource, stack []ir.NodeID, obs Observation,
) (string, error) {
	if asker, ok := src.(StaleAsker); ok {
		return asker.WhyStaleIn(ctx, stack, obs)
	}

	view, err := viewOf(ctx, src, stack, PredictedReads(obs))
	if err != nil {
		return "", err
	}

	return WhyStale(obs, view), nil
}
