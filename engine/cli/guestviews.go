package cli

import (
	"context"
	"errors"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// errNoStaleAsker is what a view with nobody to ask answers.
//
// An error rather than "nothing is stale", because the two are opposite claims
// and the cheap one is the dangerous one: a view that cannot check reporting
// everything fresh is a build that reuses a step whose base moved.
var errNoStaleAsker = errors.New("this view has no guest to ask about staleness")

// guestViews answers what a base holds by asking whoever holds the store.
//
// **The observed-input tier reads a base to check a prediction against it**, and
// a base on a device the guest owns is not on the host's filesystem - so a host
// that reads it finds nothing and reports every prediction stale, naming a file
// that is present and simply not present *here*.
//
// Path-aware, because the alternative is a round trip per file in a prediction:
// the profile is read before a view is asked for, so the whole set is known and
// one question answers it.
type guestViews struct {
	ask func(ctx context.Context, stack []ir.NodeID, paths []string) (files, listings map[string]ir.NodeID, err error)
	// stale answers the whole question rather than supplying its evidence. See
	// WhyStaleIn.
	stale func(ctx context.Context, stack []ir.NodeID, obs core.Observation) (string, error)
}

// WhyStaleIn asks the guest whether an observation still describes the base,
// instead of fetching every digest and deciding here.
//
// **Because the comparison stops at the first difference and a fetch cannot.**
// The tier walks a step's observed reads in order and returns as soon as one
// has changed; fetching computes all 6302 of them so that the first can be
// looked at. On a freshly booted guest those reads come off its own device with
// an empty page cache, which is why the same comparison costs 4.409s there and
// 0.222s on a host reading a store it has already read.
//
// Reached only under EARTH_ASK_STALE. The setting existed before this method
// did and so turned on and changed nothing - `core.whyStaleVia` asks the view
// source for this and quietly fetches when it has not got it, which is a switch
// that reports success and does nothing.

// View without a set of paths cannot be batched, and asking per path would cost
// more than the tier saves - so it declines, which the tier reads as "no view"
// and turns into an ordinary miss.
func (g *guestViews) View(context.Context, []ir.NodeID) (core.BaseView, error) {
	return nil, errNoPathsGiven
}

func (g *guestViews) WhyStaleIn(
	ctx context.Context, stack []ir.NodeID, obs core.Observation,
) (string, error) {
	if g.stale == nil {
		return "", errNoStaleAsker
	}

	return g.stale(ctx, stack, obs)
}

// ViewFor asks once for every path the prediction names.
func (g *guestViews) ViewFor(
	ctx context.Context, stack []ir.NodeID, want []string,
) (core.BaseView, error) {
	files, listings, err := g.ask(ctx, stack, want)
	if err != nil {
		return nil, err
	}

	return askedBase{files: files, listings: listings}, nil
}

// askedBase is what came back, answering from the map rather than the disk.
//
// A path absent from the map is absent from the base. That is the same
// distinction the wire keeps: "not there" and "there and empty" are different
// answers and a prediction turns on which it gets.
type askedBase struct {
	files    map[string]ir.NodeID
	listings map[string]ir.NodeID
}

func (b askedBase) Digest(path string) (ir.NodeID, bool) {
	id, ok := b.files[path]

	return id, ok
}

func (b askedBase) ListingDigest(dir string) (ir.NodeID, bool) {
	id, ok := b.listings[dir]

	return id, ok
}
