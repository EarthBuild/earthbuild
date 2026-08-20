package fleet_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every mechanism this project measured is wired into what people run.
//
// **The same shape, five times.** An instrument that was never installed (E312),
// a rate that nothing fed (E319), a safeguard that changed nothing (E325), two
// fields only the probe set (E330), and a worker serving whole layers while its
// fragments sat beside it (E331). Each was built, tested in isolation, measured
// in the probe, and absent from the binary.
//
// A unit test of a mechanism passes whether or not anything calls it, and every
// one of these had one. So this checks the **call**, by reading the source,
// which is a poor test in every way except the one that matters: it is the only
// kind that fails when a wiring is dropped.
//
// It is deliberately a table rather than five tests. The next time this class
// appears the answer is a row, and a list of what a real build is supposed to
// switch on is worth having in one place.
//
// **The snippets are exact for a reason.** The first version of the last row
// looked for `&fleet.Parts{` and a mutant that dropped the fragments half of it
// survived: a guard on the shape of a call and not its substance is the same
// mistake one level up.
func TestEveryMeasuredMechanismIsWiredIn(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ file, calls, why string }{
		{"engine/fleet/driver.go", "wire(d, DefaultCapacity(), store)",
			"a driver with no stated capacity finishes every step in one wave," +
				" and one that cannot size a layer prices the network at nothing" +
				" (E330)"},
		{"cmd/earth-worker/main.go", "fleet.WithFragments(frags",
			"a worker that does not provision fragments fetches whole layers," +
				" which is 2.8x slower than one machine (E326)"},
		{"cmd/earth-worker/main.go", "fleet.WithPeerSink(peers)",
			"a step faults in from an address chosen before any assignment" +
				" existed, which speaks the wrong protocol (E329)"},
		{"engine/fleet/driver.go", "d.Remember(store)",
			"a build that does not load what the last one measured is round" +
				" one, and round one delegates everything and keeps nothing" +
				" (E350, E351)"},
		{"engine/fleet/driver.go", "_ = d.Keep()",
			"a build that measures its fleet and does not write it down leaves" +
				" the next one to earn the same knowledge again (E351)"},
		{"engine/fleet/driver.go", "if src, ok := r.SourceFor(at); ok {",
			"a driver that dials a worker to fetch what it produced reaches a" +
				" machine that may be behind a NAT, and the back-channel it" +
				" opened is right there (E279, E347)"},
		{"cmd/earth-worker/main.go", "&fleet.Parts{Whole: layers, Some: frags}",
			"a worker serving only whole layers cannot pass on the part of a" +
				" base it just fetched, so lazy transfer is a star (E325, E331)"},
	} {
		b, err := os.ReadFile(filepath.Join("..", "..", c.file))
		if err != nil {
			t.Fatalf("%v", err)
		}

		if !strings.Contains(string(b), c.calls) {
			t.Errorf("%s does not call %s\n  %s", c.file, c.calls, c.why)
		}
	}
}
