package interp_test

import "testing"

// Which refusals are this sweep's fault rather than the engine's.
//
// E411 reported "257 of 456, and roughly 63% once you discount the ones the
// sweep causes" - a number with a judgement in a paragraph. This is the same
// judgement written where it can be read, tested and disagreed with.
//
// A `tests/*.earth` file is run by a harness that copies it somewhere as
// `Earthfile`, puts the context files it names beside it, and builds the targets
// it references. This sweep does none of that: it hands the file to the
// interpreter where it lies. Every refusal below follows from that and from
// nothing about the engine.
func TestWhichRefusalsAreTheSweepsOwn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		what string
		ours bool
	}{
		{
			// The file names a COPY source the harness would have created.
			name: "a context file the harness makes", what: "missing context file",
			ours: true,
		},
		{
			// `+other` in a file the sweep hands over alone.
			name: "a sibling target", what: "unknown target", ours: true,
		},
		{
			// `./dir+target` where the harness would have written that dir.
			name: "a sibling Earthfile", what: "no Earthfile for this reference",
			ours: true,
		},
		{
			// These are the engine's, and must not be discounted.
			name: "an unimplemented command", what: "HOST",
		},
		{
			name: "a remote reference",
			what: `"github.com/EarthBuild/x:main" refers to a target in a remote repository`,
		},
		{
			name: "an unknown VERSION flag",
			what: "VERSION --build-auto-skip is a feature this engine does not know",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := harnessCondition(tc.what); got != tc.ours {
				t.Errorf("harnessCondition(%q) = %v, want %v", tc.what, got, tc.ours)
			}
		})
	}
}
