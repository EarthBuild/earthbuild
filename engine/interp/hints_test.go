package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `SAVE IMAGE --cache-from` is accepted and ignored, because it is a hint.
//
// It names a registry to look for cache in. Green paper I5: a hint may not
// change results, so a build that heeds it and a build that ignores it produce
// the same image - which is exactly what makes ignoring it safe, and what
// separates it from a flag like `COPY --platform` that changes *what* is
// copied. Refusing a flag that cannot affect the output turns a working
// Earthfile away for nothing.
func TestACacheHintIsAcceptedAndIgnored(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN build-it
    SAVE IMAGE --cache-from=registry.example/cache:main app:latest
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Images) != 1 || p.Images[0].Ref != testImageRef {
		t.Fatalf("the image was not declared: %+v", p.Images)
	}
}

// A hint is not part of the key.
//
// Two builds differing only in where they were told to look for cache must
// share cache entries, which they cannot do if the hint reaches the key. That
// is the same requirement as I5 read from the other end.
func TestACacheHintDoesNotChangeTheGraph(t *testing.T) {
	t.Parallel()

	mk := func(src string) []ir.NodeID {
		p, err := interp.Build(versioned+src, testMain)
		if err != nil {
			t.Fatal(err)
		}

		var ids []ir.NodeID
		for _, n := range p.Graph.Nodes() {
			ids = append(ids, n.ID())
		}

		return ids
	}

	with := mk(`
main:
    FROM alpine:3.22
    RUN build-it
    SAVE IMAGE --cache-from=registry.example/cache:main app:latest
`)
	without := mk(`
main:
    FROM alpine:3.22
    RUN build-it
    SAVE IMAGE app:latest
`)

	if len(with) != len(without) {
		t.Fatalf("the graphs differ in size: %d and %d", len(with), len(without))
	}

	for i := range with {
		if with[i] != without[i] {
			t.Errorf("node %d differs: a cache hint reached the key", i)
		}
	}
}

// `COPY --if-exists src dst` copies the source when it is there, and is not an
// error when it is not.
func TestCopyIfExistsSkipsWhatIsMissing(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testPresentFile: "here\n"})

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    COPY --if-exists present.txt absent.txt /dst/
    RUN after
`, testMain, interp.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}

	var copied []string

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpLocal {
			copied = append(copied, n.Op.Args[0])
		}
	}

	if strings.Join(copied, ",") != testPresentFile {
		t.Errorf("copied %v, want only the file that is there", copied)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "after") {
		t.Errorf("the build did not continue past the missing file:\n%s", got)
	}
}

// Without --if-exists a missing source is still an error.
func TestCopyWithoutIfExistsStillRefusesAMissingFile(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testPresentFile: "here\n"})

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    COPY absent.txt /dst/\n", testMain, interp.WithContext(ctx))
	if err == nil {
		t.Fatal("a missing source was accepted without --if-exists")
	}
}

// `FROM --platform=linux/amd64 alpine` names alpine, on that platform.
//
// The flags were parsed and then the *unparsed* first argument was used as the
// image, so the reference became `--platform=linux/amd64` - an image name no
// registry has - and the platform was dropped on the way. Found by the sweep
// for engine syntax surviving into a value, in the commonest command there is.
func TestFromPlatformNamesTheImageAndThePlatform(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM --platform=linux/amd64 alpine:3.22\n    RUN build\n", testMain)
	if err != nil {
		t.Fatal(err)
	}

	var found bool

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpImage {
			continue
		}

		found = true

		if got := n.Op.Args[0]; got != testBaseImage {
			t.Errorf("the image is %q, want alpine:3.22", got)
		}

		if n.Platform.OS != testOS || n.Platform.Arch != testArch {
			t.Errorf("the image is pulled for %+v, want linux/amd64: the platform was dropped", n.Platform)
		}
	}

	if !found {
		t.Fatal("no image node in the graph")
	}
}

// The platform reaches the steps that stand on that image, not just the pull.
func TestFromPlatformAppliesToLaterSteps(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM --platform=linux/amd64 alpine:3.22\n    RUN build\n", testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		if n.Platform.Arch != testArch {
			t.Errorf("the step runs on %+v, want linux/amd64", n.Platform)
		}
	}
}
