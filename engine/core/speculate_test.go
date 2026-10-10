package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func img() *ir.Node {
	return &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}}
}

func exe(args string, on *ir.Node) *ir.Node {
	return &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{args}}, Inputs: []*ir.Node{on}}
}

// What may be done before the answer is known, divided by what it costs to be
// wrong.
//
// Three tiers rather than one switch, because "safe to speculate" is not one
// question. Moving bytes is always safe. Running a step whose result is a
// content-keyed layer is safe because a wrong guess leaves a layer nobody uses -
// the same shape as a cache miss. Running something that touches the machine,
// the clock or a registry is not speculation but a side effect that should not
// have happened, and no amount of confidence makes it retractable.
func TestWhatMayBeSpeculatedOn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		node *ir.Node
		want core.Speculation
	}{
		{
			name: "pulling an image only moves bytes",
			node: img(),
			want: core.SpeculateFreely,
		},
		{
			name: "an ordinary step leaves a layer nobody has to use",
			node: exe(testCommand, img()),
			want: core.SpeculateRetryable,
		},
		{
			name: "a LOCALLY step touches this machine",
			node: &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{"rm -rf build"}}},
			want: core.SpeculateNever,
		},
		{
			name: "a --no-cache step is not a function of its inputs",
			node: &ir.Node{
				Op:     ir.Op{Kind: ir.OpExec, Args: []string{"fetch"}, NoCache: true},
				Inputs: []*ir.Node{img()},
			},
			want: core.SpeculateNever,
		},
		{
			name: "a tolerated step is still just a step",
			node: &ir.Node{
				Op:     ir.Op{Kind: ir.OpExec, Args: []string{testStep}, Tolerate: true},
				Inputs: []*ir.Node{img()},
			},
			want: core.SpeculateRetryable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := core.MaySpeculate(tc.node); got != tc.want {
				t.Errorf("%v, want %v", got, tc.want)
			}
		})
	}
}

// A step is only as speculable as what it stands on.
//
// The transitive part is the one that is easy to get wrong and expensive to get
// wrong: an ordinary `RUN` looks perfectly retryable on its own, and if it
// stands on a `LOCALLY` step then speculating on it means running that LOCALLY
// step first. The question is never about one node.
func TestSpeculationIsLimitedByWhatAStepStandsOn(t *testing.T) {
	t.Parallel()

	host := &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{"prepare"}}}

	if got := core.MaySpeculate(exe(testCommand, host)); got != core.SpeculateNever {
		t.Errorf("a step standing on a LOCALLY reports %v", got)
	}

	// Two steps down, the same answer: the reason does not weaken with
	// distance.
	if got := core.MaySpeculate(exe("package", exe(testCommand, host))); got != core.SpeculateNever {
		t.Errorf("two steps above a LOCALLY reports %v", got)
	}

	// And a source it reads without standing on counts too: it still has to be
	// produced before this step can run.
	reading := &ir.Node{
		Op:      ir.Op{Kind: ir.OpFile, Args: []string{"a", "b"}},
		Inputs:  []*ir.Node{img()},
		Sources: []*ir.Node{host},
	}

	if got := core.MaySpeculate(reading); got != core.SpeculateNever {
		t.Errorf("a step reading from a LOCALLY reports %v", got)
	}
}

// The weakest tier wins, which is what makes the answer safe to act on.
func TestTheWeakestTierWins(t *testing.T) {
	t.Parallel()

	// An image is free, a step on it is retryable: the pair is retryable, not
	// free - moving bytes is safe but running the step is only nearly safe.
	if got := core.MaySpeculate(exe(testCommand, img())); got != core.SpeculateRetryable {
		t.Errorf("got %v, want the weaker of the two", got)
	}
}

// An ordering edge does not restrict what may be speculated.
//
// WAIT says a step must not *finish* before another, which is about when work
// lands rather than what it costs to guess. Treating it as a barrier would make
// a WAIT block suppress speculation for everything after it, which is a
// performance cliff at exactly the construct people reach for when they care
// about correctness.
func TestAnOrderingEdgeDoesNotForbidSpeculation(t *testing.T) {
	t.Parallel()

	host := &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{"push"}}}

	n := exe("build", img())
	n.After = []*ir.Node{host}

	if got := core.MaySpeculate(n); got != core.SpeculateRetryable {
		t.Errorf("an ordering edge changed the tier to %v", got)
	}
}

// Packing an image may be speculated on; loading one may not.
//
// The two differ in what a wrong guess costs. Packing writes an OCI layout into
// the build cache under a content-derived name - a file nobody uses, which is
// the cost of a cache miss. Loading puts an image into a daemon that outlives
// the build, where the next block will see it whether or not the branch that
// asked for it was taken: a side effect that cannot be taken back, and the same
// state that makes these steps uncacheable in the first place.
func TestPackingIsSpeculableAndLoadingIsNot(t *testing.T) {
	t.Parallel()

	pack := &ir.Node{Op: ir.Op{Kind: ir.OpPackImage, Args: []string{"app:latest"}}}
	if got := core.MaySpeculate(pack); got != core.SpeculateRetryable {
		t.Errorf("packing an image is %v, want retryable", got)
	}

	load := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"docker load"}, Docker: true}}
	if got := core.MaySpeculate(load); got != core.SpeculateNever {
		t.Errorf("a step with a daemon is %v, want never", got)
	}
}

// And the ban is transitive, as every other one is: a step standing on one that
// must not be speculated on cannot be either, because running it means running
// that one first.
func TestWhatStandsOnADockerStepIsNotSpeculable(t *testing.T) {
	t.Parallel()

	load := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"docker load"}, Docker: true}}
	after := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{testStep}}, Inputs: []*ir.Node{load}}

	if got := core.MaySpeculate(after); got != core.SpeculateNever {
		t.Errorf("a step standing on a daemon step is %v, want never", got)
	}
}
