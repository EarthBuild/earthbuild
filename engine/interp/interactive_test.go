package interp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `RUN --interactive` is accepted when there is somebody to talk to.
//
// A prompt needs a terminal, and a terminal is a descriptor the caller supplies
// - which puts this in the family E151 built: `ErrNotProvided`, the same as a
// secret nobody passed or a probe with nowhere to run. The Earthfile is valid
// and the invocation is incomplete.
//
// It is *not* a gap, and not a decision: with a terminal it runs. The engine
// gained the capability in E189-E193 and this is where the language reaches it.
func TestAnInteractiveRunNeedsATerminal(t *testing.T) {
	t.Parallel()

	const src = "\nmain:\n    FROM alpine:3.22\n    RUN --interactive sh\n"

	t.Run("with one", func(t *testing.T) {
		t.Parallel()

		p, err := interp.Build(versioned+src, testMain, interp.WithTerminal(true))
		if err != nil {
			t.Fatalf("a terminal was offered and the step was still refused: %v", err)
		}

		var found int

		for _, n := range p.Graph.Nodes() {
			if !strings.Contains(n.Meta.Description, "interactive") {
				continue
			}

			found++

			if !n.Op.Interactive {
				t.Error("the step is not marked interactive, so no terminal would reach it")
			}

			// What a person typed is not a function of the inputs, so the result
			// is not a function of them either. The same reasoning `--no-cache`
			// rests on, and stronger: there is no argument for reusing a session.
			if !n.Op.NoCache {
				t.Error("an interactive step is cacheable, so a later build would" +
					" serve what somebody typed once as though it were derived")
			}
		}

		if found != 1 {
			t.Errorf("found %d interactive steps, want 1", found)
		}
	})

	t.Run("without one", func(t *testing.T) {
		t.Parallel()

		_, err := interp.Build(versioned+src, testMain)
		if err == nil {
			t.Fatal("an interactive step was planned with no terminal to run it on")
		}

		if !errors.Is(err, interp.ErrNotProvided) {
			t.Errorf("a terminal nobody supplied is not in the withheld family,"+
				" so the corpus counts it as an invalid Earthfile:\n%s", err)
		}

		for _, want := range []string{"terminal", "--interactive"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not mention %q:\n%s", want, err)
			}
		}
	})
}
