package core

import "github.com/EarthBuild/earthbuild/engine/ir"

// Speculation says what may be done about a step before it is known to be
// needed.
//
// Three tiers rather than one switch, because "safe to speculate" is not one
// question. They are ordered by what being wrong costs, and the arithmetic
// below relies on that order: the weakest tier of everything involved wins.
type Speculation uint8

const (
	// SpeculateNever is work that cannot be taken back.
	//
	// A host step touches this machine, a `--no-cache` step was declared not to
	// be a function of its inputs, and a push reaches a registry. Running one of
	// these on a guess is not wasted work but a side effect that should not have
	// happened: there is no layer to discard afterwards, and no amount of
	// confidence makes `rm -rf build` retractable.
	SpeculateNever Speculation = iota
	// SpeculateRetryable is work whose result is a content-keyed layer.
	//
	// A wrong guess leaves a layer nobody uses, which is exactly the cost of a
	// cache miss - the shape this engine keeps arriving back at. A right guess
	// has the work already done.
	SpeculateRetryable
	// SpeculateFreely is work that only moves bytes.
	//
	// Pulling an image or staging a context changes nothing: wrong, it costs
	// bandwidth; right, it takes a transfer off the critical path. Worth doing
	// on any prediction at all, however weak, which is why it is a tier of its
	// own rather than the top of the retryable one.
	SpeculateFreely
)

func (s Speculation) String() string {
	switch s {
	case SpeculateNever:
		return "never"
	case SpeculateRetryable:
		return "retryable"
	case SpeculateFreely:
		return "freely"
	}

	return "unknown"
}

// MaySpeculate reports what may be done about a step before the branch that
// needs it is known.
//
// Transitive, and that is the part worth stating: an ordinary `RUN` looks
// perfectly retryable on its own, and if it stands on a `LOCALLY` step then
// speculating on it means running that LOCALLY step first. The question is never
// about one node.
//
// Ordering edges are deliberately not followed. `WAIT` says a step must not
// finish before another, which is about when work lands rather than what it
// costs to guess; treating it as a barrier would suppress speculation for
// everything after a WAIT block - a performance cliff at exactly the construct
// people reach for when they care about correctness.
func MaySpeculate(n *ir.Node) Speculation {
	return maySpeculate(n, map[ir.NodeID]Speculation{})
}

func maySpeculate(n *ir.Node, seen map[ir.NodeID]Speculation) Speculation {
	if n == nil {
		return SpeculateFreely
	}

	if s, ok := seen[n.ID()]; ok {
		return s
	}

	// Recorded before descending, so a graph that revisits a node does not walk
	// it twice. Freely is the identity for the minimum below, so an unfinished
	// entry cannot make an answer weaker than it should be.
	seen[n.ID()] = SpeculateFreely

	worst := ownSpeculation(n)

	for _, in := range n.Inputs {
		worst = min(worst, maySpeculate(in, seen))
	}

	// Sources count for the same reason inputs do: a step cannot run until what
	// it reads has been produced, whether or not it stands on it.
	for _, src := range n.Sources {
		worst = min(worst, maySpeculate(src, seen))
	}

	seen[n.ID()] = worst

	return worst
}

// ownSpeculation is the tier a step would have if it stood on nothing.
func ownSpeculation(n *ir.Node) Speculation {
	if n.Op.NoCache {
		return SpeculateNever
	}

	// A step given a docker daemon puts things in one that outlives the build.
	// The next block sees them whether or not the branch that asked for them was
	// taken, which is a side effect that cannot be taken back - and it is the
	// same state that makes these steps uncacheable in the first place.
	if n.Op.Docker {
		return SpeculateNever
	}

	switch n.Op.Kind {
	case ir.OpHost:
		return SpeculateNever

	case ir.OpImage, ir.OpLocal:
		// Fetching an image and digesting a build context both only read.
		return SpeculateFreely

	case ir.OpScratch:
		// The empty base does nothing at all - it reads nothing, writes nothing
		// and costs nothing, so a wrong guess about it costs nothing either
		// (E468).
		return SpeculateFreely

	case ir.OpExec, ir.OpFile, ir.OpMerge, ir.OpBuild:
		return SpeculateRetryable

	case ir.OpPackImage:
		// Writes an OCI layout into the build cache under a content-derived
		// name. A wrong guess leaves a file nobody uses, which is the cost of a
		// cache miss - not a side effect.
		return SpeculateRetryable
	}

	// An operation this does not recognise is not one it can promise anything
	// about. Refusing to speculate on it costs a little speed; guessing wrong
	// about a kind added later could cost a side effect.
	return SpeculateNever
}
