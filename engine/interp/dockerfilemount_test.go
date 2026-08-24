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

// A mount this engine does not provide is refused, and named.
//
// The default Dockerfile mount type is `bind`, which gives a step a window onto
// something whose contents decide the result. That is a decision this engine
// has taken and not a gap it has - so the refusal has to survive translation
// rather than be stripped, and it has to say which kind it was.
func TestADockerfileBindMountIsRefusedByKind(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ name, line string }{
		{"explicit", "RUN --mount=type=bind,target=/src make it"},
		{"by default", "RUN --mount=target=/src make it"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22\n"+c.line+"\n")

			_, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
			if err == nil {
				t.Fatal("a bind mount was accepted")
			}

			if !strings.Contains(err.Error(), "bind") {
				t.Errorf("the refusal does not name the kind: %v", err)
			}
		})
	}
}
