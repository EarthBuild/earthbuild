package core

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A docker step that will not be cached says which kind it is, and how to get a
// cacheable one.
//
// This is the channel E392 could not find. "Which daemon did this block get" is
// not a warning and not a failure - but for every block that reaches this
// function it is *the reason the step is uncacheable*, which is a question the
// build already answers per step, with a source location.
//
// The old message - "a docker daemon, whose contents no key describes" - was
// true of every WITH DOCKER block when none of them could be cached. Now that
// `--isolate` can be, it names a category the author cannot act on: two of the
// three cases have different remedies and one of them is not reachable from
// here at all.
func TestADockerStepSaysWhyItIsNotCached(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		op   ir.Op
		says []string
	}{
		{
			// It may have been handed an outer step's daemon, and `--isolate`
			// is the answer.
			name: "shared",
			op:   ir.Op{Kind: ir.OpExec, Docker: true},
			says: []string{"--isolate"},
		},
		{
			// It asked for storage that outlives the step, so there is nothing
			// to suggest: this is what the author asked for.
			name: "a named cache",
			op:   ir.Op{Kind: ir.OpExec, Docker: true, DockerCache: "layers"},
			says: []string{"layers"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := whyUncacheable(&ir.Node{Op: tc.op})

			for _, want := range tc.says {
				if !strings.Contains(got, want) {
					t.Errorf("the reason does not mention %q: %s", want, got)
				}
			}
		})
	}

	// And the one that *is* cached never reaches here, so a suggestion to
	// isolate an already-isolated block is impossible rather than merely
	// unlikely.
	iso := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Docker: true, IsolateDocker: true}}
	if strings.Contains(whyUncacheable(iso), "--isolate") {
		t.Error("an isolated block would be told to isolate itself")
	}
}
