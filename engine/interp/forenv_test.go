package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A `FOR` variable is an environment variable inside the body.
//
// `FOR` declares its name for the body exactly as `ARG` declares one for the
// recipe, so a step in the loop reads it from its environment and not only
// through substitution. The two are different wherever the *shell* is the one
// doing the reading: `tests/platform` writes a `case \$plat in` whose dollar is
// escaped precisely so the shell resolves it at run time, and with the name
// absent from the environment every arm fell through to `*) exit 1` - after the
// three lines above it had printed the right answers (E961).
//
// Substituting the unescaped occurrences and exporting nothing is the shape that
// makes this hard to see: most of the script works.
func TestAForVariableIsInTheStepEnvironment(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    FOR x IN one two
        RUN echo in-the-loop
    END
    RUN echo after-the-loop
`, "main")
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	var (
		inLoop []string
		after  map[string]string
		sawEnd bool
	)

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec || len(n.Op.Args) != 3 {
			continue
		}

		switch n.Op.Args[2] {
		case "echo in-the-loop":
			inLoop = append(inLoop, n.Op.Env["x"])
		case "echo after-the-loop":
			sawEnd, after = true, n.Op.Env
		}
	}

	if len(inLoop) != 2 {
		t.Fatalf("the loop planned %d bodies, and it has two items", len(inLoop))
	}

	// Each iteration carries its own value, which is the whole point of
	// exporting it rather than exporting the last one.
	want := map[string]bool{"one": true, "two": true}

	for _, v := range inLoop {
		if !want[v] {
			t.Errorf("a body has x=%q, want one of one, two", v)
		}

		delete(want, v)
	}

	for missing := range want {
		t.Errorf("no body carries x=%s", missing)
	}

	// Scoped to the loop, as the restore in forStatement already intends: a
	// name the loop borrowed does not outlive END.
	if !sawEnd {
		t.Fatal("the step after the loop was not planned")
	}

	if v, ok := after["x"]; ok {
		t.Errorf("the step after END still carries x=%q", v)
	}
}
