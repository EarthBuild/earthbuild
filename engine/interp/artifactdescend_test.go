package interp_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestAReferenceMayDescendIntoASavedArtifact.
//
// `SAVE ARTIFACT in` names one artifact, `/in`, sitting at `/test/in`. A
// reference may name something *inside* it - `tests/copy.earth` does
//
//	COPY --dir +artifact/in/sub/1 +artifact/in/sub/2 copied
//
// and nothing resolved that: the exact-name match failed, and the reference was
// passed through as written, so the guest was asked for `/in/sub/1`, a path no
// layer has. `+artifact/in` worked, which is why the gap read as a problem with
// copying two things at once.
func TestAReferenceMayDescendIntoASavedArtifact(t *testing.T) {
	t.Parallel()

	src := `VERSION 0.8

FROM alpine:3.22
WORKDIR /test

artifact:
    RUN mkdir -p in/sub/1 in/sub/2
    SAVE ARTIFACT in

main:
    COPY --dir +artifact/in/sub/1 copied
    RUN echo done
`

	p, err := interp.Build(src, "main")
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	var sources []string

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile {
			sources = append(sources, n.Op.Args[0])
		}
	}

	if !slices.Contains(sources, "/test/in/sub/1") {
		t.Errorf("the copy reads %v"+
			"\n  `SAVE ARTIFACT in` puts /in at /test/in, so +artifact/in/sub/1"+
			" is /test/in/sub/1", sources)
	}
}

// The most specific saved artifact wins, so a target that saves both a
// directory and something inside it resolves through the inner one.
func TestTheMostSpecificSavedArtifactWins(t *testing.T) {
	t.Parallel()

	src := `VERSION 0.8

FROM alpine:3.22
WORKDIR /test

artifact:
    RUN mkdir -p in/sub/1 in/sub/2
    SAVE ARTIFACT in
    SAVE ARTIFACT in/sub /in/sub

main:
    COPY --dir +artifact/in/sub/1 copied
    RUN echo done
`

	p, err := interp.Build(src, "main")
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpFile {
			continue
		}

		if strings.HasSuffix(n.Op.Args[0], "/sub/1") && n.Op.Args[0] != "/test/in/sub/1" {
			t.Errorf("the copy reads %q, want /test/in/sub/1", n.Op.Args[0])
		}
	}
}

// TestAnExplicitArtifactNameIsCleaned.
//
// `SAVE ARTIFACT ./file.txt ./yet-another-file-with-+.txt` names the artifact
// with the second argument, and the name was kept exactly as written. Every
// lookup then compares `"/" + name`, which for a name beginning `./` is
// `/./yet-...` and equals nothing - so `+artifact-with-plus2/yet-another-file-with-\+.txt`
// found no artifact and the reference passed through to the guest as a path no
// layer has (tests/escape.earth+test-copy-artifact2).
//
// The default name goes through filepath.Base and was always clean, which is
// why only the explicit-name form was affected.
func TestAnExplicitArtifactNameIsCleaned(t *testing.T) {
	t.Parallel()

	src := `VERSION 0.8

FROM alpine:3.22
WORKDIR /test

maker:
    RUN printf test >file.txt
    SAVE ARTIFACT ./file.txt ./named.txt

main:
    COPY +maker/named.txt ./
    RUN echo done
`

	p, err := interp.Build(src, "main")
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	var sources []string

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile {
			sources = append(sources, n.Op.Args[0])
		}
	}

	if !slices.Contains(sources, "/test/file.txt") {
		t.Errorf("the copy reads %v"+
			"\n  the artifact is named ./named.txt and lives at /test/file.txt", sources)
	}
}
