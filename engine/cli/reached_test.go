package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// state is what a build does with a mechanism the plan calls real.
type state int

const (
	// reached: an ordinary build calls it.
	reached state = iota
	// gated: it is called only when an optional port is configured, and the
	// port is named. A build with the default configuration never gets there.
	gated
)

// mechanism is one row of the plan's stage table, made checkable.
type mechanism struct {
	// call is the text a caller must contain. The outermost function, not a
	// helper: a guard that accepts any caller anywhere goes green the moment
	// somebody extracts a private function, which is how the first version of
	// the Diverge guard passed while nothing reached it (E82).
	call string
	// calledFrom is the file on a build's path that must contain the call.
	//
	// A named file rather than "anywhere but the defining package", which was
	// the first version and immediately produced two false positives: the
	// scheduler calls Κ₁ and Φ from inside `core`, and that is exactly the path
	// a build takes. **The question is not where a mechanism is called from but
	// whether the caller is on the path**, and the only honest way to say that
	// is to name the file and let it be wrong out loud.
	calledFrom string
	state      state
	// gatedBy names the core.Scheduler port whose absence gates it, and must be
	// one the port table already declares inert. Two tables about the same fact
	// drift; this makes the drift a failure.
	gatedBy string
	// why says what that absence means, for a reader who has not read both.
	why string
}

// stageZero is what the plan's stage table claims is **real** at S0:
// "Κ₁, Κ₂, Φ, Λ, records, first-divergence reporting".
//
// Every one of those is implemented, tested, and correct - and `Diverge` had no
// caller for the life of the branch, because a record was assembled every build
// and dropped at exit (E82). **The column's word was doing two jobs**:
// *implemented* and *reached*, which came apart without the table being wrong.
//
// So the two are separated here, where a change can fail rather than in prose a
// reader has to interpret. A mechanism is `reached` when an ordinary build calls
// it, and `gated` when it is called only behind a port the front end does not
// set - which is a true and useful thing to say, and not the same as real.
var stageZero = map[string]mechanism{
	"Κ₁, the chain key": {
		call: "DeriveChainKey(", calledFrom: testGoFile, state: reached,
	},
	// Reached at E125. It was gated for the life of the branch because nothing
	// set `Result.Observed`, so every profile would have been empty - and an
	// empty observation agrees with every base, which is a false hit rather
	// than a missing feature (E112).
	//
	// What unblocked it was a source that needs no tracer: the guest performs a
	// COPY's reads itself, so it can say what they were (E119). A COPY's
	// observation is about its *destination* in the base, which is what makes
	// the claim exact - a copy of an unchanged file into an unchanged
	// destination cannot produce a different layer, however much the base image
	// moved underneath it.
	"Κ₂, the observed key": {
		call: "DeriveObservedKey(", calledFrom: testGoFile, state: reached,
	},
	"Φ, flattening": {
		call: "Flatten(", calledFrom: testGoFile, state: reached,
	},
	"Λ, lookup": {
		call: "Lookup(", calledFrom: testGoFile, state: reached,
	},
	// The one that had no caller at all. `records.go` calls it, and
	// TestABuildAsksWhyItReran checks that `cli.go` calls `records.go` - two
	// links, because a guard that accepts any caller anywhere went green the
	// moment the first link was written and the second did not exist (E82).
	"first-divergence reporting": {
		call: "Diverge(", calledFrom: "cli/records.go", state: reached,
	},
}

// Everything the plan calls real at S0 is called by something.
//
// The generalisation of two guards this branch wrote one at a time, and the
// reason to generalise: each was written *after* finding the mechanism it
// guards had no caller. This asks the question of the whole row at once, so the
// next one is found by a test rather than by an audit that happened to look.
//
// Source-level, with the limits that implies - it proves a call exists, not
// that a build executes it. The behavioural half lives beside each mechanism
// (engine/core/conflictpath_test.go for the cache, records_test.go for the
// divergence). Neither replaces the other: this one notices an absent call, and
// only that one notices an unreachable one.
func TestEveryStageZeroMechanismIsCalled(t *testing.T) {
	t.Parallel()

	// A claim about *every* member of a table is satisfied by a table with no
	// members. This one is the stage register - what S0 says it does - so an
	// emptied or renamed table would report the strongest claim in the plan as
	// verified while checking nothing.
	if len(stageZero) < 4 {
		t.Fatalf("the stage-zero register holds %d mechanisms, which is fewer than"+
			" the stage table claims", len(stageZero))
	}

	for name, m := range stageZero {
		if m.state == gated {
			continue
		}

		b, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(m.calledFrom)))
		if err != nil {
			t.Errorf("%s: %s does not exist, so the claim about it cannot be checked: %v",
				name, m.calledFrom, err)

			continue
		}

		if !strings.Contains(string(b), m.call) {
			t.Errorf("%s: engine/%s does not call %s"+
				"\n  the plan's stage table calls this real, and real has to mean reached",
				name, m.calledFrom, m.call)
		}
	}
}

// A gated mechanism names a port the port table agrees is unset.
//
// Two tables about one fact, and the cross-check is the point. `schedulerPorts`
// says which ports the front end leaves alone and why; this says which
// mechanisms are dead as a result. Either can be edited without the other, and
// then one of them is quietly wrong - the shape that put "real" next to a
// mechanism nothing called for the life of the branch.
//
// So a gated row must name a port, the port must exist, and the port table must
// still call it inert. **The day somebody wires up `Profiles`, this fails and
// says Κ₂ has woken up** - which is the good news arriving as a red test rather
// than as nothing at all.
func TestAGatedMechanismNamesAnInertPort(t *testing.T) {
	t.Parallel()

	for name, m := range stageZero {
		if m.state != gated {
			if m.why != "" || m.gatedBy != "" {
				t.Errorf("%s is not gated, so its reason describes nothing: %q", name, m.why)
			}

			continue
		}

		if len(strings.Fields(m.why)) < 8 {
			t.Errorf("%s is gated for a reason too short to be one: %q", name, m.why)
		}

		p, ok := schedulerPorts[m.gatedBy]
		if !ok {
			t.Errorf("%s is gated by %q, which is not a port of core.Scheduler", name, m.gatedBy)

			continue
		}

		if p.role != inert {
			t.Errorf("%s is recorded as gated, but the port table no longer calls %s inert"+
				"\n  one of the two is stale: either the mechanism is reached now,"+
				"\n  or the port is set and this row should say so", name, m.gatedBy)
		}
	}
}
