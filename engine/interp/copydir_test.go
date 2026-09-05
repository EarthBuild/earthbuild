package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func copyNodeOf(t *testing.T, p *interp.Plan) *ir.Node {
	t.Helper()

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile {
			return n
		}
	}

	t.Fatal("no COPY node in the graph")

	return nil
}

// `COPY --dir src /dst` copies the directory itself; without it, its contents.
//
// The distinction is `cp -r src dst` against `cp -r src/. dst`, and getting it
// wrong puts a project's files one directory level from where every later
// command looks for them. It is the most common COPY flag in this repository by
// a factor of five.
func TestCopyDirCopiesTheDirectoryItself(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testSourcePath: testGoPackage})

	withDir, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    COPY --dir src /app\n", "build",
		interp.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}

	// Carried as a flag rather than as a trailing separator on the destination.
	// The separator was doing two jobs and cannot do both: for a file it means
	// "place this inside that directory", and for a directory the *default* is
	// the opposite - `COPY src .` contributes what is in src. Encoding --dir as
	// a separator made the plain form put the tree one level too deep.
	if !copyNodeOf(t, withDir).Op.DirCopy {
		t.Error("--dir did not reach the step, so the directory's contents would be copied instead")
	}

	// And the plain form does not ask for it.
	plain, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    COPY src /app\n", "build",
		interp.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}

	if copyNodeOf(t, plain).Op.DirCopy {
		t.Error("a copy without --dir asked for the directory itself")
	}
}

// And the two forms are different steps, because they produce different
// filesystems.
func TestCopyDirChangesTheStep(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testSourcePath: testGoPackage})

	mk := func(src string) ir.NodeID {
		p, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    COPY "+src+" /app\n", "build",
			interp.WithContext(ctx))
		if err != nil {
			t.Fatal(err)
		}

		return p.Graph.Root.ID()
	}

	if mk("--dir src") == mk(testSourceDir) {
		t.Error("--dir and plain COPY produced the same step; they copy different things")
	}
}

// Flags that change what is copied in ways the engine cannot express are still
// refused.
func TestUnknownCopyFlagsAreStillRefused(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testSourceDir: "x"})

	// --if-exists was here and is now implemented: it changes what is copied,
	// so it had to be built rather than ignored. The flags left are the ones
	// that still would be ignored if accepted.
	// --platform is implemented; see TestCopyPlatformBuildsTheTargetThere.
	// --keep-ts is no longer here either, for a different reason: it asks for
	// what this engine already does. Refusing it rejected an Earthfile for
	// requesting the behaviour it was going to get (E34).
	// **The list emptied**, and `--chmod` was the last entry. It is honoured
	// now: a mode is part of a layer and this engine already keeps modes
	// through SAVE ARTIFACT, so there was nothing here a store could fail to
	// carry - which is what made it different from `--chown`.
	//
	// So this asserts the flag arrives rather than that it is refused. This is the `--dir` file's shape.
	p, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    COPY --chmod=0755 src /dst\n",
		"build", interp.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}

	var seen bool

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpFile {
			continue
		}

		seen = true

		if n.Op.Chmod != "0755" {
			t.Errorf("the copy carries mode %q, so the step cannot set it", n.Op.Chmod)
		}
	}

	if !seen {
		t.Fatal("no copy was planned")
	}
}

// `BUILD +target --NAME=value` passes an argument to the target, exactly as DO
// does for a function.
func TestBuildPassesArgumentsToTheTarget(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    BUILD +other --tag=passed

other:
    FROM alpine:3.22
    ARG tag=own
    RUN echo $tag
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "echo passed") {
		t.Errorf("the argument was not passed to the target:\n%s", got)
	}
}

// The same target built with different arguments is built twice, because it is
// two different things.
func TestATargetBuiltWithTwoArgumentsIsTwoBuilds(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    BUILD +other --tag=one
    BUILD +other --tag=two

other:
    FROM alpine:3.22
    ARG tag=own
    RUN echo $tag
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	for _, want := range []string{"echo one", "echo two"} {
		if !strings.Contains(got, want) {
			t.Errorf("the graph is missing %q:\n%s", want, got)
		}
	}
}
