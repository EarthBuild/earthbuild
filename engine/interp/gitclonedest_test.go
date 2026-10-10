package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestAGitCloneLandsUnderTheWorkingDirectory.
//
// `WORKDIR /test` then `GIT CLONE <url> buildkit` puts the checkout at
// `/test/buildkit`, and `tests/git-clone.earth` says so twice: it asserts
// `pwd` is `/test` and then does `WORKDIR /test/buildkit`.
//
// The destination went to the step unanchored, so the clone landed at
// `/buildkit` - a directory the Earthfile never mentions - and the failure
// arrived two lines later as `ls .git` finding nothing, which is a question
// about git and not about where anything went.
//
// `resolveDest` is the rule and its comment is already about this: *"`WORKDIR
// /app` then `COPY . .` is the most common pair of lines in container builds,
// and without this the files landed at the filesystem root - with the symptom
// arriving two steps later"*. GIT CLONE is a copy with a destination and was
// the one that did not call it.
func TestAGitCloneLandsUnderTheWorkingDirectory(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WORKDIR /test
    GIT CLONE https://example.invalid/r.git checkout
    RUN true
`, testMain, interp.WithGitClone(func(string, string) (string, error) {
		return ctxWith(t, map[string]string{"README": "x"}), nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	var dests []string

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile && len(n.Op.Args) == 2 {
			dests = append(dests, n.Op.Args[1])
		}
	}

	if len(dests) == 0 {
		t.Fatal("the clone was not copied into the build")
	}

	for _, got := range dests {
		if !strings.HasPrefix(got, "/test/") {
			t.Errorf("the clone lands at %q, and the working directory is"+
				" /test - a checkout at the filesystem root is one the"+
				" Earthfile never asked for", got)
		}
	}
}
