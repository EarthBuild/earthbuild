package interp_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `EARTHLY_TARGET` is the reference as this build would write it.
//
// A target in the invoked directory is `+name`; one in a subdirectory is
// `./sub+name`, which is how the caller reached it and what the reference
// reports - `referenceString` uses the local path verbatim, and only a target in
// the current directory gets the bare `+name` form.
//
// This engine gave every local target the bare form, so a sub-target asked its
// own name and was told a name that names something else. It was hidden until
// E943 stopped `--pass-args` handing down the *caller's* answer, which was wrong
// in a way that looked the same from inside `tests/pass-args-no-builtins`
// (E945).
func TestABuiltinTargetNamesTheReferenceAsWritten(t *testing.T) {
	t.Parallel()

	dir := ctxWith(t, map[string]string{
		"sub/Earthfile": versioned + `
subtest:
    FROM alpine:3.22
    ARG EARTHLY_TARGET
    RUN echo "target=$EARTHLY_TARGET"
`,
	})

	p, err := interp.Build(versioned+`
test:
    FROM alpine:3.22
    ARG EARTHLY_TARGET
    RUN echo "target=$EARTHLY_TARGET"
    BUILD ./sub+subtest
`, "test", interp.WithContext(dir))
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	var got []string

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		for _, a := range n.Op.Args {
			if i := strings.Index(a, "target="); i >= 0 {
				got = append(got, strings.Trim(a[i+len("target="):], `"`))
			}
		}
	}

	slices.Sort(got)

	want := []string{"+test", "./sub+subtest"}

	if !slices.Equal(got, want) {
		t.Errorf("the two targets report %q, want %q", got, want)
	}
}
