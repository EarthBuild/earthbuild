package exec

import (
	"reflect"
	"testing"
)

// TestAStepInheritsWhatItsBaseDeclared.
//
// **A base image's `ENV` is part of what standing on it means.** `golang`
// declares `PATH=/go/bin:/usr/local/go/bin:...`; a step that does not inherit it
// cannot find `go`, and `distroless/python3` cannot find `python3`. The
// declaration is an input this step's key already covers - it comes from the
// base - so inheriting it is not the ambient state I3 forbids.
//
// The Earthfile's own `ENV` wins, because that is the later statement.
func TestAStepInheritsWhatItsBaseDeclared(t *testing.T) {
	t.Parallel()

	declared := []string{"PATH=/usr/local/go/bin:/usr/bin", "GOPATH=/go", "LANG=C.UTF-8"}

	got := stepEnv(declared, map[string]string{"GOPATH": "/work", "CGO_ENABLED": "0"})

	want := []string{
		// Declared order is kept, and an override lands where the declaration
		// put it - an image that ordered its own environment meant it.
		"PATH=/usr/local/go/bin:/usr/bin",
		"GOPATH=/work",
		"LANG=C.UTF-8",
		// What the Earthfile added, sorted: a map has no order and two runs of
		// the same build must produce the same environment.
		"CGO_ENABLED=0",
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("stepEnv gave\n  %q\nwant\n  %q", got, want)
	}
}

// And a step with no declaration behind it is exactly what it always was.
func TestAStepWithNothingDeclaredIsItsOwnEnvironment(t *testing.T) {
	t.Parallel()

	got := stepEnv(nil, map[string]string{"B": "2", "A": "1"})

	want := []string{"A=1", "B=2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stepEnv gave %q, want %q", got, want)
	}
}

// A declaration that is not `NAME=value` is passed through rather than guessed
// at: it is the image's, this engine did not write it, and dropping it silently
// loses whatever it meant.
func TestAnOddDeclarationSurvives(t *testing.T) {
	t.Parallel()

	got := stepEnv([]string{"BARE", "A=1"}, map[string]string{"A": "2"})

	want := []string{"BARE", "A=2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stepEnv gave %q, want %q", got, want)
	}
}
