package ir_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// One rule for whether a machine serves a platform, in one place.
//
// The rule is: the OS and the architecture must be equal, and the variant is
// compared only when both sides state one. A variant is optional almost
// everywhere it appears - an OCI image configuration need not carry it, a
// worker reports `runtime.GOOS/GOARCH` and so never has one - and a silent side
// is declining to say rather than claiming to be none of them.
//
// Written down because it was implemented four times and differently. In one
// session, `linux/arm/v7` was refused by scheduling (E942), by manifest
// selection (E946) and by the image-configuration check (E951), each reached
// only by fixing the one before it, and each comparing a triple against a pair.
func TestPlatformMatchesIsLooseOnlyWhereOneSideIsSilent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		have, want ir.Platform
		ok         bool
	}{{
		name: "identical",
		have: ir.Platform{OS: "linux", Arch: "arm", Variant: "v7"},
		want: ir.Platform{OS: "linux", Arch: "arm", Variant: "v7"},
		ok:   true,
	}, {
		name: "the wanting side states a variant and the having side does not",
		have: ir.Platform{OS: "linux", Arch: "arm"},
		want: ir.Platform{OS: "linux", Arch: "arm", Variant: "v7"},
		ok:   true,
	}, {
		name: "the having side states one and the wanting side does not",
		have: ir.Platform{OS: "linux", Arch: "arm64", Variant: "v8"},
		want: ir.Platform{OS: "linux", Arch: "arm64"},
		ok:   true,
	}, {
		name: "both state one and they differ",
		have: ir.Platform{OS: "linux", Arch: "arm", Variant: "v6"},
		want: ir.Platform{OS: "linux", Arch: "arm", Variant: "v7"},
	}, {
		name: "a different architecture",
		have: ir.Platform{OS: "linux", Arch: "arm64"},
		want: ir.Platform{OS: "linux", Arch: "arm"},
	}, {
		name: "a different operating system",
		have: ir.Platform{OS: "darwin", Arch: "arm64"},
		want: ir.Platform{OS: "linux", Arch: "arm64"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.have.Matches(tc.want); got != tc.ok {
				t.Errorf("%s serving %s = %v, want %v", tc.have, tc.want, got, tc.ok)
			}
		})
	}

	// Symmetric, because neither side is privileged: an image serving a build
	// and a build served by an image are the same question.
	a := ir.Platform{OS: "linux", Arch: "arm"}
	b := ir.Platform{OS: "linux", Arch: "arm", Variant: "v7"}

	if a.Matches(b) != b.Matches(a) {
		t.Error("the rule is not symmetric, so which side asks changes the answer")
	}
}
