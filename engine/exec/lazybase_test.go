package exec

import (
	"context"
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// An executor with no primer stacks layers, as it always has.
//
// Every build today, and the property that makes the lazy path safe to have
// landed: without a primer the base is assembled exactly as it was, by the same
// code taking the same branch (E302).
func TestAnExecutorWithNoPrimerIsTheDefault(t *testing.T) {
	t.Parallel()

	if (&Executor{}).Prime != nil {
		t.Error("an executor offered to prime a base without being asked")
	}
}

// A primer is only used when there is something to prime with.
//
// Three things have to be true - a primer, a prediction, and a base to take it
// from - and each absence means the same thing: assemble the layers. A step
// nobody has seen before has no prediction; a step with no base has nothing to
// prime; an engine with no peers has no primer.
func TestAPrimerNeedsAPredictionAndABase(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		want  []string
		stack []ir.NodeID
		prime bool
	}{
		{name: "no prediction", stack: []ir.NodeID{{1}}, prime: true},
		{name: "no base", want: []string{"usr/bin/cc"}, prime: true},
		{name: "no primer", want: []string{"usr/bin/cc"}, stack: []ir.NodeID{{1}}},
	} {
		asked := false

		e := &Executor{}

		if tc.prime {
			e.Prime = func(context.Context, []ir.NodeID, []string, string) error {
				asked = true

				return errNoPrimer
			}
		}

		// The decision, without a guest: whether the primer is consulted at all.
		if e.wouldPrime(&ir.Node{Meta: ir.Meta{ReadsPredicted: tc.want}}, tc.stack) {
			t.Errorf("%s: primed anyway", tc.name)
		}

		if asked {
			t.Errorf("%s: the primer was called", tc.name)
		}
	}
}

// With all three, it primes.
func TestWithAPrimerAPredictionAndABaseItPrimes(t *testing.T) {
	t.Parallel()

	e := &Executor{
		Prime: func(context.Context, []ir.NodeID, []string, string) error { return nil },
	}

	n := &ir.Node{Meta: ir.Meta{ReadsPredicted: []string{"usr/bin/cc"}}}

	if !e.wouldPrime(n, []ir.NodeID{{1}}) {
		t.Error("a step with a prediction, a base and a primer moved its whole" +
			" base anyway")
	}
}

var errNoPrimer = errors.New("no primer")
