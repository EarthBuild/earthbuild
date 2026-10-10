package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `COPY +target/artifact <path>` inside a LOCALLY target puts the artifact on
// this machine.
//
// The refusal it replaces said there was no image to copy into, which was true
// and beside the point: the author did not ask for an image, they asked for a
// directory on the machine the target already runs on. Every COPY inside a
// LOCALLY target in this repository is this shape.
func TestCopyInsideLocallyExportsTheArtifact(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
producer:
    FROM alpine:3.22
    RUN make-it > /out.txt
    SAVE ARTIFACT /out.txt

main:
    LOCALLY
    COPY +producer/out.txt ./collected.txt
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var found *interp.Artifact

	for i, a := range p.Artifacts {
		if a.LocalDest == "collected.txt" || a.LocalDest == "./collected.txt" {
			found = &p.Artifacts[i]
		}
	}

	if found == nil {
		t.Fatalf("nothing exports the artifact: %+v", p.Artifacts)
	}

	if found.Path != "/out.txt" {
		t.Errorf("exports %q, want the artifact the target saved", found.Path)
	}

	if found.From == nil {
		t.Error("the export names no producing step")
	}
}

// A destination outside the project is allowed here, and the reason is worth
// stating.
//
// `SAVE ARTIFACT AS LOCAL /etc/passwd` is refused because an Earthfile - which
// may have been fetched from elsewhere - must not choose where to write on
// someone's machine. A LOCALLY target is already running arbitrary commands on
// that machine: `RUN cp x /etc/passwd` is the same act with more steps. Refusing
// the copy while allowing the command would be theatre, and the real hazard -
// remote code with a LOCALLY target - is older and larger than this line.
func TestCopyInsideLocallyMayWriteOutsideTheProject(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
producer:
    FROM alpine:3.22
    RUN make-it > /out.txt
    SAVE ARTIFACT /out.txt

main:
    LOCALLY
    COPY +producer/out.txt /tmp/somewhere-else.txt
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	// Only the exported ones: the producer's own `SAVE ARTIFACT` with no AS
	// LOCAL is an artifact another target may reference, and having no
	// destination is what that means.
	var dests []string

	for _, a := range p.Artifacts {
		if a.LocalDest != "" {
			dests = append(dests, a.LocalDest)
		}
	}

	if strings.Join(dests, ",") != "/tmp/somewhere-else.txt" {
		t.Errorf("exported to %v", dests)
	}
}

// A COPY from the build context inside a LOCALLY target is still refused.
//
// The file is already on this machine, at the path the line names, so the copy
// is from a directory to itself. Silently doing nothing would be worse than
// saying so.
func TestCopyingTheContextNamesWhatIsWrong(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testSourceFile: "x\n"})

	_, err := interp.Build(versioned+
		"\nmain:\n    LOCALLY\n    COPY src.txt ./elsewhere.txt\n", testMain, interp.WithContext(ctx))
	if err == nil {
		t.Fatal("copying the context onto itself was accepted")
	}

	if !strings.Contains(err.Error(), "LOCALLY") {
		t.Errorf("the refusal does not explain itself:\n%s", err)
	}
}
