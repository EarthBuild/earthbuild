package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `SHELL` changes what a shell-form RUN is run by.
//
// A Dockerfile says `SHELL ["/bin/bash", "-c"]` when its steps use bash and the
// base image's `/bin/sh` is not it - buildkit's own Dockerfile does exactly
// that, and every RUN after that line means bash.
//
// It needs no new construct here. A shell-form RUN under a custom SHELL *is* an
// exec-form RUN with the shell in front of it, which this engine already runs,
// already keys, and already refuses to re-split.
func TestADockerfileShellChangesWhatARunIsRunBy(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22
SHELL ["/bin/bash", "-c"]
RUN echo hi
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("SHELL was refused: %v", err)
	}

	if text := describe(p.Graph.Nodes()); !strings.Contains(text, "/bin/bash") {
		t.Errorf("the step is not run by the shell the Dockerfile chose:\n%s", text)
	}
}

// And it applies only after the line, and only within its stage.
func TestADockerfileShellAppliesFromWhereItIsSet(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22 AS a
RUN echo before
SHELL ["/bin/bash", "-c"]
RUN echo after
FROM alpine:3.22 AS b
RUN echo elsewhere
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE --target b .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	if text := describe(p.Graph.Nodes()); strings.Contains(text, "/bin/bash") {
		t.Errorf("a later stage inherited another stage's SHELL:\n%s", text)
	}
}

// An exec-form RUN is untouched: the author already said what to run it with.
func TestADockerfileShellDoesNotTouchAnExecFormRun(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22
SHELL ["/bin/bash", "-c"]
RUN ["/bin/true"]
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	if text := describe(p.Graph.Nodes()); strings.Contains(text, "/bin/bash") {
		t.Errorf("an exec-form RUN was wrapped in a shell:\n%s", text)
	}
}
