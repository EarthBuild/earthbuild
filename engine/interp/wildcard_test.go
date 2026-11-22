package interp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A wildcard target reference is refused as a missing feature, not a missing
// target.
//
// `COPY +sub*/out.txt` is the `--wildcard-copy` feature: the reference expands
// to every target whose name matches. This engine does not expand it, and said
// so by looking up a target literally called `sub*` and reporting that no such
// target exists - which is true and useless. An author reading it goes looking
// for a typo in a name they wrote correctly.
//
// *Failure class: a missing feature reported as missing input.* The two want
// opposite responses - one is "add the target", the other is "this engine cannot
// do that yet" - and only the second is true (E412).
func TestAWildcardTargetIsRefusedAsAFeature(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"COPY", "VERSION 0.8\nmain:\n    FROM alpine\n    COPY +sub*/out.txt /x\n"},
		{"BUILD", "VERSION 0.8\nmain:\n    FROM alpine\n    BUILD +sub*\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(tc.src, "main")
			if err == nil {
				t.Fatal("a wildcard reference was expanded, which this engine cannot do")
			}

			if strings.Contains(err.Error(), "no target named") {
				t.Errorf("a missing feature is reported as a missing target: %v", err)
			}

			if !strings.Contains(err.Error(), "wildcard") {
				t.Errorf("the refusal does not say what is missing: %v", err)
			}

			// An engine limitation, so it is counted as work rather than as the
			// author's mistake - the distinction the corpus sweep is built on.
			if !errors.Is(err, interp.ErrUnimplemented) {
				t.Errorf("the refusal is not classified as unimplemented: %v", err)
			}
		})
	}
}

// And naming the feature flag no longer refuses the whole file.
//
// `VERSION --wildcard-copy` on a file that never uses a wildcard was refused
// outright, taking 24 targets in the `tests/` tree with it (E411). A flag is a
// statement about what the file *may* use; the refusal belongs at the construct
// that uses it, which now has one.
func TestNamingTheWildcardFlagDoesNotRefuseTheFile(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--wildcard-copy", "--wildcard-builds"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			src := "VERSION " + flag + " 0.8\nmain:\n    FROM alpine\n    RUN true\n"

			_, err := interp.Build(src, "main")
			if err != nil {
				t.Errorf("a file naming %s was refused although it uses no wildcard: %v",
					flag, err)
			}
		})
	}
}
