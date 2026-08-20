package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A loaded image carries the configuration its target declared.
//
// `WITH DOCKER --load app:latest=+docker` writes the target's layers into an
// OCI layout and loads that into the daemon. The layers were all it wrote: no
// entrypoint, no command, no environment. `docker run app` then answers
// `Error response from daemon: no command specified`, which names neither the
// image nor the line that built it, and arrives inside a WITH DOCKER block two
// targets from the ENTRYPOINT that was dropped.
//
// Found in `examples/tutorial/java/part6` and `js/part6` (E29), and only after
// the corpus report stopped truncating diagnoses to their first line.
func TestALoadedImageKeepsItsEntrypoint(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
docker:
    FROM alpine:3.22
    WORKDIR /app
    ENTRYPOINT ["/app/bin/thing"]
    ENV MODE=live
    SAVE IMAGE thing:latest

integration:
    FROM alpine:3.22
    WITH DOCKER --load thing:latest=+docker
        RUN docker run thing:latest
    END
`, "integration")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpPackImage {
			continue
		}

		if n.Op.Image == nil {
			t.Fatal("the packed image carries no configuration, so docker has no command to run")
		}

		if got := n.Op.Image.Entrypoint; len(got) != 1 || got[0] != "/app/bin/thing" {
			t.Errorf("the packed image's entrypoint is %q", got)
		}

		if n.Op.Image.WorkingDir != testWorkdir {
			t.Errorf("the packed image's working directory is %q", n.Op.Image.WorkingDir)
		}

		return
	}

	t.Error("no packed image in the plan")
}

// Two loads of the same layers under different entrypoints are different
// images, and their keys say so.
//
// The configuration decides what the image *does*, so a cache that could not
// tell them apart would load one where the other was asked for - and the
// symptom would be a container running the wrong command, which is the worst
// shape of wrong this engine has.
func TestALoadedImagesConfigurationReachesItsKey(t *testing.T) {
	t.Parallel()

	build := func(entrypoint string) ir.NodeID {
		t.Helper()

		p, err := interp.Build(versioned+`
docker:
    FROM alpine:3.22
    ENTRYPOINT ["`+entrypoint+`"]
    SAVE IMAGE thing:latest

integration:
    FROM alpine:3.22
    WITH DOCKER --load thing:latest=+docker
        RUN docker run thing:latest
    END
`, "integration")
		if err != nil {
			t.Fatal(err)
		}

		for _, n := range p.Graph.Nodes() {
			if n.Op.Kind == ir.OpPackImage {
				return n.ID()
			}
		}

		t.Fatal("no packed image in the plan")

		return ir.NodeID{}
	}

	if build("/bin/one") == build("/bin/two") {
		t.Error("two images with different entrypoints share a key")
	}
}

// The compose project is named `default`, because Earthfiles can see the name.
//
// Compose prefixes every network it creates with the project name, so a compose
// file declaring `java/part6_default` produces a network called
// `<project>_java/part6_default` - and the Earthfile beside it writes
// `docker run --network=default_java/part6_default`, by hand, in a RUN. The
// project name is not an internal detail; it is part of what an Earthfile is
// written against.
//
// It used to be a hash of the compose files, which is better isolation and
// breaks every Earthfile that names a network: the container came up on
// `earthbuild-9f86d081_java/part6_default` and the RUN two lines later asked
// for a network that did not exist. Three of the three tutorials that name a
// network expect `default`, which is what the shipping engine produces.
//
// The isolation that is lost is narrower than it looks: `up` and `down` agree
// because both compute the same name, and a daemon is per sandbox rather than
// per machine, so two builds collide only if they share a store *and* run at
// once - which the engine does not currently do.
func TestTheComposeProjectIsNamedDefault(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
integration:
    FROM alpine:3.22
    WITH DOCKER --compose docker-compose.yml
        RUN docker run --network=default_thing_default app
    END
`, "integration")
	if err != nil {
		t.Fatal(err)
	}

	var up string

	for _, n := range p.Graph.Nodes() {
		joined := strings.Join(n.Op.Args, " ")
		if strings.Contains(joined, "compose") && strings.Contains(joined, " up") {
			up = joined
		}
	}

	if up == "" {
		t.Fatal("no compose up in the plan")
	}

	if !strings.Contains(up, "-p default") {
		t.Errorf("compose is brought up under another project name:\n%s", up)
	}
}

