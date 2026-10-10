package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// SAVE ARTIFACT names something the build produces. It is not a step: it selects
// a path out of the filesystem a step already made, so it adds nothing to the
// graph and everything to what the build is *for*.
func TestSaveArtifactIsCollectedNotExecuted(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    RUN /bin/busybox true
    SAVE ARTIFACT /out AS LOCAL dist/out
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	// Three commands, two of which are steps.
	if got := len(p.Graph.Nodes()); got != 2 {
		t.Errorf("graph has %d nodes, want 2; SAVE ARTIFACT is not a step", got)
	}

	if len(p.Artifacts) != 1 {
		t.Fatalf("collected %d artifacts, want 1", len(p.Artifacts))
	}

	a := p.Artifacts[0]
	if a.Path != testOutDir {
		t.Errorf("path is %q, want /out", a.Path)
	}

	if a.LocalDest != "dist/out" {
		t.Errorf("local destination is %q, want dist/out", a.LocalDest)
	}
}

// Without AS LOCAL an artifact is still produced - other targets may reference
// it - but nothing is written to the host.
func TestArtifactWithoutAsLocalHasNoDestination(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    SAVE ARTIFACT /out\n", "build")
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Artifacts) != 1 {
		t.Fatalf("collected %d artifacts, want 1", len(p.Artifacts))
	}

	if d := p.Artifacts[0].LocalDest; d != "" {
		t.Errorf("local destination is %q, want empty", d)
	}
}

// An artifact must come from a filesystem that exists.
func TestSaveArtifactBeforeFromIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+"\nbuild:\n    SAVE ARTIFACT /out\n", "build")
	if err == nil {
		t.Fatal("SAVE ARTIFACT with no base image was accepted")
	}

	if !strings.Contains(err.Error(), "FROM") {
		t.Errorf("refusal does not mention FROM:\n%s", err)
	}
}

// An artifact is attributed to the step whose filesystem it is taken from, so a
// later failure can say which command produced the missing file.
func TestArtifactRemembersItsStep(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine
    RUN /bin/busybox true
    SAVE ARTIFACT /out
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	if p.Artifacts[0].From == nil {
		t.Fatal("artifact is not attributed to a step")
	}

	// VERSION is line 1, the blank line 2, `build:` line 3, FROM line 4, RUN 5.
	if src := p.Artifacts[0].From.Meta.Source; !strings.Contains(src, ":5") {
		t.Errorf("artifact comes from %q, want the RUN at line 5", src)
	}
}
