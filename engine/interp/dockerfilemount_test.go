package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A Dockerfile's cache mount is the Earthfile's cache mount.
//
// Both spellings mean the same thing and this engine already provides one of
// them, so translating the Dockerfile form into the Earthfile form and letting
// the same parser decide is the whole implementation. It also means the two
// syntaxes cannot drift: a mount kind accepted for one is accepted for the
// other, and refused for both with the same words.
//
// Refusing every mounted RUN was the previous behaviour and it was too broad -
// it is the single construct blocking the largest group of corpus targets.
func TestADockerfileCacheMountIsACacheMount(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22
RUN --mount=type=cache,target=/root/.cache make the-thing
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("a cache mount this engine provides was refused: %v", err)
	}

	if text := describe(p.Graph.Nodes()); !strings.Contains(text, "make the-thing") {
		t.Errorf("the mounted step is not in the graph:\n%s", text)
	}
}

// The default Dockerfile mount type is `bind`, and a bind of the context works.
//
// Written both ways because a Dockerfile means `bind` when it says nothing -
// so `--mount=target=/src` and `--mount=type=bind,target=/src` are one
// instruction with two spellings, and an engine that built only the explicit
// one would refuse the form people actually write.
func TestADockerfileBindOfTheContextIsBuiltEitherWayItIsWritten(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ name, line string }{
		{"explicit", "RUN --mount=type=bind,source=.,target=/src make it"},
		{"by default", "RUN --mount=source=.,target=/src make it"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22\n"+c.line+"\n")

			_, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
			if err != nil {
				t.Fatalf("a view of the build context was refused: %v", err)
			}
		})
	}
}

// A view of an earlier stage is still refused, and says it is unbuilt.
//
// A stage's filesystem is an assembled stack of layers rather than one layer,
// so showing it needs machinery a view of the context does not (§3.3d, ν ∈ 𝕂).
// Refused rather than stripped: a step that quietly loses a mount does not
// fail, it produces the wrong thing.
func TestADockerfileBindFromAStageSaysItIsUnbuilt(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22 AS other\n"+
		"FROM alpine:3.22\n"+
		"RUN --mount=from=other,source=/x,target=/x make it\n")

	_, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err == nil {
		t.Fatal("a view of another stage was accepted")
	}

	if !strings.Contains(err.Error(), "from") {
		t.Errorf("the refusal does not name what was not honoured: %v", err)
	}
}
