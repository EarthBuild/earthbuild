package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A mount specification may arrive in an environment variable.
//
// `RUN --mount=$EARTHLY_RUST_CARGO_HOME_CACHE` is how EarthBuild's own Rust
// library passes cache mounts around: a function sets the whole specification
// as an ENV and every RUN references it by name. This engine substitutes
// declared *arguments* into what it consumes and leaves environment variables
// for the shell - which is right for a RUN's command and wrong for its flags,
// because nothing downstream will expand a flag:
//
//	RUN --mount type=(none) is not supported by the native engine
//
// It blocks every Rust example in the corpus, through a remote library (E66).
func TestAMountSpecificationCanComeFromTheEnvironment(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ENV CARGO_CACHE=type=cache,target=/root/.cargo
    RUN --mount=$CARGO_CACHE cargo build
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var mounts []ir.Mount

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && strings.Contains(strings.Join(n.Op.Args, " "), "cargo build") {
			mounts = n.Op.Mounts
		}
	}

	if len(mounts) != 1 {
		t.Fatalf("the step has %d mounts, want 1", len(mounts))
	}

	if mounts[0].Target != "/root/.cargo" {
		t.Errorf("the mount is at %q, and the environment said /root/.cargo", mounts[0].Target)
	}
}

// An argument still works, and beats the environment for the same name.
//
// ARG and ENV are different scopes and an argument is the nearer one: this is
// the rule everywhere else in the interpreter, and a flag is not the place to
// invent a second one.
func TestAMountSpecificationPrefersAnArgument(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ENV SPEC=type=cache,target=/from-env
    ARG SPEC=type=cache,target=/from-arg
    RUN --mount=$SPEC cargo build
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec || len(n.Op.Mounts) == 0 {
			continue
		}

		if n.Op.Mounts[0].Target != "/from-arg" {
			t.Errorf("the mount is at %q, and the argument said /from-arg", n.Op.Mounts[0].Target)
		}
	}
}

// The command keeps its own variables, which belong to the shell.
//
// The distinction this is about: a flag has no later reader, so the engine must
// expand it; a command does, so the engine must not. Expanding both would break
// `for i in 1 2 3; do echo $i; done`, which is the failure `expandWord` exists
// to prevent (E65).
func TestTheCommandKeepsItsOwnVariables(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ENV CARGO_CACHE=type=cache,target=/root/.cargo
    RUN --mount=$CARGO_CACHE for i in 1 2 3; do echo $i; done
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	text := describe(p.Graph.Nodes())

	if !strings.Contains(text, "echo $i") {
		t.Errorf("the shell's own variable was expanded away:\n%s", text)
	}
}

// The longest name wins, and it wins every time.
//
// `expandWith` replaced each name in turn by walking a *map*, which has two
// defects and Go's random iteration order hides both behind each other. `$FOO`
// substituted before `$FOOBAR` leaves `<foo>BAR` - braces would have protected
// it, and the bare form is the one people write - and which happens depends on
// the run - so the same Earthfile planned two ways, which is what
// `TestPlanningIsDeterministic` intermittently caught (E66).
//
// The engine already had a correct expander: `expandWord` scans left to right
// and reads the whole name at each `$`, braces included. The helper should not
// have existed.
func TestALongerNameIsNotEatenByAShorterOne(t *testing.T) {
	t.Parallel()

	// Ten builds of the same source: with the map version this failed about
	// half of them, which is exactly the shape of a bug that survives review.
	for range 10 {
		p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ENV DIR=short
    ENV DIRECTORY=long
    RUN --mount=type=cache,target=/$DIRECTORY cargo build
`, testMain)
		if err != nil {
			t.Fatal(err)
		}

		for _, n := range p.Graph.Nodes() {
			if n.Op.Kind != ir.OpExec || len(n.Op.Mounts) == 0 {
				continue
			}

			if got := n.Op.Mounts[0].Target; got != "/long" {
				t.Fatalf("the mount is at %q: a shorter name ate a longer one", got)
			}
		}
	}
}
