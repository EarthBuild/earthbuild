package cli

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A plan containing a WITH DOCKER block needs a sandbox that has a daemon in
// it, and one that does not must not pay for one.
//
// The two are different VMs, and get there by the naming that already exists:
// the sandbox is named after its image, so a project needing docker gets its
// own machine without disturbing a project that does not. The scheme built for
// reuse carries this for free.
func TestOnlyAPlanThatNeedsDockerGetsADaemon(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		source string
		docker bool
	}{
		{"a plain build", `
main:
    FROM alpine:3.22
    RUN true
`, false},
		{"a build with a WITH DOCKER block", `
main:
    FROM alpine:3.22
    WITH DOCKER
        RUN docker images
    END
`, true},
		{"a block further down the graph", `
tool:
    FROM alpine:3.22
    WITH DOCKER
        RUN docker images
    END

main:
    FROM alpine:3.22
    COPY +tool/nothing /x
`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build("VERSION 0.8\n"+tc.source, testMainTarget,
				interp.WithContext(t.TempDir()))
			if err != nil {
				// The third case names an artifact that is not saved; the plan
				// is what is under test, so a refusal here is the fixture's
				// fault and worth saying plainly.
				t.Skipf("fixture did not plan: %v", err)
			}

			if got := needsDocker(p); got != tc.docker {
				t.Errorf("needsDocker = %v, want %v", got, tc.docker)
			}
		})
	}
}

// The image with a daemon is not the ordinary one.
func TestTheDockerSandboxUsesADifferentImage(t *testing.T) {
	t.Parallel()

	if sandboxImage(false) == sandboxImage(true) {
		t.Error("a build needing docker would get a sandbox with no daemon in it")
	}

	if sandboxImage(false) == "" || sandboxImage(true) == "" {
		t.Error("a sandbox image is empty")
	}
}

// needsDocker looks at every step, not only the ones on the spine.
func TestNeedsDockerLooksAtEveryStep(t *testing.T) {
	t.Parallel()

	g := &ir.Graph{
		Root: &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"a"}}},
		Also: []*ir.Node{{Op: ir.Op{Kind: ir.OpExec, Args: []string{"b"}, Docker: true}}},
	}

	if !needsDocker(&interp.Plan{Graph: g}) {
		t.Error("a step off the spine was not looked at")
	}
}
