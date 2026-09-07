package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// **A local function reads the caller's context too.**
//
// TestAFunctionCopiesFromTheCallersContext establishes the rule and says of the
// local case: *"Locally they are the same directory and nothing can tell them
// apart."* They are the same directory only when the function and its caller
// share one. A function defined in a parent Earthfile and called from a
// subdirectory separates them exactly as a remote one does, and `callerContext`
// answers only for the remote half - so the copy looked in the function's
// directory and reported the caller's own file missing.
//
// `tests/invalid/Earthfile` is the instance: it calls `tests+RUN_EARTH`, whose
// `COPY "$earthfile"` names `trailing-backslash.earth` - a file in
// `tests/invalid/`. Buildkit builds it and this engine did not, which is what
// makes it a defect rather than a difference (nit #80).
func TestALocalFunctionCopiesFromTheCallersContext(t *testing.T) {
	t.Parallel()

	dir := ctxWith(t, map[string]string{
		"sub/Earthfile": versioned + `
use:
    FROM alpine:3.22
    DO ..+COPY_IT
`,
		"sub/theirs.txt": "belongs to the caller\n",
	})

	p, err := interp.Build(versioned+`
main:
    BUILD ./sub+use

COPY_IT:
    FUNCTION
    COPY theirs.txt ./
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("the caller's own file was not found: %v", err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "theirs.txt") {
		t.Errorf("the copy did not reach the plan:\n%s", got)
	}
}
