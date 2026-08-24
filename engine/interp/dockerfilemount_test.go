package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
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

// A view of an earlier stage names that stage, and reads it.
//
// §3.3d with ν ∈ 𝕂. Two things have to be true and neither is automatic: the
// stage has to be *built* - it may not have been, since stages are built on
// demand and only the ones something needs - and it has to end up among the
// step's sources, or nothing keys it and nothing orders it.
//
// This is the shape buildkit's own Dockerfile uses:
//
//	RUN --mount=source=/tmp/.ldflags,target=/tmp/.ldflags,from=buildkit-version
func TestADockerfileBindFromAStageReadsThatStage(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22 AS other\n"+
		"RUN make the-other-thing\n"+
		"FROM alpine:3.22\n"+
		"RUN --mount=from=other,source=/x,target=/x make it\n")

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("a view of another stage was refused: %v", err)
	}

	// The stage it binds was built, which is not implied by the Dockerfile
	// naming it: nothing else in this file depends on `other`.
	if text := describe(p.Graph.Nodes()); !strings.Contains(text, "make the-other-thing") {
		t.Errorf("the bound stage was never built:\n%s", text)
	}

	// **And the mount names it.** Building the stage is not enough on its own:
	// a view that never recorded which object it shows keys against nothing,
	// and the step reads a mount point no source filled. E646 is that mutant,
	// and it survived a test that checked only that the stage got built.
	var bound *ir.Node

	for _, n := range p.Graph.Nodes() {
		for _, m := range n.Op.Mounts {
			if !m.View {
				continue
			}

			if m.From == (ir.NodeID{}) {
				t.Fatalf("the view at %s shows nothing: no object was recorded", m.Target)
			}

			for _, src := range n.Sources {
				if src.ID() == m.From {
					bound = src
				}
			}
		}
	}

	if bound == nil {
		t.Fatal("no step binds a view of anything among its sources")
	}
}

// A view naming a stage that does not exist says which ones do.
func TestADockerfileBindFromAnUnknownStageNamesTheOnesThereAre(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", "FROM alpine:3.22 AS real\n"+
		"FROM alpine:3.22\n"+
		"RUN --mount=from=absent,target=/x make it\n")

	_, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err == nil {
		t.Fatal("a view of a stage that is not there was accepted")
	}

	if !strings.Contains(err.Error(), "real") {
		t.Errorf("the refusal does not say what stages exist: %v", err)
	}
}

// In an Earthfile, `from=` names nothing: there are no stages.
func TestAnEarthfileBindHasNoStagesToNameFrom(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN --mount=type=bind,from=other,target=/x true
`, testMain)
	if err == nil {
		t.Fatal("from= was accepted in an Earthfile, which has no stages")
	}

	if !strings.Contains(err.Error(), "from") {
		t.Errorf("the refusal does not name what was not honoured: %v", err)
	}
}
