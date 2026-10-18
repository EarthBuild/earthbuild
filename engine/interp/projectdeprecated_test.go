package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// PROJECT is deprecated, and a build that uses it is told so.
//
// The legacy engine says it, and `tests/Earthfile` asserts it:
// `--output_contains="the PROJECT command is deprecated"`. This engine
// validated the command and said nothing, so that assertion is one of the
// Native suite's failures (E846).
//
// Worth saying rather than only accepting: the cloud integration is gone, so
// PROJECT has no effect here unless a custom secret command reads it, and an
// author whose Earthfile still carries the line cannot learn that from a build
// which works silently.
func TestAProjectCommandSaysItIsDeprecated(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION --use-project-secrets 0.8
PROJECT some/project

probe:
    FROM alpine:3.22
    RUN true
`, "probe")
	if err != nil {
		t.Fatal(err)
	}

	var found bool

	for _, note := range p.Advice {
		if strings.Contains(note, "the PROJECT command is deprecated") {
			found = true
		}
	}

	if !found {
		t.Errorf("a build using PROJECT was not told it is deprecated: %v", p.Advice)
	}
}

// And a build that does not use it hears nothing about it.
//
// The pair matters: a note printed unconditionally would satisfy the assertion
// above while telling every build in the world about a command it does not use.
func TestABuildWithoutProjectIsNotWarnedAboutIt(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8

probe:
    FROM alpine:3.22
    RUN true
`, "probe")
	if err != nil {
		t.Fatal(err)
	}

	for _, note := range p.Advice {
		if strings.Contains(note, "PROJECT") {
			t.Errorf("a build with no PROJECT was warned about it: %q", note)
		}
	}
}
