package interp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestADockerfileCopyResolvesAgainstTheProducingTarget.
//
// `FROM DOCKERFILE +gen/` makes the Dockerfile's own COPY read from `+gen`'s
// output rather than from this machine. The source path went through verbatim,
// so `COPY bc.txt ./` asked the guest for `/bc.txt` - and `SAVE ARTIFACT ./*`
// in a target with `WORKDIR /test` puts it at `/test/bc.txt`, which is what an
// Earthfile's own `+gen/bc.txt` resolves to through savedAt.
//
// tests/gen-dockerfile.earth is the corpus case: it failed with
// `COPY bc.txt: nothing in that target has it`.
func TestADockerfileCopyResolvesAgainstTheProducingTarget(t *testing.T) {
	t.Parallel()

	made := t.TempDir()

	err := os.WriteFile(filepath.Join(made, "Dockerfile"),
		[]byte("FROM alpine:3.22\nCOPY bc.txt ./\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	src := `VERSION 0.8

FROM alpine:3.22
WORKDIR /test

gen:
    RUN echo hello >bc.txt
    SAVE ARTIFACT ./*

main:
    FROM DOCKERFILE +gen/
    RUN echo done
`

	p, err := interp.Build(src, "main",
		interp.WithArtifacts(func(string, string) (string, error) { return made, nil }))
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	found := false

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpFile || len(n.Op.Args) < 2 {
			continue
		}

		if filepath.Base(n.Op.Args[0]) != "bc.txt" {
			continue
		}

		found = true

		if n.Op.Args[0] != "/test/bc.txt" {
			t.Errorf("the Dockerfile's COPY reads %q"+
				"\n  the artifact is saved by `SAVE ARTIFACT ./*` under WORKDIR"+
				" /test, so it is at /test/bc.txt", n.Op.Args[0])
		}
	}

	if !found {
		t.Fatal("the Dockerfile's COPY produced no copy node")
	}
}
