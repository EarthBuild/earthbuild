package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

const artifactCopy = versioned + `
compile:
    FROM alpine:3.22
    RUN make
    SAVE ARTIFACT /out/binary

package:
    FROM alpine:3.22
    COPY +compile/binary /usr/bin/
    RUN check
`

// `COPY +target/artifact` takes a file out of another target's output.
//
// It is the companion of SAVE ARTIFACT and it is everywhere in real Earthfiles.
// Reading it as a path in the build context - which is what the interpreter did
// - produces "+compile/binary is not in the build context", a message that sends
// the reader looking for a file that was never meant to exist.
func TestCopyFromAnotherTarget(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(artifactCopy, "package")
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())

	// The producing target's steps are in the graph: the artifact has to be
	// built before it can be copied.
	if !strings.Contains(got, "RUN make") {
		t.Errorf("the producing target was not built:\n%s", got)
	}

	if !strings.Contains(got, "RUN check") {
		t.Errorf("the consuming step is missing:\n%s", got)
	}
}

// The producing target is a *source*, not a base.
//
// `COPY +compile/binary /usr/bin/` takes one file. Stacking compile's whole
// filesystem underneath package would merge an entire image in and produce
// something the Earthfile does not describe - the same defect as stacking a
// build context.
func TestAnArtifactSourceIsNotStacked(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(artifactCopy, "package")
	if err != nil {
		t.Fatal(err)
	}

	var copyNode *ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile {
			copyNode = n
		}
	}

	if copyNode == nil {
		t.Fatal("no COPY node in the graph")
	}

	if len(copyNode.Sources) != 1 {
		t.Fatalf("COPY has %d sources, want 1 (the producing target)", len(copyNode.Sources))
	}

	// And exactly one thing it stands on: the state before it.
	if len(copyNode.Inputs) != 1 {
		t.Errorf("COPY stands on %d inputs, want 1", len(copyNode.Inputs))
	}
}

// COPY takes flags, and one that changes what is copied is refused by name.
//
// `COPY --dir src dest` copies directories as directories rather than their
// contents - a different result. Reading the flag as a path produced
// "--dir is not in the build context", which is a diagnosis of the wrong thing
// entirely, forty times over in this repository.
func TestCopyFlagsAreRefusedByName(t *testing.T) {
	t.Parallel()

	// --dir is absent because it is now *honoured* rather than refused: it
	// desugars into a destination, and TestCopyDirChangesTheStep asserts it is
	// not ignored. The rest still change what is copied in ways the engine
	// cannot express.
	// --if-exists is no longer here: it is implemented, and a flag that is
	// honoured is not a flag that is ignored.
	// --platform was here and is now implemented: it builds the referenced
	// target somewhere else, as FROM and BUILD already did. What is left are the
	// flags that would still be silently dropped if accepted.
	// --keep-ts is no longer here either, for a different reason: it asks for
	// what this engine already does. Refusing it rejected an Earthfile for
	// requesting the behaviour it was going to get (E34).
	for _, flag := range []string{"--chmod=0755"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    COPY `+flag+` src /dst
`, "build", interp.WithContext(ctxWith(t, map[string]string{testSourceDir: "x"})))
			if err == nil {
				t.Fatalf("COPY %s was accepted and its flag ignored", flag)
			}

			name, _, _ := strings.Cut(flag, "=")
			if !strings.Contains(err.Error(), name) {
				t.Errorf("the refusal does not name %s:\n%s", name, err)
			}

			if strings.Contains(err.Error(), "build context") {
				t.Errorf("a flag was diagnosed as a missing file:\n%s", err)
			}
		})
	}
}

// A cross-file artifact reference is now resolved, so one naming an Earthfile
// that is not there says where it looked - and still never diagnoses it as a
// missing context file, which is what sent readers after the wrong thing.
func TestCrossFileArtifactCopyNamesTheMissingEarthfile(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    COPY ../../libs/hello+artifact/out /dst
`, "build")
	if err == nil {
		t.Fatal("a reference to an Earthfile that is not there was accepted")
	}

	if !strings.Contains(err.Error(), testEarthfile) {
		t.Errorf("the error does not say what it looked for:\n%s", err)
	}

	if strings.Contains(err.Error(), "build context") {
		t.Errorf("a cross-file reference was diagnosed as a missing file:\n%s", err)
	}
}

// A copy from a target that does not exist lists what does.
func TestCopyFromAnUnknownTargetListsAlternatives(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    COPY +compil/binary /dst

compile:
    FROM alpine
    RUN make
`, "build")
	if err == nil {
		t.Fatal("a copy from a missing target was accepted")
	}

	for _, want := range []string{"compil", "compile"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}
