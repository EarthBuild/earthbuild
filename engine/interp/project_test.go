package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `PROJECT org/project` is accepted, and changes nothing about the build.
//
// It names the organisation and project a build belongs to, which is what the
// hosted service resolves secrets against. This engine resolves secrets from
// the invocation and nowhere else, so the declaration has nothing to act on -
// and refusing a build over a line that only says who owns it would be refusing
// the whole Earthfile for a fact it never uses.
func TestProjectIsAccepted(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8
PROJECT acme/widgets

main:
    FROM alpine:3.22
    RUN build
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Graph.Nodes()) == 0 {
		t.Error("the Earthfile planned nothing")
	}
}

// It does not change what is built, which is the claim that makes ignoring it
// safe rather than convenient.
func TestProjectDoesNotChangeTheBuild(t *testing.T) {
	t.Parallel()

	const recipe = `
main:
    FROM alpine:3.22
    RUN build
`

	with, err := interp.Build("VERSION 0.8\nPROJECT acme/widgets\n"+recipe, testMain)
	if err != nil {
		t.Fatal(err)
	}

	without, err := interp.Build("VERSION 0.8\n"+recipe, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if with.Graph.Root.ID() != without.Graph.Root.ID() {
		t.Error("declaring a project changed what the build does")
	}
}

// A malformed declaration is refused rather than recorded as nonsense.
func TestAProjectNeedsAnOrgAndAName(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(`VERSION 0.8
PROJECT widgets

main:
    FROM alpine:3.22
`, testMain)
	if err == nil {
		t.Fatal("a project with no organisation was accepted")
	}

	if !strings.Contains(err.Error(), "PROJECT") {
		t.Errorf("the refusal does not name the construct:\n%s", err)
	}
}
