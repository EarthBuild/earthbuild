package interp_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A LOCALLY target's relative `SAVE ARTIFACT` names a path on this machine.
//
// The host step starts in the directory holding the Earthfile, so that is what
// a relative path is relative to - not "a base with no working directory",
// which is `FROM scratch` and was refused as such (E471). The refusal stopped
// `upload-sarif-github-action`'s `+user-source` (`LOCALLY`, a `RUN` that fills
// `.user-src`, then `SAVE ARTIFACT .user-src /src`), which `earth` builds.
//
// The path is read *after* the host step has run: it is that step's output, so
// a capture made while planning would see last build's files or none. That is
// also why the capture is never cached - nothing bounds what a host step wrote
// (I7), so there is no key under which yesterday's copy is today's.
func TestALocallyArtifactIsCapturedFromThisMachine(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
user-source:
    LOCALLY
    RUN mkdir -p .user-src && cp -r src/. .user-src/
    SAVE ARTIFACT .user-src /src

main:
    FROM alpine:3.22
    COPY +user-source/src /scan/
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	cap := savedFrom(t, p, "src")

	if cap.Op.Kind != ir.OpLocal {
		t.Fatalf("the artifact comes from a %s step, want a capture of the host path", cap.Op.Kind)
	}

	if len(cap.Op.Args) != 1 || cap.Op.Args[0] != ".user-src" {
		t.Errorf("captures %q, want the path the host step wrote", cap.Op.Args)
	}

	if !cap.Op.NoCache {
		t.Error("the capture is cacheable, so a later build could be served an earlier one's files")
	}

	if !standsOnHost(cap) {
		t.Error("the capture is not ordered after the host step that writes what it reads")
	}

	copiesFrom(t, p, cap)
}

// A WORKDIR after LOCALLY moves the host step, so it moves the capture with it.
func TestALocallyArtifactFollowsTheHostWorkdir(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
user-source:
    LOCALLY
    WORKDIR sub
    RUN echo x > out.txt
    SAVE ARTIFACT out.txt

main:
    FROM alpine:3.22
    COPY +user-source/out.txt /
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	cap := savedFrom(t, p, "out.txt")
	if got := cap.Op.Args; len(got) != 1 || got[0] != "sub/out.txt" {
		t.Errorf("captures %q, want the file in the directory the host step ran in", got)
	}

	copiesFrom(t, p, cap)
}

// An absolute WORKDIR after LOCALLY is a directory on this machine.
//
// `tests/local/Earthfile+save-local-artifact-with-workdir` is the reference's own
// case: `WORKDIR /tmp/<id>`, a `RUN` that writes a file there, and a relative
// `SAVE ARTIFACT` of it. A host step's Dir is joined onto the project, so this
// ran `chdir <project>/tmp/<id>` and failed before the artifact was reached.
func TestAnAbsoluteWorkdirAfterLocallyIsOnThisMachine(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
saver:
    LOCALLY
    WORKDIR /tmp/scratch-dir
    RUN echo ddd > out
    SAVE ARTIFACT out data

main:
    FROM alpine:3.22
    COPY +saver/data .
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpHost && (n.Op.Dir != "/tmp/scratch-dir" || !n.Op.HostAbs) {
			t.Errorf("the host step runs in %q (host-absolute %v), want /tmp/scratch-dir on this machine",
				n.Op.Dir, n.Op.HostAbs)
		}
	}

	cap := savedFrom(t, p, "data")
	if got := cap.Op.Args; len(got) != 1 || got[0] != "tmp/scratch-dir/out" || !cap.Op.HostAbs {
		t.Errorf("captures %q (host-absolute %v), want tmp/scratch-dir/out from this machine's root",
			got, cap.Op.HostAbs)
	}

	copiesFrom(t, p, cap)
}

// An absolute SAVE ARTIFACT in a LOCALLY target is a file on this machine.
//
// The corpus has four (`SAVE ARTIFACT /tmp/earthbuild-test-<id>`), and the
// pinned-remote tests save /etc/hostname. A LOCALLY target already runs
// arbitrary commands here, so reading a file it names is the same act.
func TestAnAbsoluteLocallyArtifactIsAFileOnThisMachine(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
saver:
    LOCALLY
    SAVE ARTIFACT /etc/hostname

main:
    FROM alpine:3.22
    COPY +saver/hostname /
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	cap := savedFrom(t, p, "hostname")
	if got := cap.Op.Args; len(got) != 1 || got[0] != "etc/hostname" || !cap.Op.HostAbs {
		t.Errorf("captures %q (host-absolute %v), want etc/hostname from this machine's root",
			got, cap.Op.HostAbs)
	}

	copiesFrom(t, p, cap)
}

// Relative to this project, so it may not leave it - the rule a context has.
func TestALocallyArtifactMayNotLeaveTheProject(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
user-source:
    LOCALLY
    SAVE ARTIFACT ../elsewhere /src
`, "user-source")
	if err == nil || !strings.Contains(err.Error(), "leaves") {
		t.Fatalf("a capture above the project was not refused: %v", err)
	}
}

// savedFrom is the step the artifact `name` is taken from. Names are kept as
// written (`/src`) and compared rooted, as the plan's own lookups do.
func savedFrom(t *testing.T, p *interp.Plan, name string) *ir.Node {
	t.Helper()

	for _, a := range p.Artifacts {
		if filepath.Join("/", a.Name) == filepath.Join("/", name) && a.From != nil {
			return a.From
		}
	}

	t.Fatalf("no artifact %q in %+v", name, p.Artifacts)

	return nil
}

// standsOnHost reports whether n runs after a host step.
func standsOnHost(n *ir.Node) bool {
	for _, in := range n.Inputs {
		if in.Op.Kind == ir.OpHost || standsOnHost(in) {
			return true
		}
	}

	return false
}

// copiesFrom checks that the consuming COPY reads the capture: its source is the
// capture or stands on it, so the captured layer is in the stack it reads.
//
// Not merely that the plan *records* the artifact as the capture's. A COPY
// reads from the target's last step, and a capture beside the chain rather than
// in it left the COPY reading the host step's empty layer.
func copiesFrom(t *testing.T, p *interp.Plan, capture *ir.Node) {
	t.Helper()

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpFile || len(n.Sources) == 0 {
			continue
		}

		if src := n.Sources[0]; src.ID() == capture.ID() || standsOn(src, capture) {
			return
		}

		t.Errorf("the COPY reads from %s (%s), which does not hold the capture",
			n.Sources[0].Op.Kind, n.Sources[0].Meta.Source)

		return
	}

	t.Error("no COPY in the plan reads from a source")
}

// standsOn reports whether n is built on want.
func standsOn(n, want *ir.Node) bool {
	for _, in := range n.Inputs {
		if in.ID() == want.ID() || standsOn(in, want) {
			return true
		}
	}

	return false
}
