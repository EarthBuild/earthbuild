package exec

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step that asked for raw output has its lines reported as raw.
//
// The prefix is decided where the line is written and not where it is read, so
// the sink has to carry the request forward: `sinkFor` holds the node and the
// display holds the format, and nothing else sees both.
//
// Capture is unaffected on purpose. `$( )` substitution takes a step's output
// as a value, and a value does not have a prefix to drop (E937).
func TestARawOutputStepIsReportedAsRaw(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		meta ir.Meta
		want bool
	}{{
		name: "an ordinary step is prefixed",
		meta: ir.Meta{Source: "Earthfile:1"},
		want: false,
	}, {
		name: "a raw-output step is not",
		meta: ir.Meta{Source: "Earthfile:1", RawOutput: true},
		want: true,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got []bool

			e := &Executor{Progress: func(_, _ string, raw bool) {
				got = append(got, raw)
			}}

			n := &ir.Node{Op: ir.Op{Kind: ir.OpExec}, Meta: tc.meta}

			write, done := e.sinkFor(n)
			write("hello\n", false)
			done()

			if len(got) != 1 {
				t.Fatalf("the sink reported %d lines, and one was written", len(got))
			}

			if got[0] != tc.want {
				t.Errorf("the line was reported raw=%v, want %v", got[0], tc.want)
			}
		})
	}
}
