package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// **Each stage of a Dockerfile is its own scope for ARG**, so the same name may
// be declared in every one of them - and in a multi-platform Dockerfile it
// generally is.
//
// This refused such a file. `ARG TARGETPLATFORM is declared twice in this
// recipe` is an Earthfile rule (E438), where a second ARG for a name the recipe
// already declared genuinely does nothing; a Dockerfile's stages are separate
// scopes and the rule does not reach across them. The stage builder copies the
// interpreter's state with `sub := *rs`, which copies a map *header*: every
// stage shared one `declared`, so the second stage saw the first stage's
// declaration.
//
// Found by `+all`, whose `+all-buildkitd` does `FROM
// github.com/EarthBuild/buildkit:<sha>+build`, whose Earthfile does `FROM
// DOCKERFILE`, whose Dockerfile declares `ARG TARGETPLATFORM` eight times -
// once per stage, as it must (E584).
func TestEachDockerfileStageDeclaresItsOwnArgs(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22 AS first
ARG TARGETPLATFORM
RUN one $TARGETPLATFORM

FROM first AS second
ARG TARGETPLATFORM
RUN two $TARGETPLATFORM
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE --target second .
    RUN after
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("a Dockerfile declaring one name in two stages was refused: %v", err)
	}

	if text := describe(p.Graph.Nodes()); !strings.Contains(text, "RUN two") {
		t.Errorf("the selected stage did not run:\n%s", text)
	}
}

// The Earthfile rule itself is untouched: within one recipe a second ARG for a
// name already declared still does nothing and is still refused.
func TestARecipeStillRefusesADoubledArg(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG SAME=1
    ARG SAME=2
    RUN go
`, testMain)
	if err == nil {
		t.Fatal("a recipe declaring one name twice was accepted")
	}

	if !strings.Contains(err.Error(), "declared twice") {
		t.Errorf("refused for the wrong reason: %v", err)
	}
}
