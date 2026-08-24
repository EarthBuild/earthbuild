package interp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// withDockerfile makes a build context holding a Dockerfile and returns it.
func withDockerfile(t *testing.T, name, body string) string {
	t.Helper()

	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

// `FROM DOCKERFILE .` builds a Dockerfile as this target's base.
//
// Translated into the commands this interpreter already runs rather than handed
// to another builder: a Dockerfile's FROM, RUN, COPY, ENV and WORKDIR mean what
// the Earthfile spellings of them mean, so they can be the same steps - with
// the same keys, the same layers and the same cache.
func TestADockerfileBecomesSteps(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22
ENV GREETING=hello
WORKDIR /app
RUN make the-thing
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
    RUN after
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	text := describe(p.Graph.Nodes())
	for _, want := range []string{testBaseImage, "make the-thing", "after"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is not in the graph:\n%s", want, text)
		}
	}
}

// The Dockerfile's steps come before the Earthfile's, because it is the base.
func TestTheEarthfileContinuesFromTheDockerfile(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22\nRUN build-the-base\n")

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
    RUN after
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description == testAfter {
			if !reaches(n, "RUN build-the-base") {
				t.Errorf("the target does not stand on the Dockerfile:\n%s",
					describe(p.Graph.Nodes()))
			}

			return
		}
	}

	t.Errorf("the target's own step is missing:\n%s", describe(p.Graph.Nodes()))
}

// `-f` names a Dockerfile that is not called Dockerfile.
func TestAnAlternativeDockerfileIsRead(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "other.Dockerfile", "FROM alpine:3.22\nRUN from-the-other-file\n")

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE -f other.Dockerfile .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(describe(p.Graph.Nodes()), "from-the-other-file") {
		t.Errorf("the named Dockerfile was not the one read:\n%s", describe(p.Graph.Nodes()))
	}
}

// A Dockerfile that is not there says so, naming what it looked for.
func TestAMissingDockerfileIsNamed(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(t.TempDir()))
	if err == nil {
		t.Fatal("a Dockerfile that does not exist was built")
	}

	if !strings.Contains(err.Error(), "Dockerfile") {
		t.Errorf("the refusal does not name the file:\n%s", err)
	}
}

// An instruction this engine cannot do is refused by name, as everything else
// is - never accepted and ignored.
func TestAnUnsupportedInstructionIsRefusedByName(t *testing.T) {
	t.Parallel()

	for _, instr := range []string{
		"HEALTHCHECK CMD true",
		"ONBUILD RUN true",
		"ADD https://example.test/x /x",
	} {
		t.Run(strings.Fields(instr)[0], func(t *testing.T) {
			t.Parallel()

			dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22\n"+instr+"\n")

			_, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
			if err == nil {
				t.Fatalf("%s was accepted and ignored", instr)
			}

			if !strings.Contains(err.Error(), strings.Fields(instr)[0]) {
				t.Errorf("the refusal does not name the instruction:\n%s", err)
			}
		})
	}
}

// The Dockerfile's own content decides the key: editing it is a different
// build, which is the whole reason for translating rather than delegating.
func TestEditingTheDockerfileChangesTheBuild(t *testing.T) {
	t.Parallel()

	key := func(body string) ir.NodeID {
		t.Helper()

		p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(withDockerfile(t, "Dockerfile", body)))
		if err != nil {
			t.Fatal(err)
		}

		return p.Graph.Root.ID()
	}

	if key("FROM alpine:3.22\nRUN one\n") == key("FROM alpine:3.22\nRUN two\n") {
		t.Error("two different Dockerfiles produced one key")
	}
}

