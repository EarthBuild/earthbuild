package exec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// errBrokenPrimer is what a primer that cannot prime says, so that a caller
// receiving it can be told apart from one that fell back.
var errBrokenPrimer = errors.New("the primer is broken")

// A primer that cannot prime makes the build slower, not failed.
//
// **Every mechanism the lazy base rests on was built to degrade** (I11, E302).
// Priming is an optimisation: it hands the guest a directory holding the paths a
// step is predicted to read, instead of the whole stack of layers. When it does
// not work - no space, a bad prediction, a primer that errors - the step is
// still perfectly buildable the way it always was, by stacking the layers.
//
// So the failure is dropped and the ordinary path taken. Returning it would turn
// an optimisation into a new way for builds to fail, which is the one thing an
// optimisation must not be.
//
// The test does not require the fallback to *succeed* - this loopback guest has
// no such layer to stack - only that the primer's failure is not what comes
// back. That is the whole difference between the two paths and it is what the
// mutant changes: with the fallback removed, `base` returns the primer's error
// verbatim.
func TestAPrimerThatCannotPrimeFallsBackToTheStack(t *testing.T) {
	// Not parallel: it serves a guest over a pipe and takes a temporary root.
	c, err := guest.Dial(LoopbackConn())
	if err != nil {
		t.Skipf("no loopback guest here: %v", err)
	}

	asked := false

	e := &Executor{
		Scratch: t.TempDir(),
		Prime: func(context.Context, []ir.NodeID, []string, string) error {
			asked = true

			return errBrokenPrimer
		},
	}

	// A prediction and a base, which is what makes priming worth trying at all.
	n := &ir.Node{Meta: ir.Meta{ReadsPredicted: []string{"usr/bin/cc"}}}

	h, release, err := e.base(context.Background(), c, n, []ir.NodeID{{1}})
	if release != nil {
		defer release()
	}

	if h != nil {
		_ = h.Release()
	}

	// Without this the test would pass against an executor that never primed,
	// which is a different thing entirely and would prove nothing.
	if !asked {
		t.Fatal("the primer was never called, so this measured nothing")
	}

	if err != nil && strings.Contains(err.Error(), errBrokenPrimer.Error()) {
		t.Errorf("the primer's failure was handed to the caller: %v"+
			"\n  priming is an optimisation, and one that fails should cost a"+
			" slower build rather than a failed one (I11, E302)", err)
	}
}
