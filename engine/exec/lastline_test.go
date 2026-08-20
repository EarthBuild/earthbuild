package exec

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Output that does not end in a newline still arrives.
//
// A step's output is buffered to line boundaries, so a write that splits
// mid-line does not produce half a sentence in the middle of another step's.
// Nothing flushed what was left when the step ended, so **a command whose last
// line has no newline printed nothing at all** - and `printf hello` is such a
// command (E449).
//
// It costs more than a missing line on screen. `ARG V=$(cat ./content)` takes
// its value from this stream, and `tests/build-arg.earth` writes exactly that
// over a file with no trailing newline: the argument arrived empty, and the
// failure named an assertion three lines later.
func TestOutputWithNoTrailingNewlineIsNotLost(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		chunks []string
		want   []string
	}{{
		name:   "a whole line",
		chunks: []string{"hello\n"},
		want:   []string{"hello"},
	}, {
		name:   "no trailing newline",
		chunks: []string{"hello"},
		want:   []string{"hello"},
	}, {
		name:   "split across writes",
		chunks: []string{"hel", "lo"},
		want:   []string{"hello"},
	}, {
		name:   "a line and then a fragment",
		chunks: []string{"one\ntw", "o"},
		want:   []string{"one", "two"},
	}, {
		name:   "nothing at all",
		chunks: []string{},
		want:   nil,
	}, {
		// A step that ends with a newline has nothing left over, and flushing
		// an empty remainder would print a blank line after every command.
		name:   "a trailing newline leaves nothing behind",
		chunks: []string{"one\n"},
		want:   []string{"one"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got []string

			e := &Executor{Capture: func(_ *ir.Node, line string) {
				got = append(got, line)
			}}

			n := &ir.Node{Op: ir.Op{Kind: ir.OpExec}, Meta: ir.Meta{Source: "Earthfile:1"}}

			write, done := e.sinkFor(n)
			for _, c := range tc.chunks {
				write(c)
			}

			done()

			if len(got) != len(tc.want) {
				t.Fatalf("captured %q, want %q", got, tc.want)
			}

			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("captured %q, want %q", got, tc.want)
				}
			}
		})
	}
}
