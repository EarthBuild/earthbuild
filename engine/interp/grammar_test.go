package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `copy-sources = copy-source *( WSP copy-source )`: COPY takes several
// sources, and the last argument is the destination.
//
// Taking only the first silently dropped every other file - a build that
// succeeds and produces an image missing half of what the Earthfile put in it,
// which is the worst way to be wrong.
func TestCopyTakesSeveralSources(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testFileA: "a", testFileB: "b", "c.txt": "c"})

	p, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    COPY a.txt b.txt c.txt /dst/\n",
		"build", interp.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}

	var copies int

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile {
			copies++
		}
	}

	if copies != 3 {
		t.Errorf("three sources produced %d copies:\n%s", copies, describe(p.Graph.Nodes()))
	}
}

// Dropping a source must change the build, or the bug above could return
// unnoticed.
func TestEachSourceChangesTheGraph(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testFileA: "a", testFileB: "b"})

	mk := func(srcs string) ir.NodeID {
		p, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    COPY "+srcs+" /dst/\n",
			"build", interp.WithContext(ctx))
		if err != nil {
			t.Fatal(err)
		}

		return p.Graph.Root.ID()
	}

	if mk("a.txt b.txt") == mk(testFileA) {
		t.Error("copying two files and copying one produced the same build")
	}
}

// `from-args = *( from-option WSP ) target-ref *( WSP build-arg-override )`:
// FROM takes options before the reference and arguments after it.
func TestFromTakesOptionsAndArguments(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM --pass-args +other --tag=passed
    RUN main-step

other:
    FROM alpine:3.22
    ARG tag=own
    RUN echo $tag
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "echo passed") {
		t.Errorf("the argument was not passed through FROM:\n%s", got)
	}
}

// FROM has no refused option left, and that is written here rather than
// asserted with a substitute.
//
// This was `TestUnsupportedFromOptionsAreRefused`, and it held two flags in
// turn: `--platform`, until it was honoured, and `--allow-privileged`, until it
// was accepted (E476). Nothing is left for it to be about - `TestFromPlatform`
// covers the first and `TestAllowPrivilegedDoesNotMakeAStepPrivileged` the
// second, and an unknown flag is `TestAnUnknownFlagIsNamed`'s.
//
// Rewriting it around an invented flag would have kept a green test whose name
// says something the source no longer does: *a test with nothing left to assert
// asserts nothing*, and saying so is worth more than the line count.
// `TestNoFlagIsSilentlyDropped` is what watches this command's flags now, and it
// watches all of them rather than the two somebody remembered.

// `target-with-args = "(" WSP target-ref *( WSP build-arg-override ) WSP ")"`:
// the parenthesised form groups a reference with its arguments, and is how a
// COPY names an artifact from a target built with particular arguments.
func TestParenthesisedReferences(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    COPY (+other/out --tag=v2) /dst/

other:
    FROM alpine:3.22
    ARG tag=own
    RUN echo $tag
    SAVE ARTIFACT /out
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "echo v2") {
		t.Errorf("the parenthesised argument was not applied:\n%s", got)
	}
}
