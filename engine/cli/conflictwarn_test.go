package cli

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Something outside this file calls it.
//
// A warning that renders correctly and is never printed is the shape this
// branch keeps producing: the FINALLY artefact nobody read, the SAVE IMAGE
// nobody wrote, the flatten dispatch nobody called. Each was covered by a unit
// test of the piece and by nothing at the seam, because **a seam belongs to
// nobody** - the function is right, the caller is right, and there is no caller.
//
// A source-level check rather than a behavioural one, and it is worth being
// plain about the difference: this proves the call exists, not that a build
// reaches it. The reaching is what engine/core/conflictpath_test.go establishes,
// through a real scheduler and the real on-disk cache.
func TestTheConflictWarningIsPrintedBySomething(t *testing.T) {
	t.Parallel()

	callers, err := nonTestFilesContaining(".", "conflictWarning(")
	if err != nil {
		t.Fatal(err)
	}

	delete(callers, "conflicts.go")

	if len(callers) == 0 {
		t.Error("nothing outside conflicts.go calls conflictWarning" +
			"\n  the cache refuses the rewrite either way, so the build stays correct" +
			"\n  and the determinism bug it found is never shown to anybody")
	}
}

// A build that saw a cache key claim two results says so.
//
// The cache now refuses the rewrite, which keeps I9 - state is inserted or
// removed, never modified - and refusing on its own would be the worse half of
// the fix. A key determines a result by construction, so two layers under one
// key is a step that read the same things and produced different output. Kept
// to itself, that turns a determinism bug into a cache miss nobody can account
// for: the build is simply slower every time, forever, with no line anywhere
// saying why.
//
// So the warning has to carry three things - that it happened, which key, and
// what it means - because a reader who has never heard of Κ₂ needs the third
// one to know whether to care.
func TestAConflictWarningSaysWhatItMeans(t *testing.T) {
	t.Parallel()

	k := core.Key{0x3f, 0xa2}
	got := conflictWarning([]cache.Conflict{
		{Key: k, Held: ir.NodeID{0x9c}, Given: ir.NodeID{0x44}},
	}, 1, nil)

	if got == "" {
		t.Fatal("a conflict produced no warning at all")
	}

	for _, want := range []string{
		k.String()[:12],    // which key
		"9c",               // what was held
		"44",               // what was offered
		"same inputs",      // what it means
		"not reproducible", // why the reader should care
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the warning does not mention %q:\n%s", want, got)
		}
	}
}

// No conflicts, no warning.
//
// The line that matters most: a diagnostic printed on a healthy build is
// trained away within a week, and then it is not there on the build that needed
// it.
func TestACleanBuildWarnsAboutNothing(t *testing.T) {
	t.Parallel()

	if got := conflictWarning(nil, 0, nil); got != "" {
		t.Errorf("a build with no conflicts printed:\n%s", got)
	}
}

// More conflicts than were kept says how many are missing.
//
// The recorded list is capped, and a capped list presented as a whole list is a
// build that quietly under-reports how wrong it is. Naming the remainder is the
// same rule the scheduler follows when it drops output: say what was left out.
func TestAnOverflowingConflictListSaysHowManyAreMissing(t *testing.T) {
	t.Parallel()

	recorded := make([]cache.Conflict, 0, 32)

	for i := range 32 {
		recorded = append(recorded, cache.Conflict{
			Key:   core.Key{byte(i)},
			Held:  ir.NodeID{0x01},
			Given: ir.NodeID{0x02},
		})
	}

	got := conflictWarning(recorded, 40, nil)

	if !strings.Contains(got, "8 more") {
		t.Errorf("the warning does not say 8 were not listed:\n%s", got)
	}
}

// The whole warning is one block, and every line of it is indented under the
// first.
//
// A multi-line diagnostic whose continuation lines start at column zero reads
// as several unrelated messages, which is how a warning becomes three warnings
// in a bug report and gets triaged three times.
func TestTheWarningIsOneIndentedBlock(t *testing.T) {
	t.Parallel()

	got := conflictWarning([]cache.Conflict{
		{Key: core.Key{0x01}, Held: ir.NodeID{0x02}, Given: ir.NodeID{0x03}},
	}, 1, nil)

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("the warning is a single line, so it cannot be saying much:\n%s", got)
	}

	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, "    ") {
			t.Errorf("a continuation line is not indented under the first:\n%q", l)
		}
	}
}

// A conflict names the step it happened in.
//
// **Otherwise it is unactionable where it matters most.** The warning says a
// step is not reproducible and prints a key and two layer digests - none of
// which a reader can turn into an Earthfile line. Nondeterminism is exactly the
// diagnosis that needs a place to look, and the engine already knows: the step
// record carries the chain key it was published under beside `Meta.Source`, so
// the join costs a lookup and no new field anywhere.
func TestAConflictNamesTheStep(t *testing.T) {
	t.Parallel()

	key := core.Key{7}

	rec := &core.Record{Steps: []core.StepRecord{
		{ChainKey: core.Key{1}, Meta: ir.Meta{Source: "Earthfile:11"}},
		{ChainKey: key, Meta: ir.Meta{Source: "Earthfile:42"}},
	}}

	got := conflictWarning([]cache.Conflict{
		{Key: key, Held: ir.NodeID{1}, Given: ir.NodeID{2}},
	}, 1, rec)

	if !strings.Contains(got, "Earthfile:42") {
		t.Errorf("the warning does not say which step:\n%s", got)
	}

	if strings.Contains(got, "Earthfile:11") {
		t.Errorf("the warning named a step that did not conflict:\n%s", got)
	}
}

// A conflict under a key no step was published with still reports.
//
// Κ₂ and Κₜ entries are published under keys the step record does not carry, and
// a conflict there is as real as any other. Saying less about it is right;
// saying nothing would lose it.
func TestAConflictWithNoMatchingStepStillReports(t *testing.T) {
	t.Parallel()

	got := conflictWarning([]cache.Conflict{
		{Key: core.Key{9}, Held: ir.NodeID{1}, Given: ir.NodeID{2}},
	}, 1, &core.Record{})

	if got == "" {
		t.Error("a conflict under an unrecognised key was not reported at all")
	}
}
