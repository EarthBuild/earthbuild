package cli

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A build containing LOCALLY cannot be auto-skipped.
//
// **Two independent reasons, and the second is the one that bites.** `LOCALLY`
// runs on the invoking machine with no sandbox, so nothing watched it and its
// reads are unknown - unknown is not empty. But it also *writes* on that
// machine, and a side effect is not an input: a perfect read set would not
// make skipping it safe, because what the build produced outside its own layers
// would simply not happen.
//
// It reached here by falling through. `gapIn` skipped every kind `watched` did
// not name, and `watched` named only OpExec and OpFile - so OpHost was passed
// over in silence, the key was recorded, and the next build skipped the host
// command.
func TestALocallyStepCannotBeKeyed(t *testing.T) {
	t.Parallel()

	rec := &core.Record{Steps: []core.StepRecord{
		{Kind: ir.OpImage, Outcome: core.OutcomeMiss},
		{Kind: ir.OpHost, Outcome: core.OutcomeMiss, Meta: ir.Meta{Source: "Earthfile:12"}},
	}}

	why := gapIn(rec, profilesOf{})
	if why == "" {
		t.Fatal("a build containing LOCALLY was keyed, so the next one will skip" +
			"\n  the host command and whatever it writes outside the build")
	}

	if !strings.Contains(why, "Earthfile:12") || !strings.Contains(why, "LOCALLY") {
		t.Errorf("refused with %q, want the line and the word LOCALLY -"+
			"\n  the reader has to know which step to look at and why", why)
	}
}

// A cached LOCALLY is refused too.
//
// Serving one from cache says its *layers* were reproduced, never that its
// host effects were. Keying on the outcome would skip exactly the builds that
// had already paid to learn the answer.
func TestACachedLocallyStepIsRefusedToo(t *testing.T) {
	t.Parallel()

	rec := &core.Record{Steps: []core.StepRecord{
		{Kind: ir.OpHost, Outcome: core.OutcomeL1Hit, Meta: ir.Meta{Source: "Earthfile:12"}},
	}}

	if gapIn(rec, profilesOf{}) == "" {
		t.Error("a cached LOCALLY was keyed: a hit reproduces layers, not host effects")
	}
}

// Every op kind is classified, so a new one cannot join the benign set by
// being forgotten.
//
// This is the E468 lesson applied to a second table: `OpScratch`'s own comment
// records that an opcode added in the middle renumbers the rest, and the guard
// that counts them is the only reason it was caught. The same hazard lives
// here - a kind nobody classifies is silently keyable, and the failure is a
// build that does not run.
func TestEveryOpKindIsClassifiedForSkipping(t *testing.T) {
	t.Parallel()

	for kind := ir.OpImage; kind <= ir.OpScratch; kind++ {
		if kind.String() == "" || strings.HasPrefix(kind.String(), "OpKind(") {
			continue
		}

		if !watched(kind) && !placing(kind) && !benign(kind) && !refuses(kind) {
			t.Errorf("%v is in no class: it is neither watched, placing, benign"+
				" nor refused,"+
				"\n  so a build containing it is keyed without anyone deciding that", kind)
		}

		if benign(kind) && refuses(kind) {
			t.Errorf("%v is both benign and refused", kind)
		}

		// A kind answers for its reads or for its placements, never both: the
		// two gates ask different questions and a kind subject to both would
		// have to satisfy a rule nobody wrote down.
		if watched(kind) && placing(kind) {
			t.Errorf("%v is both watched and placing", kind)
		}
	}
}
