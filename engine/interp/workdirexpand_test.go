package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// An Earthfile's WORKDIR already expands a build argument. Kept so the fix for
// the Dockerfile case below cannot quietly take this with it.
func TestWorkdirExpandsAnArg(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8

test:
    FROM alpine:3.20
    ARG GOPATH=/go
    WORKDIR $GOPATH/src/thing
    RUN true
`, "test")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && n.Op.Dir != "/go/src/thing" {
			t.Errorf("step runs in %q, want /go/src/thing", n.Op.Dir)
		}
	}
}

// **A Dockerfile's WORKDIR expands what its ENV set**, which is Docker's rule
// and not this engine's to reinterpret. It did not, and the argument reached the
// graph with the `$` still in it - so the step ran in a directory *named*
// `$GOPATH`.
//
// Found in buildkit's own Dockerfile, which this repository builds:
// `WORKDIR $GOPATH/src/github.com/opencontainers/runc` put the step somewhere
// the `--mount=type=bind,target=.` had not been placed, and the build died on
// `go: go.mod file not found` - a message about the wrong thing entirely, three
// layers away from the cause.
func TestADockerfileWorkdirExpandsItsEnv(t *testing.T) {
	t.Parallel()

	dir := withDockerfile(t, "Dockerfile", `FROM alpine:3.22
ENV GOPATH=/go
WORKDIR $GOPATH/src/thing
RUN make the-thing
`)

	p, err := interp.Build(versioned+`
main:
    FROM DOCKERFILE .
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		if strings.Contains(n.Op.Dir, "$") {
			t.Fatalf("step runs in %q, which still holds a variable", n.Op.Dir)
		}

		if n.Op.Dir != "/go/src/thing" {
			t.Errorf("step runs in %q, want /go/src/thing", n.Op.Dir)
		}
	}
}
