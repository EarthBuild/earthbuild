package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// cloner stands in for fetching a repository.
type cloner struct {
	calls [][2]string
	dir   string
}

func (c *cloner) clone(url, ref string) (string, error) {
	c.calls = append(c.calls, [2]string{url, ref})

	return c.dir, nil
}

// `GIT CLONE <url> <dest>` puts a repository into the image.
//
// Content-addressed like any other source: the checkout is digested at graph
// construction, so a build whose dependency moved gets a different key. Keyed on
// the *path* instead would leave the graph unchanged when the branch advanced,
// and the build would hit the cache and reproduce the previous checkout.
func TestGitCloneBringsARepositoryIn(t *testing.T) {
	t.Parallel()

	c := &cloner{dir: ctxWith(t, map[string]string{"README.md": "the repo\n"})}

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    GIT CLONE https://github.com/org/repo /src
    RUN build
`, testMain, interp.WithGitClone(c.clone))
	if err != nil {
		t.Fatal(err)
	}

	if len(c.calls) != 1 || c.calls[0][0] != "https://github.com/org/repo" {
		t.Fatalf("cloned %v, want the url as written", c.calls)
	}

	var copied *ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile {
			copied = n
		}
	}

	if copied == nil {
		t.Fatalf("nothing copies the checkout in:\n%s", describe(p.Graph.Nodes()))
	}

	if len(copied.Op.Args) < 2 || copied.Op.Args[1] != "/src" {
		t.Errorf("copied to %v, want /src", copied.Op.Args)
	}

	// The checkout's contents decide the key, so a repository that moved is a
	// different build.
	if len(copied.Sources) != 1 || copied.Sources[0].Op.Content == (ir.NodeID{}) {
		t.Error("the checkout's contents are not in the graph, so a moved branch would hit the cache")
	}
}

// `--branch` names the ref, which is what makes a clone reproducible.
func TestGitCloneBranchIsPassedOn(t *testing.T) {
	t.Parallel()

	c := &cloner{dir: ctxWith(t, map[string]string{"x": "y\n"})}

	if _, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    GIT CLONE --branch stable https://example.test/repo /src\n",
		testMain, interp.WithGitClone(c.clone)); err != nil {
		t.Fatal(err)
	}

	if len(c.calls) != 1 || c.calls[0][1] != "stable" {
		t.Errorf("cloned %v, want the branch as written", c.calls)
	}
}

// Without a cloner it is refused by name, so a plan-only caller never reaches
// the network.
func TestGitCloneWithoutAClonerIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    GIT CLONE https://example.test/repo /src\n", testMain)
	if err == nil {
		t.Fatal("GIT CLONE was accepted with no way to clone")
	}

	if !strings.Contains(err.Error(), "GIT CLONE") {
		t.Errorf("the refusal does not name the construct:\n%s", err)
	}
}