// A WITH DOCKER block takes away the containers it started.
//
// The sandbox VM outlives the build - that reuse is what takes a rebuild from
// 700ms to 65ms - so a container started inside a block is still running for
// the next build, and every build after it. `compose down` already handles the
// `--compose` case; a bare `docker run -d` is the commoner one in the corpus
// and was handled by nothing.
//
// The symptom is not a leak, it is a wrong answer: two `typescript-node`
// targets failed with `Bind for 0.0.0.0:8080 failed: port is already
// allocated`, where the port had been taken by a container a *different*
// target left behind. A build whose result depends on which builds ran before
// it on the same machine is the non-determinism this engine exists to remove -
// and it can make a build pass as easily as fail.
func TestADockerBlockRemovesTheContainersItStarted(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
check:
    FROM alpine:3.22
    WITH DOCKER
        RUN docker run -d -p 8080:8080 thing
    END
`, "check")
	if err != nil {
		t.Fatal(err)
	}

	var steps []string

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec {
			steps = append(steps, strings.Join(n.Op.Args, " "))
		}
	}

	joined := strings.Join(steps, "\n")

	if !strings.Contains(joined, "docker rm -f") {
		t.Errorf("the block never removes what it started:\n%s", joined)
	}

	// Recorded before the body, or "what the block started" is every container
	// on the machine - including ones another build is using.
	before, after := -1, -1

	for i, s := range steps {
		// Matched on the file the two steps share rather than on the exact
		// command: the recording step gained a sentinel line, for reasons that
		// have nothing to do with what this test is about, and an assertion
		// spelled `docker ps -aq >` went red for a fix that was correct.
		if strings.Contains(s, "earthbuild-containers-") && strings.Contains(s, "echo __none__") {
			before = i
		}

		if strings.Contains(s, "docker rm -f") {
			after = i
		}
	}

	if before < 0 {
		t.Fatalf("the block does not record what was already running:\n%s", joined)
	}

	if after < before {
		t.Errorf("the containers are removed before they are recorded:\n%s", joined)
	}
}

// `--dir` reaches an artifact copy exactly as it reaches a context one.
//
// It was cancelled here for artifacts, on the evidence of one differential case:
// `COPY --dir +build/sub /here` produced `/here/sub/b.txt` where the reference
// produces `/here/b.txt`. That reading was right about the case and wrong about
// the rule - `/here` did not exist, and the reference places nothing inside a
// destination that is not already a directory (E48).
//
// Cancelling the flag reproduced the reference for every destination that does
// not exist and broke the one this repository's own Earthfile uses,
// `COPY --dir +code/earthly /`, where the destination is the root.
//
// So the interpreter passes the flag along for both kinds and the guest, which
// is the only place the destination can be looked at, decides. This test now
// asserts that it is passed - where the destination leads is the guest's suite
// and the differential's.
func TestDirReachesAnArtifactCopy(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    WORKDIR /work
    RUN make sub
    SAVE ARTIFACT sub

probe:
    FROM alpine:3.22
    COPY --dir +build/sub /here
    COPY --dir ctx /there
`, "probe", interp.WithContext(ctxWith(t, map[string]string{"ctx/f.txt": "x"})))
	if err != nil {
		t.Fatal(err)
	}

	var fromArtifact, fromContext *ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpFile || len(n.Op.Args) != 2 {
			continue
		}

		switch n.Op.Args[1] {
		case "/here":
			fromArtifact = n
		case "/there":
			fromContext = n
		}
	}

	if fromArtifact == nil {
		t.Fatalf("no copy from the artifact:\n%s", describe(p.Graph.Nodes()))
	}

	if !fromArtifact.Op.DirCopy {
		t.Error("--dir was cancelled for an artifact copy, so the destination can no longer decide")
	}

	// The context copy keeps it, or the flag has been removed rather than
	// scoped - and `COPY --dir` on a project directory is what it is for.
	if fromContext == nil {
		t.Fatalf("no copy from the context:\n%s", describe(p.Graph.Nodes()))
	}

	if !fromContext.Op.DirCopy {
		t.Error("--dir was dropped from a build-context copy, where it decides what is copied")
	}
}

// `FROM +target` continues from that target's state, not only its filesystem.
//
// A base target that sets WORKDIR and ENV and nothing else is the commonest
// shape in the corpus - every `part5` tutorial has one - and it exists precisely
// so the targets built on it inherit that setup. Taking the layers and dropping
// the working directory and the environment gives a filesystem that looks right
// and a build that runs in the wrong place with none of its variables.
//
// Found by the differential (E32): the reference answers `green` and `/w`, this
// engine answered an empty string and `/`.
func TestFromATargetInheritsItsWorkingDirectoryAndEnvironment(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
common:
    FROM alpine:3.22
    WORKDIR /w
    ENV COLOUR=green

probe:
    FROM +common
    RUN report
`, "probe")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec || !strings.Contains(strings.Join(n.Op.Args, " "), "report") {
			continue
		}

		if n.Op.Dir != "/w" {
			t.Errorf("the step runs in %q, want /w - the directory the base target left", n.Op.Dir)
		}

		if got := n.Op.Env["COLOUR"]; got != "green" {
			t.Errorf("COLOUR is %q, want green - the environment the base target set", got)
		}

		return
	}

	t.Errorf("no step in the plan:\n%s", describe(p.Graph.Nodes()))
}

// `--keep-ts` asks for what this engine already does, so it is accepted.
//
// The reference clamps mtimes to a fixed epoch and `--keep-ts` asks it not to;
// this engine preserves them always, because I8 makes an mtime part of a
// layer's identity. Refusing the flag therefore rejected an Earthfile for
// requesting the behaviour it was going to get anyway - a gratuitous
// incompatibility, and the least defensible kind, because the build it refuses
// would have been correct.
//
// Accepted rather than implemented: there is nothing to implement while the
// default is to keep timestamps. If this engine ever clamps by default - which
// is an open question, not a settled one - the flag becomes load-bearing and
// this test is where to start.
func TestKeepTsIsAcceptedBecauseItIsWhatWeDo(t *testing.T) {
	t.Parallel()

	with, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    WORKDIR /w
    RUN make
    SAVE ARTIFACT --keep-ts f.txt

probe:
    FROM alpine:3.22
    COPY --keep-ts +build/f.txt /got.txt
`, "probe")
	if err != nil {
		t.Fatalf("--keep-ts was refused: %v", err)
	}

	// And it changes nothing, which is the claim being made by accepting it.
	without, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    WORKDIR /w
    RUN make
    SAVE ARTIFACT f.txt

probe:
    FROM alpine:3.22
    COPY +build/f.txt /got.txt
`, "probe")
	if err != nil {
		t.Fatal(err)
	}

	if with.Graph.Root.ID() != without.Graph.Root.ID() {
		t.Error("--keep-ts changed the plan, so accepting it as a no-op is a lie")
	}
}
