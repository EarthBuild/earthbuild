package decl_test

import (
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/decl"
)

func env(kv ...string) []string { return kv }

// Declarations apply in order, later winning.
func TestLaterWins(t *testing.T) {
	t.Parallel()

	got := decl.Fold(nil,
		decl.Declaration{Env: env("A=1", "B=1")},
		decl.Declaration{Env: env("A=2")})

	want := env("A=2", "B=1")
	if !slices.Equal(got, want) {
		t.Errorf("folded to %v, want %v", got, want)
	}
}

// **A name with no value removes it.**
//
// A tar can only add entries, so a layer expresses deletion with a whiteout
// marker; a declaration can only set, so it expresses removal the same way. The
// marker is a name with no `=`, which no environment entry can be - POSIX
// forbids `=` in a name, so the two cannot be confused (§3.2a).
func TestANameWithNoValueRemovesIt(t *testing.T) {
	t.Parallel()

	got := decl.Fold(env("GOFLAGS=-mod=vendor", "KEEP=1"),
		decl.Declaration{Env: env("GOFLAGS")})

	if slices.Contains(got, "GOFLAGS=-mod=vendor") {
		t.Errorf("the variable survived removal: %v", got)
	}

	for _, e := range got {
		if e == "GOFLAGS" {
			t.Errorf("removal left a malformed entry behind: %v", got)
		}
	}

	if !slices.Contains(got, "KEEP=1") {
		t.Errorf("removal took something else with it: %v", got)
	}
}

// **Empty is not absent.**
//
// `os.LookupEnv` tells them apart, and so does anything that enumerates - a step
// scanning for `CARGO_*` sees a name set to nothing, which is not the same as a
// name that is not there. A model with only "set" cannot say the second.
func TestEmptyIsNotAbsent(t *testing.T) {
	t.Parallel()

	set := decl.Fold(env("A=1"), decl.Declaration{Env: env("A=")})
	if !slices.Contains(set, "A=") {
		t.Errorf("setting a variable to empty removed it: %v", set)
	}

	gone := decl.Fold(env("A=1"), decl.Declaration{Env: env("A")})
	if slices.Contains(gone, "A=") {
		t.Errorf("removing a variable set it to empty: %v", gone)
	}
}

// Removal and setting interleave, which is why they share one sequence.
//
// A separate list of removals could not say "set, then remove, then set again":
// the order between the two lists would be lost, and it is the whole of what a
// fold means.
func TestRemovalAndSettingInterleave(t *testing.T) {
	t.Parallel()

	got := decl.Fold(nil, decl.Declaration{Env: env("A=1", "A", "A=2")})

	if !slices.Contains(got, "A=2") {
		t.Errorf("the last word did not win: %v", got)
	}
}

// Removing what was never there is not an error.
func TestRemovingWhatIsNotThereIsQuiet(t *testing.T) {
	t.Parallel()

	got := decl.Fold(env("A=1"), decl.Declaration{Env: env("NOPE")})

	if !slices.Equal(got, env("A=1")) {
		t.Errorf("folded to %v, want the environment unchanged", got)
	}
}

// A value is expanded against everything set before it, and against nothing set
// after.
func TestAValueSeesWhatCameBefore(t *testing.T) {
	t.Parallel()

	got := decl.Fold(env("PATH=/bin"),
		decl.Declaration{Env: env("PATH=/opt/bin:$PATH")},
		decl.Declaration{Env: env("SEEN=$PATH")})

	if !slices.Contains(got, "PATH=/opt/bin:/bin") {
		t.Errorf("a value did not see the one before it: %v", got)
	}

	if !slices.Contains(got, "SEEN=/opt/bin:/bin") {
		t.Errorf("a later declaration did not see an earlier one: %v", got)
	}
}

// A removed name expands to nothing afterwards, exactly as an unset one does.
func TestARemovedNameExpandsToNothing(t *testing.T) {
	t.Parallel()

	got := decl.Fold(env("A=1"), decl.Declaration{Env: env("A", "B=[$A]")})

	if !slices.Contains(got, "B=[]") {
		t.Errorf("a removed name still expanded: %v", got)
	}
}

// A value that is already expanded survives the fold unchanged.
//
// An image's ENV was resolved when the image was built, so `JAVA_OPTS=-Dx=$HOME`
// in a config means those characters and not "whatever $HOME is here". A
// declaration stores text *before* expansion (3.10), so importing an already
// expanded value has to say so - otherwise the fold expands it a second time and
// an image that shipped a literal dollar gets something else.
func TestAnAlreadyExpandedValueSurvives(t *testing.T) {
	t.Parallel()

	got := decl.Fold(env("HOME=/root"),
		decl.Literal(env("JAVA_OPTS=-Dx=$HOME", "LITERAL=$$")))

	if !slices.Contains(got, "JAVA_OPTS=-Dx=$HOME") {
		t.Errorf("an imported value was expanded again: %v", got)
	}

	if !slices.Contains(got, "LITERAL=$$") {
		t.Errorf("an imported dollar pair was not preserved: %v", got)
	}
}

// Literal keeps removals as removals.
//
// The escaping is about values, and a removal has none: escaping a bare name
// would turn "remove this" into "set this to nothing", which is the distinction
// the whole edge case is about.
func TestLiteralKeepsRemovals(t *testing.T) {
	t.Parallel()

	got := decl.Fold(env("A=1"), decl.Literal(env("A")))

	if slices.Contains(got, "A=") || slices.Contains(got, "A=1") {
		t.Errorf("an imported removal did not remove: %v", got)
	}
}

// Whole declarations compose, not only their environments.
//
// An image declares a working directory, a user and an entrypoint as well, and a
// step needs the composition of everything its stack said - so the composition
// is one operation over whole declarations rather than one rule per field
// invented at each call site.
func TestDeclarationsCompose(t *testing.T) {
	t.Parallel()

	got := decl.Compose(
		decl.Declaration{Env: env("A=1"), WorkingDir: "/base", User: rootUser, Cmd: env("/bin/sh")},
		decl.Declaration{Env: env("B=2"), WorkingDir: "/later"},
	)

	if got.WorkingDir != "/later" {
		t.Errorf("working directory %q, want the later one", got.WorkingDir)
	}

	// Unset by the later one is not "set to nothing": a declaration that says
	// nothing about the user leaves the user alone, exactly as a Dockerfile that
	// omits USER inherits it.
	if got.User != rootUser {
		t.Errorf("user %q, want the earlier one to survive a declaration that is silent", got.User)
	}

	if !slices.Equal(got.Cmd, env("/bin/sh")) {
		t.Errorf("cmd %v, want the earlier one to survive", got.Cmd)
	}

	folded := decl.Fold(nil, got)
	if !slices.Contains(folded, "A=1") || !slices.Contains(folded, "B=2") {
		t.Errorf("composed environment %v, want both", folded)
	}
}

// Composing nothing is nothing.
func TestComposingNothingIsEmpty(t *testing.T) {
	t.Parallel()

	if decl.ID(decl.Compose()) != decl.ID(decl.Declaration{}) {
		t.Error("composing no declarations produced something")
	}
}
