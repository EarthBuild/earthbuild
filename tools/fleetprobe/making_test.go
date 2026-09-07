package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The driver's own executor runs as many steps at once as it says, and no more.
//
// **Without this every measurement of the driver is optimistic.** A synthetic
// step is a sleep, so an executor with no limit runs eight of them in the time
// of one - and the machine that keeps work under E320 looks infinitely parallel
// while the worker it is compared against was given room for two. E271 made
// exactly this point about workers and the driver was left out of it.
//
// *Failure class: comparing a cost against the wrong denominator.* Fourth
// sighting, and the one that would have flattered the fix rather than a rival.
func TestTheLocalExecutorRunsWhatItSaysAtOnce(t *testing.T) {
	t.Parallel()

	m := &making{store: layersIn(t), size: 16, compute: 50 * time.Millisecond}
	m.roomFor(2)

	began := time.Now()

	var wg sync.WaitGroup

	for range 6 {
		// `wg.Go` keeps `Add(1)` and `Done()` in one place (modernize).
		wg.Go(func() {
			_, err := m.Run(context.Background(), &ir.Node{}, core.Worker{}, nil, nil)
			if err != nil {
				t.Errorf("%v", err)
			}
		})
	}

	wg.Wait()

	// Six steps of 50ms, two at a time: three waves, so 150ms at the least.
	if took := time.Since(began); took < 150*time.Millisecond {
		t.Errorf("six 50ms steps two at a time took %v, want at least 150ms"+
			"\n  an executor with no limit makes one machine look like a fleet",
			took)
	}
}

// layersIn is a store in a directory that goes away with the test.
func layersIn(t *testing.T) *fleet.Layers {
	t.Helper()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "layers"), 0o750)
	if err != nil {
		t.Fatalf("%v", err)
	}

	return &fleet.Layers{Root: root}
}

// A step that was given a prediction can be made to read outside it.
//
// **The probe predicted perfectly, so the cost of being wrong was unmeasured.**
// E327 gave a worker a way to survive a bad hint - fetch the whole base and run
// again - and left the obvious question open: how often can a prediction be
// wrong before lazy transfer stops paying for itself?
//
// A step faults only when it was *given* a prediction. The retry arrives with
// that field cleared (E327), so this needs no memory of which step it is: the
// mechanism under measurement is what tells the two attempts apart.
func TestAStepCanBeMadeToReadOutsideItsPrediction(t *testing.T) {
	t.Parallel()

	m := &making{store: layersIn(t), size: 16, miss: 1}

	_, err := m.Run(context.Background(),
		&ir.Node{Meta: ir.Meta{ReadsPredicted: []string{"usr/lib/lib0.so"}}},
		core.Worker{}, nil, nil)
	if !errors.Is(err, core.ErrInputMissing) {
		t.Errorf("a step told to mispredict returned %v, want a missing input", err)
	}

	// The retry, which arrives with no prediction.
	_, err = m.Run(context.Background(), &ir.Node{}, core.Worker{}, nil, nil)
	if err != nil {
		t.Errorf("the retry failed: %v", err)
	}
}

// A chain is what a build's critical path looks like.
//
// **Three correct changes in a row bought nothing** (E335, E340, E342) because
// the only arrangement this probe measures is a fan-out from one base: sixteen
// steps ready at once, every worker saturated in milliseconds, and no start-up
// cost able to show.
//
// A chain is the opposite and is the shape of every real build's critical path:
// one step at a time, each standing on what the last produced, so a fleet has no
// parallelism to sell and can only win by not costing anything. Whether it
// manages that is the question three experiments could not ask (E343).
func TestAChainStandsOnWhatCameBefore(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	steps := chainFrom(base, 3, 4096)

	if len(steps) != 3 {
		t.Fatalf("a chain of three has %d step(s)", len(steps))
	}

	if got := steps[0].Base; len(got) != 1 || got[0] != base {
		t.Errorf("the first step stands on %v, not the seeded base", got)
	}

	for i := 1; i < len(steps); i++ {
		if got := steps[i].Base; len(got) != 1 || got[0] != steps[i-1].Produces {
			t.Errorf("step %d stands on %v, not on what step %d produced",
				i, got, i-1)
		}
	}
}

// Levels are what a real build graph is.
//
// **Neither shape measured so far is a build.** A fan-out is every step ready at
// once, which flatters a fleet; a chain is one at a time, which cannot use one.
// A real graph is levels: some parallelism, then a barrier, then more - and the
// fleet has to win the parallel part by more than it loses on the barriers
// (E345).
func TestLevelsAreParallelStepsWithBarriersBetween(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	steps := levelsFrom(base, 3, 2, 4096)

	if len(steps) != 6 {
		t.Fatalf("three levels of two is %d step(s), want 6", len(steps))
	}

	// The first level stands on the seeded base.
	for i := range 2 {
		if got := steps[i].Base; len(got) != 1 || got[0] != base {
			t.Errorf("step %d of the first level stands on %v", i, got)
		}
	}

	// Every later level stands on something the level before produced.
	made := map[ir.NodeID]bool{}
	for i := range 2 {
		made[steps[i].Produces] = true
	}

	for i := 2; i < 4; i++ {
		if got := steps[i].Base; len(got) != 1 || !made[got[0]] {
			t.Errorf("step %d stands on %v, which the level before did not make",
				i, got)
		}
	}
}

// Every seeded base is a different layer, so every round is cold.
//
// **The noise floor is about fifteen per cent** at this scale: four workers on
// the same build measured 1.467s, 1.527s and 1.674s. Every change evaluated
// below that has been unmeasurable, and three were reported as "no effect" when
// what they are is "smaller than the spread" (E340, E342, E346, E349).
//
// Repeating inside one process removes the process from the variance. What it
// must not remove is the transfer: a second round on the *same* base is a warm
// fleet, which is a different regime rather than another sample of this one.
//
// The salt is what makes them differ, and it was deleted once: on darwin two
// seeds already differed, because a layer's identity includes its directories'
// mtimes (§3.3) and those were far enough apart. On Linux they were not, and the
// same test failed there - so the salt is content, which is the same on every
// filesystem (E357).
func TestEverySeededBaseIsADifferentLayer(t *testing.T) {
	t.Parallel()

	store := layersIn(t)

	first, _, err := seedBase(store, 4, 0)
	if err != nil {
		t.Fatalf("%v", err)
	}

	second, _, err := seedBase(store, 4, 1)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if first == second {
		t.Error("two seeded bases are the same layer, so every round after the" +
			" first would transfer nothing and measure a warm fleet (E349)")
	}
}
