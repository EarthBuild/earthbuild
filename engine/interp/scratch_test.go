package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `FROM scratch` is the empty base, not an image to fetch.
//
// Three corpus targets failed with
//
//	fetch the manifest for scratch: .../library/scratch/manifests/latest
//	returned 404 Not Found
//
// which is the engine asking a registry for a name no registry has. `scratch` is
// the reserved name for *no base at all* - it is where a build starts when it
// brings its own filesystem, and every engine that reads a Dockerfile or an
// Earthfile knows it (E468).
//
// A 404 is the worst way to fail here: it reads as a network problem, or a
// deleted image, and sends the reader to the registry rather than to the line
// they wrote.
func TestFromScratchIsTheEmptyBase(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM scratch\n    COPY x /x\n",
		testMain, interp.WithContext(ctxWith(t, map[string]string{"x": "hello"})))
	if err != nil {
		t.Fatalf("FROM scratch was refused: %v", err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpImage {
			t.Errorf("the plan fetches an image %v; scratch names none", n.Op.Args)
		}
	}
}

// And a step on it runs with nothing beneath it.
//
// The distinction that matters: an empty base is not "no base", which is what a
// target with no FROM at all has and which this engine refuses by name. A build
// that says `FROM scratch` has said where it starts.
func TestAStepOnScratchHasABase(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM scratch\n    COPY x /x\n",
		testMain, interp.WithContext(ctxWith(t, map[string]string{"x": "hello"})))
	if err != nil {
		t.Fatal(err)
	}

	if p.Graph.Root == nil {
		t.Fatal("no plan at all")
	}

	var copies int

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile {
			copies++
		}
	}

	if copies == 0 {
		t.Error("the copy onto the empty base is not in the plan")
	}
}

// An image actually called scratch elsewhere is still an image.
//
// The reserved name is bare `scratch`, which is how every reader of these files
// has it: `docker.io/library/scratch` and `myregistry/scratch` are ordinary
// references, and treating them as the empty base would be inventing a rule
// nobody else has.
func TestAQualifiedScratchIsStillAnImage(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM registry.example/scratch:1\n    RUN make\n", testMain)
	if err != nil {
		t.Fatal(err)
	}

	var fetched bool

	for _, n := range p.Graph.Nodes() {
		fetched = fetched || n.Op.Kind == ir.OpImage
	}

	if !fetched {
		t.Error("a qualified reference that ends in scratch was read as the empty base")
	}
}

// The empty base has no working directory, and a relative path needs one.
//
// `tests/file-copying.earth` sets `WORKDIR base` in its base recipe and then:
//
//	setup-scratch:
//	    FROM scratch
//	    COPY +setup/* ./
//
//	test-dot-scratch:
//	    # Note: This is a negative test (should fail).
//	    FROM +setup-scratch
//	    SAVE ARTIFACT . AS LOCAL out-dot-scratch/
//
// `test-dot`, the same save from an ordinary image, succeeds - so the difference
// is `scratch`. A `FROM` starts from the named image's configuration, and
// scratch's is **empty**: no working directory, no environment, no user. This
// engine kept `/base` from the base recipe, so `.` resolved to something and the
// negative test passed (E471).
//
// Scratch is the one image whose configuration is known without fetching it,
// which is what makes this a rule the interpreter can apply. `FROM alpine` after
// a WORKDIR has the same question and a different answer, and answering it needs
// the image's config at planning time - recorded, not done here.
func TestTheEmptyBaseHasNoWorkingDirectory(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nFROM alpine:3.22\nWORKDIR base\n\n"+
		"main:\n    FROM scratch\n    RUN make > out\n    SAVE ARTIFACT . AS LOCAL o/\n",
		testMain)
	if err == nil {
		t.Fatal("a relative path resolved against a base that has no working" +
			" directory")
	}

	for _, want := range []string{"working directory", "scratch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refused with %q, which does not mention %q", err, want)
		}
	}
}

// A WORKDIR after it says where, and then relative paths work.
//
// The remedy the refusal names, asserted so that the rule is a redirection
// rather than a dead end: a build on `scratch` says where it is and carries on.
func TestAWorkdirAfterScratchSaysWhere(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nFROM alpine:3.22\nWORKDIR base\n\n"+
		"main:\n    FROM scratch\n    WORKDIR /w\n    RUN make > out\n"+
		"    SAVE ARTIFACT . AS LOCAL o/\n", testMain)
	if err != nil {
		t.Fatalf("a WORKDIR after scratch did not settle it: %v", err)
	}
}

// An absolute path needs no working directory at all.
func TestAnAbsolutePathOnScratchIsFine(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nFROM alpine:3.22\nWORKDIR base\n\n"+
		"main:\n    FROM scratch\n    SAVE ARTIFACT /etc AS LOCAL o/\n", testMain)
	if err != nil {
		t.Fatalf("an absolute path on scratch was refused: %v", err)
	}
}
