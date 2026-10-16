package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

const argAsEnv = versioned + `
build:
    FROM alpine:3.22
    ARG GOOS=linux
    ARG GOARCH=amd64
    RUN go build -o out ./cmd
`

// **A build argument is an environment variable inside the step.**
//
// The reference exports them, and a great deal of real Earthfile depends on it:
// this repository's own cross-compilation is `ARG GOOS` and `ARG GOARCH` beside
// a `go build` that names neither, because the Go toolchain reads them from the
// environment.
//
// Substituting them into the command text is not the same thing and looks
// identical in the common case. Differentially, on `echo "env=${MYVAR:-UNSET}"`:
//
//	earthly   env=hello
//	earth     env=UNSET-IN-ENV
//
// The cost of the gap is silent: `+all-binaries` built five platforms, reported
// success, and produced five identical linux/arm64 binaries - including the two
// darwin ones and the .exe - because `go build` never saw a GOOS (E580).
func TestABuildArgumentIsEnvironmentInsideTheStep(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(argAsEnv, "build")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if !strings.Contains(n.Meta.Description, "go build") {
			continue
		}

		env := n.Op.Env
		if env["GOOS"] != "linux" {
			t.Errorf("GOOS is %q in the step's environment, want linux"+
				"\n  the argument was substituted into the text and never exported",
				env["GOOS"])
		}

		if env["GOARCH"] != "amd64" {
			t.Errorf("GOARCH is %q in the step's environment, want amd64\n  whole env: %v",
				env["GOARCH"], env)
		}

		return
	}

	t.Fatal("no go build step in the plan")
}
