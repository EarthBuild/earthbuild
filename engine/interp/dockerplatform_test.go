package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A Dockerfile may name a stage by the platform it is building for.
//
// Docker predefines TARGETPLATFORM, TARGETOS, TARGETARCH, TARGETVARIANT and the
// BUILD* four for every build, and - unlike an Earthfile's built-ins - they are
// available in the global scope, so a `FROM` line uses them without declaring
// anything. A file with one stage per operating system and a final
// `FROM binaries-$TARGETOS` is the ordinary way to write that.
//
// This engine passed the name through unexpanded, so it tried to pull an image:
//
//	parse image reference "binaries-$TARGETOS": invalid reference format:
//	repository name (library/binaries-$TARGETOS) must be lowercase
//
// Found in buildkit's own Dockerfile, reached from this repository's `+test-ast`
// through four Earthfiles (E63). It is E49's defect one front end over.
func TestADockerfileStageCanBeNamedByPlatform(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22 AS binaries-linux
RUN echo linux > /which.txt

FROM alpine:3.22 AS binaries-darwin
RUN echo darwin > /which.txt

FROM binaries-$TARGETOS
RUN cat /which.txt
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir), interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	text := describe(p.Graph.Nodes())

	// The linux stage was built and the darwin one was not: a stage nothing
	// selects must not run, which is the same rule that keeps a `test` stage
	// out of a production build.
	if !strings.Contains(text, "echo linux") {
		t.Errorf("the stage the platform selects was not built:\n%s", text)
	}

	if strings.Contains(text, "echo darwin") {
		t.Errorf("a stage the platform did not select was built anyway:\n%s", text)
	}
}

// `COPY --from` takes the same names.
//
// The other place a stage is named, and it resolves through the same lookup -
// so an expansion applied at one and not the other would produce a file that
// builds its stages correctly and copies out of a registry.
func TestADockerfileCopyFromCanBeNamedByPlatform(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22 AS tools-linux
RUN echo built > /tool

FROM alpine:3.22
COPY --from=tools-$TARGETOS /tool /tool
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir), interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	if text := describe(p.Graph.Nodes()); !strings.Contains(text, "echo built") {
		t.Errorf("the stage COPY --from named was not built:\n%s", text)
	}
}

// What is *not* predefined stays untouched.
//
// Docker leaves an undeclared argument empty rather than treating it as text,
// but an engine that expanded every `$name` in a stage reference would also
// eat the ones a Dockerfile means literally. Only the eight Docker defines are
// substituted here; anything else is left for the layer that knows about it.
func TestADockerfileLeavesUnknownNamesAlone(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22 AS one
RUN echo one

FROM one
RUN echo "$NOT_A_DOCKER_BUILTIN"
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir), interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	if text := describe(p.Graph.Nodes()); !strings.Contains(text, "NOT_A_DOCKER_BUILTIN") {
		t.Errorf("a name this engine does not define was substituted away:\n%s", text)
	}
}

// `COPY --from` may name an image, not only a stage.
//
// Docker allows either, and the difference is invisible in the syntax: a name
// that matches no stage is an image reference. buildkit's own Dockerfile copies
// the qemu binaries straight out of `tonistiigi/binfmt@sha256:...`, which is how
// this surfaced - the refusal named the whole digest and called it an
// unsupported stage (E64).
//
// The image is a *source*: read and never stacked, exactly as a stage would be,
// which is the same distinction `COPY +target/artifact` rests on.
func TestADockerfileCopyFromCanNameAnImage(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22
COPY --from=busybox:1.37 /bin/busybox /usr/local/bin/busybox
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir), interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	text := describe(p.Graph.Nodes())

	if !strings.Contains(text, "busybox:1.37") {
		t.Errorf("the image COPY --from named is not in the graph:\n%s", text)
	}

	if !strings.Contains(text, "/usr/local/bin/busybox") {
		t.Errorf("the copy itself is not in the graph:\n%s", text)
	}
}

// A Dockerfile's global ARGs reach its FROM lines.
//
// An `ARG` before the first stage is Docker's way of parameterising a base
// image, and pinning a tool version that way is the ordinary idiom:
//
//	ARG XX_VERSION=1.2.1
//	FROM tonistiigi/xx:${XX_VERSION}
//
// The parser hands these back as meta-arguments and this engine dropped them -
// `stages, _, err := instructions.Parse(...)` - so the reference kept its
// braces and was sent to a registry as written. buildkit's Dockerfile, again
// (E64).
func TestADockerfileGlobalArgReachesAFrom(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `ARG FLAVOUR=3.22

FROM alpine:${FLAVOUR}
RUN echo built
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir), interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	if text := describe(p.Graph.Nodes()); !strings.Contains(text, testBaseImage) {
		t.Errorf("the global ARG did not reach the FROM:\n%s", text)
	}
}

// And `--build-arg` overrides it, as it does for a stage's own ARG.
//
// The default is what the file says when nobody asks; the flag is somebody
// asking. A global ARG that ignored the flag would pin the version the author
// happened to write on the day, which is the opposite of why it is an argument.
func TestADockerfileGlobalArgTakesTheBuildArg(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `ARG FLAVOUR=3.21

FROM alpine:${FLAVOUR}
RUN echo built
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE --build-arg FLAVOUR=3.22 .
`, testMain, interp.WithContext(dir), interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	text := describe(p.Graph.Nodes())

	if !strings.Contains(text, testBaseImage) {
		t.Errorf("--build-arg did not reach the global ARG:\n%s", text)
	}

	if strings.Contains(text, "alpine:3.21") {
		t.Errorf("the default was used despite --build-arg:\n%s", text)
	}
}
