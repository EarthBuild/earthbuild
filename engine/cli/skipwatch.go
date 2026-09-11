package cli

import (
	"fmt"
	"maps"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// skipWatch gathers what a build read and where its copies put things.
//
// **A build is many steps and the record is about all of them.** What any step
// read from the checkout is an input to the build, whichever step read it, and a
// key derived from one step's reads would skip on a change another step would
// have seen.
//
// Not safe for concurrent use by itself - the executor calls it from whichever
// goroutine finished a step - so `Watch` is wrapped in a lock where it is
// installed. Kept out of here because a type that locks for a caller who does
// not need it is a type nobody can compose.
type skipWatch struct {
	obs    []core.Observation
	places []core.Placement
	// gap is why this build cannot be keyed, or empty. Sticky: the first reason
	// is kept and later steps cannot clear it.
	gap string
}

// watched are the step kinds whose reads decide their result, and so the kinds
// whose silence is a gap rather than an absence.
//
// A `FROM` resolves an image and watches nothing; a local context is staged and
// watches nothing. Counting those as gaps would mean no build with a base image
// ever earns a key, which is every build.
func watched(kind ir.OpKind) bool {
	return kind == ir.OpExec || kind == ir.OpFile
}

// saw takes one step's result.
func (w *skipWatch) saw(kind ir.OpKind, r core.Result) {
	w.places = append(w.places, r.Placements...)

	if !watched(kind) {
		return
	}

	switch {
	case !r.Observed:
		w.note(fmt.Sprintf("a %s step was not watched", kind))

	case r.Observation.Incomplete:
		w.note(fmt.Sprintf("a %s step's tracer missed something: %v",
			kind, r.Observation.Why))

	default:
		w.obs = append(w.obs, r.Observation)
	}
}

// note keeps the first reason this build cannot be keyed.
//
// The first rather than the last, and never cleared: a build with one
// unwatched step among fifty is a build whose key would be wrong, and a later
// step reporting cleanly says nothing about the one that did not.
func (w *skipWatch) note(why string) {
	if w.gap == "" {
		w.gap = why
	}
}

// hostInputs is 𝑅 for the whole build, or why it cannot be had.
func (w *skipWatch) hostInputs(contexts map[string]bool, root string) ([]hostInput, error) {
	if w.gap != "" {
		return nil, fmt.Errorf("%w: %s", ErrNotDerivable, w.gap)
	}

	merged := core.Observation{
		Reads:    map[string]ir.NodeID{},
		Listings: map[string]ir.NodeID{},
	}

	for _, one := range w.obs {
		maps.Copy(merged.Reads, one.Reads)
		maps.Copy(merged.Listings, one.Listings)

		merged.Negative = append(merged.Negative, one.Negative...)
	}

	return hostInputsFrom(contexts, w.places, merged, root)
}
