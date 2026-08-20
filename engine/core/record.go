package core

import (
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Outcome is what the lookup path decided for a step.
type Outcome uint8

// The outcomes.
const (
	OutcomeMiss       Outcome = iota // executed
	OutcomeL1Hit                     // chain key hit
	OutcomeL2Hit                     // observed-input key hit
	OutcomeRefused                   // the engine cannot evaluate this construct (I10)
	OutcomeUncaptured                // executed, but what it produced was not captured
)

func (o Outcome) String() string {
	//nolint:exhaustive // the default is the remaining case
	switch o {
	case OutcomeL1Hit:
		return "L1 hit"
	case OutcomeL2Hit:
		return "L2 hit"
	case OutcomeRefused:
		return "refused"
	case OutcomeUncaptured:
		return "uncaptured"
	default:
		return "miss"
	}
}

// StepRecord is one step's entry in a build record, green paper B.2.
//
// It holds digests and structure, never content, which is what makes a record
// small enough to keep and fast enough to diff. The four component digests are
// not redundant with the chain key: the key says *that* two steps differ, and
// only the components say *which part* differs - which is the whole value of
// the record.
type StepRecord struct {
	// Seq is the step's position in the graph's deterministic traversal.
	//
	// Records are sorted by it after a build, so that a concurrent build and a
	// serial one produce identical records. Without it the order would be the
	// order goroutines happened to finish, and every tool that diffs two builds
	// would report noise.
	Seq int
	// Ident correlates a step across builds. It is *positional* - where the
	// step sits in the Earthfile - not content-derived.
	//
	// Node identity cannot serve here, and the reason is worth stating because
	// it is not obvious: a content identity changes whenever anything about the
	// step changes, which is exactly when attribution is wanted. Correlating on
	// it makes every command change report as "the graph changed shape", and
	// the finer causes can never fire.
	Ident string

	Node  ir.NodeID
	Class Key

	// Component digests, so divergence can be attributed rather than merely
	// located.
	Base ir.NodeID // ids(𝑏)
	Op   ir.NodeID // 𝒮(ω)
	Env  ir.NodeID // 𝒮(ε)
	Plat ir.NodeID // 𝒮(π)

	ChainKey    Key
	ObservedKey Key

	Layer   ir.NodeID
	Exit    int
	Bytes   int64
	Outcome Outcome

	// Flattened records that Φ was applied and over what, because flattening
	// trades away cache granularity and a build where it happened behaves
	// differently from one where it did not (green paper §4.6).
	Flattened Flattening

	// Observation is what the step looked at, and Observed says whether anyone
	// was watching. Carried in full - paths and digests, never content - because
	// B.5's report has to name the files that changed, and a digest of the
	// observation can only say *that* it changed.
	Observation Observation
	Observed    bool
	// ObsDigest summarises the observation, so two records can be compared for
	// equality without walking every path.
	ObsDigest ir.NodeID

	// Meta is carried for diagnostics only and never compared.
	Meta ir.Meta
}

// Record is a build record: the step graph with its outcomes, green paper B.2.
type Record struct {
	Steps []StepRecord
}

// find returns the record for a step position, if present.
func (r *Record) find(ident string) (StepRecord, bool) {
	for _, s := range r.Steps {
		if s.Ident == ident {
			return s, true
		}
	}

	return StepRecord{}, false
}

// Cause classifies why two builds diverged at a step.
type Cause uint8

// The causes, ordered from most to least actionable.
const (
	CauseNone Cause = iota
	// CauseGraphShape: the step exists in one build and not the other.
	CauseGraphShape
	// CauseBase: the step ran over different inputs.
	CauseBase
	// CauseOp: the command changed.
	CauseOp
	// CauseEnv: an environment value in the key changed.
	CauseEnv
	// CausePlatform: the target platform changed.
	CausePlatform
	// CauseNonDeterminism: nothing in the key changed and the output did
	// anyway.
	//
	// This is the most valuable diagnostic a build tool can emit, and no
	// chain-keyed system can emit it, because it does not know what the step
	// actually depended on.
	CauseNonDeterminism
)

func (c Cause) String() string {
	switch c {
	case CauseGraphShape:
		return "the graph changed shape"
	case CauseBase:
		return "the step ran over different inputs"
	case CauseOp:
		return "the command changed"
	case CauseEnv:
		return "an environment value changed"
	case CausePlatform:
		return "the platform changed"
	case CauseNonDeterminism:
		return "NON-DETERMINISM: nothing in the key changed and the output did"
	default:
		return "no divergence"
	}
}

// Divergence is the first point at which two builds differ.
type Divergence struct {
	Cause Cause
	// Step is the node at which they diverged, zero for CauseNone.
	Step ir.NodeID
	// Meta describes that step, for a human.
	Meta ir.Meta
	// A and B are the two records' entries. B is zero for CauseGraphShape.
	A, B StepRecord
}

func (d Divergence) String() string {
	if d.Cause == CauseNone {
		return "identical"
	}

	where := d.Meta.Description
	if where == "" {
		where = d.Step.String()[:12]
	}

	return fmt.Sprintf("%s: %s", where, d.Cause)
}

// Diverge finds the earliest step at which two builds differ, green paper B.4.
//
// One walk over two lists of digests, answering six questions that look
// unrelated: why did this rebuild, is this step deterministic, which worker is
// lying, why does it work locally but not in CI, what did that dependency bump
// change, and which change broke it. The records differ; the algorithm does not.
//
// It compares recorded digests rather than rebuilding, so it costs milliseconds
// where `git bisect` costs a build per probe - and it names the *step*, which is
// usually the more useful answer than the commit.
func Diverge(a, b *Record) Divergence {
	for _, sa := range a.Steps {
		sb, ok := b.find(sa.Ident)
		if !ok {
			return Divergence{Cause: CauseGraphShape, Step: sa.Node, Meta: sa.Meta, A: sa}
		}

		if sa.Layer == sb.Layer {
			continue
		}

		d := Divergence{Step: sa.Node, Meta: sa.Meta, A: sa, B: sb}

		// Attribute in the order a human would check: what it ran over, then
		// what it ran, then the ambient state, then the platform.
		switch {
		case sa.Base != sb.Base:
			d.Cause = CauseBase
		case sa.Op != sb.Op:
			d.Cause = CauseOp
		case sa.Env != sb.Env:
			d.Cause = CauseEnv
		case sa.Plat != sb.Plat:
			d.Cause = CausePlatform
		default:
			// Every component of the key is identical and the results are not.
			// The step is non-deterministic, and that is the finding.
			d.Cause = CauseNonDeterminism
		}

		return d
	}

	// A is a prefix of B, or they agree everywhere A has steps.
	for _, sb := range b.Steps {
		if _, ok := a.find(sb.Ident); !ok {
			return Divergence{Cause: CauseGraphShape, Step: sb.Node, Meta: sb.Meta, A: sb}
		}
	}

	return Divergence{Cause: CauseNone}
}
