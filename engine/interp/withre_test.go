package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// WITH RE marks every step of its block, and nothing after END.
//
// **The service is a property of what the block wraps.** A step inside it can
// have its actions executed and cached by this engine; one after END cannot,
// and marking it would promise a socket that is not there.
func TestWithREMarksItsBlockAndNoMore(t *testing.T) {
	t.Parallel()

	g, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    RUN echo before
    WITH RE
        RUN echo inside
    END
    RUN echo after
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}

	for _, n := range g.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		for _, want := range []string{"before", "inside", "after"} {
			if strings.Contains(strings.Join(n.Op.Args, " "), "echo "+want) {
				got[want] = n.Op.Actions
			}
		}
	}

	if len(got) != 3 {
		t.Fatalf("found %v, and the target has three RUNs", got)
	}

	if !got["inside"] {
		t.Error("the step inside the block was not given an action service")
	}

	if got["before"] || got["after"] {
		t.Errorf("a step outside the block was marked: before=%v after=%v",
			got["before"], got["after"])
	}
}

// The service reaches the key, so a step with one is not served a step without.
//
// In the key for `Op.Docker`'s reason and it is the same reason: a step that
// can have its actions executed by this engine, and the same line without one,
// are different requests.
func TestWithREChangesTheKey(t *testing.T) {
	t.Parallel()

	const bare = `
VERSION 0.8
build:
    FROM alpine
    RUN echo hi
`

	const wrapped = `
VERSION 0.8
build:
    FROM alpine
    WITH RE
        RUN echo hi
    END
`

	if keyOfBuild(t, bare) == keyOfBuild(t, wrapped) {
		t.Error("a step inside a WITH RE keys the same as one outside, so a" +
			" build would be served a result produced without the service")
	}
}

// WITH RE takes no options, and says so rather than discarding them.
func TestWithRETakesNoOptions(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    WITH RE --cache-id=x
        RUN echo hi
    END
`, "build")
	if err == nil {
		t.Fatal("an option WITH RE does not take was accepted and ignored")
	}

	if !strings.Contains(err.Error(), "--cache-id=x") {
		t.Errorf("the refusal does not name what it refused: %v", err)
	}
}

// keyOfBuild is the chain key of the one exec step a source builds.
func keyOfBuild(t *testing.T, src string) ir.NodeID {
	t.Helper()

	g, err := interp.Build(src, "build")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range g.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec {
			return n.ID()
		}
	}

	t.Fatal("the source built no exec step")

	return ir.NodeID{}
}
