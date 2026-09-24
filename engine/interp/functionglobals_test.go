package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A function sees the globals of the file it is written in, and no others.
//
// The corpus states both halves and in two different files.
//
// `tests/function-nested-global.earth` is the same-file half: a function reads
// `$foo` *before* declaring it and asserts the value in force, with a comment
// saying so - "foo has not yet been declared in the function, therefore we
// reference the globally declared arg". So a global does travel into a function
// in its own file, carrying whatever overrode it.
//
// `tests/pass-args-via-function-with-override/sub.earth` is the other half: it
// declares no globals, and its function asserts `test -z "$MY_ARG"` while the
// *root* file declares `ARG --global MY_ARG=this-should-be-ignored`. The name
// is the assertion.
//
// This engine wrote every one of the caller's globals into the function's
// arguments, so a function in another file saw a value that file never
// mentions - and `--pass-args` then forwarded it over the caller's declared one,
// two targets further down (E956).
func TestAFunctionSeesItsOwnFilesGlobals(t *testing.T) {
	t.Parallel()

	dir := ctxWith(t, map[string]string{
		// No globals at all, which is what makes it the interesting file.
		"sub/Earthfile": versioned + `
FUNC2:
  FUNCTION
  RUN echo "sub sees [$MY_ARG]"
`,
	})

	p, err := interp.Build(versioned+`
ARG --global MY_ARG=from-the-root-file

test:
  FROM alpine:3.22
  DO +FUNC1

FUNC1:
  FUNCTION
  RUN echo "root sees [$MY_ARG]"
  DO ./sub+FUNC2
`, "test", interp.WithContext(dir))
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	var steps []string

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && len(n.Op.Args) == 3 {
			steps = append(steps, n.Op.Args[2])
		}
	}

	var sawRoot, sawSub bool

	for _, s := range steps {
		switch {
		case strings.Contains(s, "root sees"):
			sawRoot = true

			// The same file: the global travels, which is the half
			// `function-nested-global.earth` asserts.
			if !strings.Contains(s, "[from-the-root-file]") {
				t.Errorf("a function in the declaring file does not see its global: %q", s)
			}
		case strings.Contains(s, "sub sees"):
			sawSub = true

			// Left for the shell, which is what this engine does with a name
			// it has no value for - and in the step's environment there is
			// none, so `test -z "$MY_ARG"` holds. What must *not* happen is
			// the caller's file's value being substituted here.
			if !strings.Contains(s, "[$MY_ARG]") {
				t.Errorf("a function in a file that declares no globals saw one: %q", s)
			}
		}
	}

	if !sawRoot || !sawSub {
		t.Fatalf("the plan has %d steps and the recipe has two: %q", len(steps), steps)
	}
}
