package interp_test

import (
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

	if !has(sources, "/test/in/sub/1") {
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

func has(in []string, want string) bool {
	for _, s := range in {
		if s == want {
			return true
		}
	}

	return false
}

// TestSeveralSourcesMakeTheDestinationADirectory.
//
// `COPY --dir a b dest` places each source *inside* dest, whether or not dest
// exists: there is nowhere else for two things to go. The destination was
// resolved once and handed to every source unchanged, so with dest absent the
// first source became dest and only the second landed beside it -
// `tests/copy.earth+copy-art-multi-no-exist` got `copied/file` and `copied/2`
// where it wanted `copied/1` and `copied/2`.
//
// Decided from the sources as *written*, not as expanded: a wildcard is one
// written source, and a rule that changed meaning according to how many files
// happened to match would make the build depend on the directory's contents.
func TestSeveralSourcesMakeTheDestinationADirectory(t *testing.T) {
	t.Parallel()

	src := `VERSION 0.8

FROM alpine:3.22
WORKDIR /test

artifact:
    RUN mkdir -p in/sub/1 in/sub/2
    SAVE ARTIFACT in

main:
    COPY --dir +artifact/in/sub/1 +artifact/in/sub/2 copied
    RUN echo done
`

	p, err := interp.Build(src, "main")
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpFile || len(n.Op.Args) < 2 {
			continue
		}

		if !strings.HasSuffix(n.Op.Args[1], "/") {
			t.Errorf("a source lands at %q; with two sources the destination is"+
				" a directory each one goes inside", n.Op.Args[1])
		}
	}
}
