package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// WORKDIR is state: several in one stage, each replacing the last, a relative
// one resolving against it - and a later ENV changing what a later WORKDIR
// reads. Expansion must therefore use the environment as it stands at that
// point, not one snapshot for the stage.
func TestDockerfileWorkdirsComposeAndReExpand(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22
ENV ROOT=/go
WORKDIR $ROOT/src
ENV SUB=thing
WORKDIR $SUB
ENV SUB=other
WORKDIR /abs/$SUB
RUN make it
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && n.Op.Dir != "/abs/other" {
			t.Errorf("step runs in %q, want /abs/other", n.Op.Dir)
		}
	}
}
