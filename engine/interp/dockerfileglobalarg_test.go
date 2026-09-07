package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// An ARG before the first FROM is global, and a stage re-declaring it gets it.
//
// This is a Dockerfile rule and not an Earthfile one: an `ARG` above the first
// `FROM` belongs to the file rather than to a stage, and a stage that wants it
// says `ARG NAME` with no value to bring it into scope. The value comes from
// the global declaration.
//
// **It is how buildkit's own Dockerfile pins runc.** `ARG RUNC_VERSION=v1.3.5`
// at the top, `ARG RUNC_VERSION` in the stage that fetches it, and a step that
// interpolates it. Read as "declare with an empty default", that step runs
//
//	git checkout -q ""
//
// which exits 128 saying nothing - and eight Native CI jobs failed on it,
// reported against an ENV in a different repository's Earthfile.
func TestAGlobalDockerfileArgReachesTheStageThatRedeclaresIt(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `ARG PINNED=v1.2.3
FROM alpine:3.22
ARG PINNED
RUN use --version="$PINNED"
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	text := describe(p.Graph.Nodes())
	if !strings.Contains(text, "v1.2.3") {
		t.Errorf("the stage's re-declared ARG is empty, so the step runs with"+
			" nothing where the version should be:\n%s", text)
	}
}

// And a stage that does not re-declare it does not see it.
//
// The other half of the same rule, and the reason it cannot be implemented by
// simply putting every global into every stage's scope.
func TestAGlobalDockerfileArgNeedsRedeclaring(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `ARG PINNED=v1.2.3
FROM alpine:3.22
RUN use --version="$PINNED"
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	if text := describe(p.Graph.Nodes()); strings.Contains(text, "v1.2.3") {
		t.Errorf("a stage that never declared the ARG was given it anyway:\n%s", text)
	}
}
