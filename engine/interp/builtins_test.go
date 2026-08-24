package interp_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A declared platform argument gets the platform, not an empty string.
//
// The values are the reference engine's, taken from it rather than reasoned
// about, on a Mac building for Linux:
//
//	T linux/arm64|linux|arm64|
//	U darwin/arm64|darwin|arm64|
//	N linux/arm64|linux|arm64|
//
// Which is the distinction worth having a test for: TARGET is what is being
// built, USER is the machine that typed the command, and NATIVE is the machine
// doing the work. Two of the three coincide here, and an engine that returned
// one value for all three would pass any test that only looked at arm64.
//
// This engine returned nothing for all twelve. `+earthly` in this repository
// declares `ARG TARGETOS`, derives `ARG GOOS=$TARGETOS`, and saves its binary to
// `build/$GOOS/$GOARCH$VARIANT/earthly` - so an empty value put the compiled
// tool in a directory named `$TARGETOS`, with a dollar sign, and reported
// success (E49).
func TestADeclaredPlatformArgumentHasAValue(t *testing.T) {
	t.Parallel()

	src := versioned + `
probe:
    FROM --platform=linux/arm64 alpine:3.22
    ARG TARGETPLATFORM
    ARG TARGETOS
    ARG TARGETARCH
    ARG TARGETVARIANT
    ARG NATIVEPLATFORM
    ARG NATIVEOS
    ARG NATIVEARCH
    RUN echo "$TARGETPLATFORM $TARGETOS $TARGETARCH [$TARGETVARIANT] $NATIVEPLATFORM $NATIVEOS $NATIVEARCH"
`

	p, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	got := lastRun(t, p.Graph.Nodes())

	want := "linux/arm64 linux arm64 [] linux/arm64 linux arm64"
	if !strings.Contains(got, want) {
		t.Errorf("the platform arguments did not reach the command:\n got %s\nwant %s", got, want)
	}
}

// The invoking machine is not the machine the work runs on.
//
// USER* is the only one of the three that can differ from the others here, and
// it is the one an engine is most likely to get wrong by treating "the
// platform" as a single fact. On this machine the reference reports
// darwin/arm64 while the build itself runs on linux/arm64.
func TestTheUserPlatformIsTheInvokingMachine(t *testing.T) {
	t.Parallel()

	src := versioned + `
probe:
    FROM --platform=linux/arm64 alpine:3.22
    ARG USERPLATFORM
    ARG USEROS
    ARG USERARCH
    RUN echo "user is $USERPLATFORM / $USEROS / $USERARCH"
`

	p, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	got := lastRun(t, p.Graph.Nodes())

	// The test runs where the interpreter runs, so the expected value is this
	// machine's - written as a comparison against the engine's own answer for
	// the *user* platform rather than a hard-coded "darwin", which would fail
	// on Linux CI for a reason that is not a defect.
	if strings.Contains(got, "user is  /  / ") {
		t.Errorf("the user platform is empty: %s", got)
	}

	if strings.Contains(got, "user is linux/arm64") && interp.UserPlatform() != "linux/arm64" {
		t.Errorf("the user platform was answered with the target's: %s", got)
	}
}

// An *undeclared* platform argument is not the engine's to substitute.
//
// `$TARGETARCH` with no `ARG TARGETARCH` above it reaches the command as those
// thirteen characters, and the shell inside the image expands it - to nothing,
// because nothing set it. That is how the reference behaves too, checked
// directly against it, and the agreement is on what the build *observes*.
//
// The distinction matters because it is the same rule that keeps `$i` in
// `for i in 1 2 3; do echo $i; done` intact: the engine substitutes what an
// Earthfile declared and leaves the shell's own variables alone. Filling in a
// built-in that was never declared would break both at once - it would change
// what an Earthfile means, and it would make the engine a second, disagreeing
// shell.
func TestAnUndeclaredPlatformArgumentIsLeftToTheShell(t *testing.T) {
	t.Parallel()

	src := versioned + `
probe:
    FROM --platform=linux/arm64 alpine:3.22
    RUN echo "[$TARGETARCH][$TARGETOS]"
`

	p, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	got := lastRun(t, p.Graph.Nodes())

	if !strings.Contains(got, "[$TARGETARCH][$TARGETOS]") {
		t.Errorf("an undeclared platform argument was substituted by the engine: %s", got)
	}
}