// `--target` selects a stage; without it the last stage is the one built.
//
// Both are Docker's own rule. A multi-stage Dockerfile with no target means the
// last stage, and refusing the whole file because it has more than one stage
// refused a great many Dockerfiles for a property that does not affect the
// answer.
func TestTheSelectedStageIsBuilt(t *testing.T) {
	t.Parallel()

	const df = `FROM alpine:3.22 AS builder
RUN build-in-builder

FROM alpine:3.22 AS runtime
RUN build-in-runtime
`

	for _, tc := range []struct{ name, opts, want, absent string }{
		{"no target: the last stage", "", "build-in-runtime", "build-in-builder"},
		{"--target names one", "--target builder", "build-in-builder", "build-in-runtime"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE `+tc.opts+` .
`, testMain, interp.WithContext(withDockerfile(t, "Dockerfile", df)))
			if err != nil {
				t.Fatal(err)
			}

			text := describe(p.Graph.Nodes())
			if !strings.Contains(text, tc.want) {
				t.Errorf("%q is not in the graph:\n%s", tc.want, text)
			}

			if strings.Contains(text, tc.absent) {
				t.Errorf("%q is in the graph, so the wrong stage was built:\n%s", tc.absent, text)
			}
		})
	}
}

// A target that is not in the file says so, listing what is.
func TestAMissingStageIsNamed(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22 AS builder\nRUN x\n")

	_, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE --target nope .
`, testMain, interp.WithContext(dir))
	if err == nil {
		t.Fatal("a stage that does not exist was built")
	}

	for _, want := range []string{"nope", "builder"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

// `--build-arg` supplies a value for the Dockerfile's own ARG.
func TestABuildArgReachesTheDockerfile(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22
ARG VERSION=default
RUN build-$VERSION
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE --build-arg VERSION=1.2.3 .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	text := describe(p.Graph.Nodes())
	if !strings.Contains(text, "build-1.2.3") {
		t.Errorf("the argument did not reach the Dockerfile:\n%s", text)
	}

	if strings.Contains(text, "build-default") {
		t.Errorf("the default won over the supplied value:\n%s", text)
	}
}

// And the default stands when nothing is supplied.
func TestADockerfileArgKeepsItsDefault(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22\nARG VERSION=default\nRUN build-$VERSION\n")

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(describe(p.Graph.Nodes()), "build-default") {
		t.Errorf("the default was not used:\n%s", describe(p.Graph.Nodes()))
	}
}

// A stage may build on another stage.
//
// `FROM builder` inside a Dockerfile names a stage, not an image. Refusing it
// was right while nothing built the stages; resolving it as an image reference
// would have pulled a stranger's `builder` from a registry.
func TestAStageMayBuildOnAnother(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22 AS builder
RUN compile-the-thing

FROM builder
RUN package-the-thing
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description != "RUN package-the-thing" {
			continue
		}

		if !reaches(n, "RUN compile-the-thing") {
			t.Errorf("the second stage does not stand on the first:\n%s", describe(p.Graph.Nodes()))
		}

		return
	}

	t.Errorf("the selected stage is not in the graph:\n%s", describe(p.Graph.Nodes()))
}

// `COPY --from=<stage>` takes files out of another stage.
//
// The point of a multi-stage build: compile in one, carry the result into a
// small one. The stage is read and never stacked - a source, not an input -
// which is the same distinction `COPY +target/artifact` already rests on.
func TestCopyFromAnotherStage(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22 AS builder
RUN compile-the-thing

FROM alpine:3.22
COPY --from=builder /out/app /usr/local/bin/app
RUN check-the-app
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	var copied *ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile && len(n.Op.Args) == 2 && n.Op.Args[0] == "/out/app" {
			copied = n
		}
	}

	if copied == nil {
		t.Fatalf("nothing copies out of the stage:\n%s", describe(p.Graph.Nodes()))
	}

	if len(copied.Sources) == 0 {
		t.Fatal("the stage is not a source of the copy, so its files come from nowhere")
	}

	if !reaches(copied.Sources[0], "RUN compile-the-thing") {
		t.Error("the copy reads from something other than the stage it names")
	}

	// Read, not stood on: the builder's filesystem must not become the base.
	for _, in := range copied.Inputs {
		if reaches(in, "RUN compile-the-thing") {
			t.Error("the builder stage was stacked into the image it was copied from")
		}
	}
}

// A stage nobody needs is not built.
//
// Docker builds only what the selected stage depends on, and so does this:
// building the others would run work the Earthfile never asked for, and on a
// file with a `test` stage that is exactly the work someone excluded on purpose.
func TestAnUnusedStageIsNotBuilt(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22 AS builder
RUN compile-the-thing

FROM alpine:3.22 AS slow-tests
RUN run-the-slow-tests

FROM builder
RUN package-the-thing
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(describe(p.Graph.Nodes()), "run-the-slow-tests") {
		t.Errorf("a stage nothing depends on was built:\n%s", describe(p.Graph.Nodes()))
	}
}

// A stage naming itself, or a loop between stages, is refused rather than
// followed.
func TestAStageLoopIsRefused(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM b AS a
RUN x

FROM a AS b
RUN y
`)

	_, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err == nil {
		t.Fatal("a loop between stages was followed")
	}
}

// A target's output can be the Dockerfile's build context.
//
// `FROM DOCKERFILE -f ./Dockerfile +context/*` means: build that target, and
// let the Dockerfile's COPY read from what it produced. It is how a Dockerfile
// is fed something this build made rather than something on disk, and it is the
// most-written form of the construct in the corpus.
func TestATargetCanBeTheDockerfileContext(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22\nCOPY a.txt /a.txt\nRUN cat /a.txt\n")

	p, err := interp.Build(versioned+`
context:
    FROM alpine:3.22
    RUN make-the-file > a.txt
    SAVE ARTIFACT a.txt

main:
    FROM DOCKERFILE -f Dockerfile +context/*
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	var copied *ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile && len(n.Op.Args) == 2 && n.Op.Args[0] == testFileA {
			copied = n
		}
	}

	if copied == nil {
		t.Fatalf("the Dockerfile's COPY is not in the graph:\n%s", describe(p.Graph.Nodes()))
	}

	if len(copied.Sources) == 0 {
		t.Fatal("the copy reads from nowhere, so the context was ignored")
	}

	if !reaches(copied.Sources[0], "RUN make-the-file > a.txt") {
		t.Errorf("the copy does not read from the target named as the context:\n%s",
			describe(p.Graph.Nodes()))
	}
}

// The Dockerfile itself still comes from this machine.
//
// `-f` names a file beside the Earthfile: the context is what the *build* reads,
// and the Dockerfile is what says how to read it. Looking for it in the target's
// output would need the target built before anything could be parsed.
func TestTheDockerfileIsReadFromTheEarthfilesDirectory(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Build.Dockerfile", "FROM alpine:3.22\nRUN from-the-local-file\n")

	p, err := interp.Build(versioned+`
context:
    FROM alpine:3.22
    SAVE ARTIFACT /etc/hostname

main:
    FROM DOCKERFILE -f Build.Dockerfile +context/*
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(describe(p.Graph.Nodes()), "from-the-local-file") {
		t.Errorf("the Dockerfile beside the Earthfile was not read:\n%s", describe(p.Graph.Nodes()))
	}
}

// `--allow-privileged` is accepted and grants nothing.
//
// The flag permits a referenced target to use `RUN --privileged`. This engine
// refuses that construct wherever it appears, so the permission has nothing to
// act on - and the only way accepting it can be wrong is by refusing a build
// the shipping engine would run, which is the safe direction. Refusing the flag
// itself refused builds over a permission nobody could have used.
func TestAllowPrivilegedGrantsNothing(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22\nRUN build\n")

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE --allow-privileged -f Dockerfile .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(describe(p.Graph.Nodes()), "RUN build") {
		t.Errorf("the Dockerfile was not built:\n%s", describe(p.Graph.Nodes()))
	}
}

// And a privileged step inside it is still refused, which is what makes
// accepting the flag safe.
func TestAllowPrivilegedDoesNotPermitPrivilegedSteps(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22\nRUN build\n")

	_, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE --allow-privileged -f Dockerfile .
    RUN --privileged ip link add dummy0 type dummy
`, testMain, interp.WithContext(dir))
	if err == nil {
		t.Fatal("a privileged step ran because a flag said it was allowed")
	}

	if !strings.Contains(err.Error(), testPrivilegedFlag) {
		t.Errorf("the refusal does not name the construct:\n%s", err)
	}
}

// A Dockerfile's VOLUME, EXPOSE and LABEL are translated, not refused.
//
// All three are ordinary in a Dockerfile and all three already have Earthfile
// handlers here - they set the image configuration and produce no step. The
// translation knew about eight instructions and refused the rest, so a
// `FROM DOCKERFILE` over a perfectly normal Dockerfile failed with
// `VOLUME is not supported by the native engine`, naming a construct this
// engine supports.
//
// Found by pointing the build corpus at `tests/`, whose Earthfiles reach the
// repository root, which builds buildkitd from a remote Earthfile, whose
// Dockerfile declares a volume. Four hops from anything anyone was looking at.
func TestADockerfilesImageConfigurationIsTranslated(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22
VOLUME /data
EXPOSE 8080
LABEL org.example.role=probe
RUN make the-thing
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
    SAVE IMAGE probe:latest
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("a Dockerfile with VOLUME, EXPOSE and LABEL was refused: %v", err)
	}

	if len(p.Images) != 1 {
		t.Fatalf("want one image, got %d", len(p.Images))
	}

	cfg := p.Images[0].Config

	if len(cfg.Volumes) != 1 || cfg.Volumes[0] != "/data" {
		t.Errorf("the image declares volumes %v, want [/data]", cfg.Volumes)
	}

	if len(cfg.Exposed) != 1 || cfg.Exposed[0] != "8080/tcp" {
		t.Errorf("the image exposes %v, want [8080]", cfg.Exposed)
	}

	if cfg.Labels["org.example.role"] != "probe" {
		t.Errorf("the image's labels are %v, want org.example.role=probe", cfg.Labels)
	}
}

// A Dockerfile's ADD of an ordinary file is a COPY, and nothing else is.
//
// ADD is the instruction real Dockerfiles reach for most often after RUN and
// COPY, and refusing it stops a Dockerfile that is otherwise entirely ordinary.
// It is *not* a synonym for COPY, though, and treating it as one is the kind of
// wrong this engine exists to avoid: ADD extracts a local tar archive into the
// destination, and fetches a URL. A build given the archive where it expected
// its contents does not fail - it succeeds, differently.
//
// So the plain case is translated and the two that are not COPY are refused by
// name, each saying which behaviour is missing. Refusing on the *look* of the
// source is conservative in the right direction: docker decides by reading the
// file, and a file called `.tar.gz` that is not one is refused where it would
// have worked, which is the survivable half of being wrong.
func TestADockerfileAddIsTranslatedOnlyWhenItIsACopy(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22
ADD thing.txt /thing.txt
`)

	err := os.WriteFile(filepath.Join(dir, "thing.txt"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("ADD of an ordinary file was refused: %v", err)
	}

	var copies int

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile {
			copies++
		}
	}

	if copies == 0 {
		t.Errorf("ADD produced no copy:\n%s", describe(p.Graph.Nodes()))
	}
}

// The two cases ADD does not share with COPY are refused, each by name.
func TestADockerfileAddIsRefusedWhenItIsNotACopy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		line string
		says string
	}{
		{"a local archive", "ADD bundle.tar.gz /out", "extract"},
		{"a URL", "ADD https://example.test/f.txt /f.txt", "fetch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22\n"+tc.line+"\n")

			_, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
			if err == nil {
				t.Fatal("ADD was treated as a COPY, which silently produces a different filesystem")
			}

			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("the refusal does not say what ADD would have done (%q):\n%v", tc.says, err)
			}
		})
	}
}

// A Dockerfile RUN that carries mounts is refused rather than run without them.
//
// **This is the buildkit Dockerfile, and it is not a corner.** Its buildkitd
// stage reads
//
//	RUN --mount=target=. --mount=target=/go/pkg/mod,type=cache \
//	    --mount=source=/tmp/.ldflags,target=/tmp/.ldflags,from=buildkit-version \
//	    xx-go build -ldflags "$(cat /tmp/.ldflags)" ...
//
// and the translation kept the command while dropping every mount. What a
// caller then saw was `cat: can't open '/tmp/.ldflags'` and `go: go.mod file
// not found in current directory` - two confusing errors from inside somebody
// else's Dockerfile, neither of them naming the thing that was missing.
//
// The rule is already written twice in this repository and applies here
// unchanged: a RUN flag that changes what the step can *do* is "refused rather
// than stripped, because a step that quietly loses its secret does not fail, it
// produces the wrong thing" (runflags.go), and `translate`'s own note says an
// instruction silently dropped "produces an image that is not what the
// Dockerfile describes, and nothing downstream can tell". Its mounts are the
// same instruction one level down.
//
// **`type=cache` is no longer among them**, because this engine provides it and
// always did on the Earthfile side - refusing it here was the translation being
// broader than the gap. See TestADockerfileCacheMountIsACacheMount. What is
// listed below is what remains genuinely absent, and each is refused by kind.
func TestADockerfileRunWithMountsIsRefused(t *testing.T) {
	t.Parallel()

	for _, mount := range []string{
		"--mount=target=.",
		"--mount=source=/tmp/.ldflags,target=/tmp/.ldflags,from=other",
		"--mount=type=secret,id=token",
	} {
		dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22\nRUN "+mount+" true\n")

		_, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
		if err == nil {
			t.Errorf("%s was accepted, so the step runs without it", mount)

			continue
		}

		// Named, because the whole point is that the refusal says which
		// construct went unhonoured rather than leaving the step to fail
		// somewhere inside itself.
		if !strings.Contains(err.Error(), "--mount") {
			t.Errorf("%s: the refusal never mentions --mount: %v", mount, err)
		}
	}
}

// A Dockerfile RUN with no mounts is untouched.
func TestAPlainDockerfileRunStillWorks(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22\nRUN make the-thing\n")

	_, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("a RUN with no mounts was refused: %v", err)
	}
}
