package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `RUN --raw-output` prints its lines without the step prefix, and says so.
//
// The prefix names which step a line came from, which matters because steps run
// concurrently - and is exactly wrong for a step whose output is meant for
// something that parses it. GitHub Actions reads `::group::` at the start of a
// line and nowhere else, so a prefixed one is not a fold marker but a sentence
// about one.
//
// Refused before this, at the VERSION line: `--raw-output is a feature this
// engine does not know` took a whole file down, and that was the entire cause
// of one Native CI job (E937).
//
// The request travels in Meta, which is not hashed. Two steps differing only in
// how their output is displayed compute the same thing and must share a cache
// entry; keying on it would make a display option rebuild the world.
func TestRawOutputIsRequestedAndNotKeyed(t *testing.T) {
	t.Parallel()

	const body = `
probe:
    FROM alpine:3.22
    RUN --raw-output echo "::group::x"
    RUN echo plain
`

	_, err := interp.Build("VERSION 0.8\n"+body, "probe")
	if err == nil {
		t.Fatal("RUN --raw-output was accepted in a file that did not ask for it")
	}

	// The remedy is the flag, so the refusal has to name it.
	if !strings.Contains(err.Error(), "--raw-output") {
		t.Errorf("the refusal does not name the flag that enables it:\n%v", err)
	}

	p, err := interp.Build("VERSION --raw-output 0.8\n"+body, "probe")
	if err != nil {
		t.Fatalf("RUN --raw-output was refused in a file that asked for it: %v", err)
	}

	var raw, plain *ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		if strings.Contains(strings.Join(n.Op.Args, " "), "::group::") {
			raw = n
		} else {
			plain = n
		}
	}

	if raw == nil || plain == nil {
		t.Fatalf("the plan has %d exec steps, and the recipe has two", len(p.Graph.Nodes()))
	}

	if !raw.Meta.RawOutput {
		t.Error("the step that asked for raw output does not carry the request")
	}

	if plain.Meta.RawOutput {
		t.Error("a step that did not ask for raw output carries the request")
	}
}
