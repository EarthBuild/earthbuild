package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// The step that goes out to learn does not wait for itself.
//
// Pricing a fleet needs one transfer to happen, so the first step whose inputs
// are worth shipping goes out alone and the others wait for what it learns. If
// the pilot waited too there would be nobody to teach it: every step would sit
// on a gate that only a step going out can open, for the whole of `PilotWait`,
// on every build with a cold rate (E319).
//
// Thirty seconds, once, on a machine that had work to do. The pilot is not
// privileged and is not retried - it simply must not join the queue it exists to
// end.
func TestThePilotDoesNotWaitForItself(t *testing.T) {
	t.Parallel()

	d := &Delegating{Local: local(core.Result{}), Store: &countingKeeper{}, Room: 4}

	began := time.Now()
	d.learn(context.Background(), Assignment{Hints: Hints{Bytes: 1024}})
	took := time.Since(began)

	// Generous, because what is being distinguished is "returned" from "waited
	// thirty seconds": anything in between is still a pass and still correct.
	if took > PilotWait/10 {
		t.Errorf("the first step took %v to be let go, against a PilotWait of"+
			" %v: it is waiting on the gate that only it can open, so every"+
			" step on a cold rate pays the whole bound", took, PilotWait)
	}

	// And the second caller is the one that waits, or the gate does nothing at
	// all and the pricing it exists for never happens.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	began = time.Now()
	d.learn(ctx, Assignment{Hints: Hints{Bytes: 1024}})

	if time.Since(began) < 100*time.Millisecond {
		t.Error("the second step was not held at all: nothing waits for the" +
			" measurement, so a fleet is priced by a constant again")
	}
}
