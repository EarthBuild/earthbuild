package core

import (
	"context"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// MaxStackDepth is 𝑛ₘₐₓ, green paper (4.8).
//
// overlayfs refuses more than 500 lower layers - OVL_MAX_STACK - and a target
// exceeding it fails with a bare `invalid argument` naming nothing (experiment
// E11: a 1,000-step target dies at exactly step 500). The engine flattens
// before it reaches the wall rather than reporting the kernel's error.
//
// The margin below 500 is deliberate: a stack is assembled and then mounted,
// sometimes with a scratch layer on top, so arriving at exactly the limit
// leaves nothing for the executor.
const MaxStackDepth = 480

// Flattening records that Φ was applied, and over what.
//
// It is returned rather than performed silently because flattening is a policy
// with a cost - it trades away per-step cache granularity across the squashed
// range - and green paper §4.6 requires the choice to appear in the build
// record instead of being an implementation detail nobody can see.
type Flattening struct {
	// From and To bound the squashed range, half-open, in the original stack.
	From, To int
	// Into is the identity of the layer the range collapses to.
	Into ir.NodeID
	// Was is how deep the stack was before.
	Was int
}

// Applied reports whether Φ did anything.
func (f Flattening) Applied() bool { return f.To > f.From }

// Flatten is Φ, green paper (4.8):
//
//	Φ(⟨ℓ₀ … ℓₙ⟩) ≡ ⟨ℓ₀ … ℓₖ, flatten(ℓₖ₊₁ … ℓₙ)⟩   when 𝑛 > 𝑛ₘₐₓ
//
// It squashes the **oldest** contiguous range, keeping the most recent layers
// individually addressable. That choice is not arbitrary. Edits land near the
// top of a stack - a source file changes, the last few steps rebuild - so
// granularity is worth most there, and the base is what stays unchanged between
// builds. Squashing the top would destroy exactly the cache hits that matter.
//
// The result is deterministic in its input, because a schedule that flattens
// differently between runs is a schedule that produces different keys between
// runs (green paper §4.7.3).
//
// squash derives the identity of a collapsed range. In S3 it becomes a real
// filesystem operation; here it is the identity function over the range, which
// is enough for the scheduler to be correct and for the choice to be tested.
func Flatten(stack []ir.NodeID, max int, squash func([]ir.NodeID) ir.NodeID) ([]ir.NodeID, Flattening) {
	if max < 2 {
		max = 2 // a stack must keep at least the squashed base and one layer
	}

	if len(stack) <= max {
		return stack, Flattening{Was: len(stack)}
	}

	// Keep the newest max-1 layers; squash everything older into one.
	keep := max - 1
	cut := len(stack) - keep

	into := squash(stack[:cut])

	out := make([]ir.NodeID, 0, max)
	out = append(out, into)
	out = append(out, stack[cut:]...)

	return out, Flattening{From: 0, To: cut, Into: into, Was: len(stack)}
}

// SquashID derives the identity of a squashed range.
//
// It is a hash over the range in order, domain-separated from every other key
// so a flattened layer can never be mistaken for a step result or a chain key.
// Content-derived, so two builds squashing the same range agree on the answer
// and share the cached result.
func SquashID(rng []ir.NodeID) ir.NodeID {
	h := ir.NewHasher()

	h.Byte(domainSquash)
	h.Count(len(rng))

	for _, id := range rng {
		h.Fixed(id[:])
	}

	return h.Sum()
}

const domainSquash = 0x03

// Squasher is an executor that can collapse a range of layers into one.
//
// Optional, and asked for by type assertion rather than added to Executor: a
// simulator has no filesystem to build a layer in, and requiring it would make
// every test double implement a method it cannot mean anything by.
//
// The contract is idempotent and content-addressed. `into` is derived from the
// range (SquashID), so a second call for the same range must be a no-op rather
// than a rebuild, and two builds that collapse the same layers share the result.
type Squasher interface {
	Squash(ctx context.Context, into ir.NodeID, rng []ir.NodeID) error
}
