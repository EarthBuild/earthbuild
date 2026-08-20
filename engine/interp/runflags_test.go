package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A flag on RUN is a flag, not part of the command.
//
// `RUN --no-cache fetch` was being handed to the shell whole, so the step ran
// `sh -c "--no-cache fetch"` - a command nobody wrote, which fails with a
// message about `--no-cache` not being a program. A hundred and eleven RUN
// lines in this repository carry a flag.
func TestRunFlagsAreNotPartOfTheCommand(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --no-cache fetch-latest\n", testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		if joined := strings.Join(n.Op.Args, " "); strings.Contains(joined, "--no-cache") {
			t.Errorf("the flag reached the command line: %q", joined)
		}
	}
}

// `RUN --no-cache` means the step is not cached.
//
// The author is saying this step is not a function of its inputs - it fetches
// something, or reads the clock. Ignoring that would serve a stale result from
// the cache and report success, which is the one failure this engine exists to
// prevent, so the flag has to be honoured rather than merely stripped.
func TestNoCacheMarksTheStepUncacheable(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN ordinary-step
    RUN --no-cache fetch-latest
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var checked int

	for _, n := range p.Graph.Nodes() {
		switch {
		case strings.Contains(n.Meta.Description, "fetch-latest"):
			checked++

			if !n.Op.NoCache {
				t.Error("a --no-cache step is marked cacheable")
			}
		case strings.Contains(n.Meta.Description, "ordinary-step"):
			checked++

			if n.Op.NoCache {
				t.Error("an ordinary step was marked uncacheable")
			}
		}
	}

	if checked != 2 {
		t.Fatalf("checked %d steps, want 2", checked)
	}
}

// --no-cache is part of the key, because it changes what the step means.
//
// Not a hint: the same command with and without it are different requests, and
// keying them alike would let one serve the other's result.
func TestNoCacheChangesTheKey(t *testing.T) {
	t.Parallel()

	mk := func(src string) ir.NodeID {
		p, err := interp.Build(versioned+"\nmain:\n    FROM alpine:3.22\n    RUN "+src+"\n", testMain)
		if err != nil {
			t.Fatal(err)
		}

		return p.Graph.Root.ID()
	}

	if mk("--no-cache fetch") == mk("fetch") {
		t.Error("--no-cache does not reach the identity, so a cached run could serve it")
	}
}

// A flag that changes what a step can do is refused, not stripped.
func TestSemanticRunFlagsAreRefused(t *testing.T) {
	t.Parallel()

	// --mount was here and is now implemented for `type=cache`; the types this
	// engine cannot provide are refused by parseMount instead, which names the
	// type rather than the flag. A flag that is honoured is not a flag that is
	// ignored.
	//
	// `--ssh` left this list the same way: the invoking user's agent is mounted
	// into a step that asks for one (E466), and a step that asks for an agent
	// nobody is running is refused by the *executor*, which is where the answer
	// is known.
	for _, flag := range []string{testPrivilegedFlag, "--secret=TOKEN"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+
				"\nmain:\n    FROM alpine:3.22\n    RUN "+flag+" do-it\n", testMain)
			if err == nil {
				t.Fatalf("RUN %s was accepted and its flag ignored", flag)
			}

			name, _, _ := strings.Cut(flag, "=")
			if !strings.Contains(err.Error(), name) {
				t.Errorf("the refusal does not name %s:\n%s", name, err)
			}
		})
	}
}