// A declaration with a default keeps the default, because that is what the
// author asked for.
//
// `ARG TARGETARCH=amd64` in a target that means to cross-build is an
// instruction, and a built-in that overrode it would be the engine arguing with
// the Earthfile.
func TestADefaultBeatsTheBuiltInValue(t *testing.T) {
	t.Parallel()

	src := versioned + `
probe:
    FROM --platform=linux/arm64 alpine:3.22
    ARG TARGETARCH=riscv64
    RUN echo "arch is $TARGETARCH"
`

	p, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	if got := lastRun(t, p.Graph.Nodes()); !strings.Contains(got, "arch is riscv64") {
		t.Errorf("the author's default was overridden by the built-in: %s", got)
	}
}

// An argument's default may name another argument.
//
// `ARG GOOS=$TARGETOS` is the second line of every cross-building target in
// this repository, and the default was stored as those nine characters: `$GOOS`
// then expanded to the *text* `$TARGETOS`, which reached a path and made a
// directory called `$TARGETOS`. Supplying the built-ins fixed nothing, because
// nothing was reading them.
//
// The engine expands `$(...)` in a default already - a command's output is
// obviously a value to compute - and did not expand `$name`, which is the same
// question with a cheaper answer.
func TestAnArgumentDefaultCanNameAnotherArgument(t *testing.T) {
	t.Parallel()

	src := versioned + `
probe:
    FROM --platform=linux/arm64 alpine:3.22
    ARG TARGETOS
    ARG TARGETARCH
    ARG GOOS=$TARGETOS
    ARG GOARCH=$TARGETARCH
    ARG TAG=build-$GOOS-$GOARCH
    RUN echo "tag is $TAG"
`

	p, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	if got := lastRun(t, p.Graph.Nodes()); !strings.Contains(got, "tag is build-linux-arm64") {
		t.Errorf("a default naming another argument was not expanded: %s", got)
	}
}

// A default naming nothing is left alone, like every other undeclared name.
//
// `ARG MESSAGE=$HOME` is the author asking for the shell's HOME, not for the
// engine's opinion about it - the same rule that keeps `$i` intact in a RUN.
func TestADefaultNamingAnUndeclaredArgumentIsLeftAlone(t *testing.T) {
	t.Parallel()

	src := versioned + `
probe:
    FROM --platform=linux/arm64 alpine:3.22
    ARG WHERE=$HOME/somewhere
    RUN echo "at $WHERE"
`

	p, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	if got := lastRun(t, p.Graph.Nodes()); !strings.Contains(got, "at $HOME/somewhere") {
		t.Errorf("an undeclared name in a default was substituted: %s", got)
	}
}

// A name the engine consumes and nobody declared expands to nothing.
//
// The two rules are already distinguished here - a RUN's text is a shell's and
// keeps `$i` intact, everything else is the engine's - and the *unset* half of
// the engine's rule was wrong. `SAVE ARTIFACT x AS LOCAL "build/$GOARCH$VARIANT/x"`
// is this repository's own line, `VARIANT` is declared nowhere in it, and the
// reference writes `build/arm64/x`. Checked against it directly, because this
// is not something to reason out: the answer differs by context and the
// context is the point.
//
// Left literal, the destination becomes `build/arm64$VARIANT/x` - a directory
// with a dollar sign in its name, which no later step looks in and no error
// mentions.
func TestAnUndeclaredNameInADestinationBecomesNothing(t *testing.T) {
	t.Parallel()

	src := versioned + `
probe:
    FROM --platform=linux/arm64 alpine:3.22
    ARG TARGETARCH
    RUN echo shipped > /bin.txt
    SAVE ARTIFACT /bin.txt AS LOCAL dist/$TARGETARCH$VARIANT/bin.txt
`

	p, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Artifacts) != 1 {
		t.Fatalf("expected one artifact, found %d", len(p.Artifacts))
	}

	if got := p.Artifacts[0].LocalDest; got != "dist/arm64/bin.txt" {
		t.Errorf("the destination kept an undeclared name: %q", got)
	}
}

// lastRun is the command of the final exec step, which is what these cases
// assert against.
func lastRun(t *testing.T, nodes []*ir.Node) string {
	t.Helper()

	for i := range slices.Backward(nodes) {
		if nodes[i].Op.Kind == ir.OpExec {
			return strings.Join(nodes[i].Op.Args, " ")
		}
	}

	t.Fatal("no command in the plan")

	return ""
}
