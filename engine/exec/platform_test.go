package exec

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A node's platform decides which image is pulled for it.
//
// Without this the interpreter records a platform, the key changes, two builds
// are planned - and both pull the same image. The plan would be right and the
// result wrong, which is the most expensive shape of bug available here.
func TestNodePlatformDecidesTheImagePulled(t *testing.T) {
	t.Parallel()

	e := &Executor{Platform: testOtherPlatform}

	for _, tc := range []struct {
		name string
		node ir.Platform
		want string
	}{
		{"the node's platform wins", ir.Platform{OS: "linux", Arch: testArch}, testPlatform},
		{"a variant is carried", ir.Platform{OS: "linux", Arch: "arm", Variant: "v7"}, "linux/arm/v7"},
		{"an unset node falls back to the executor's", ir.Platform{}, testOtherPlatform},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			n := &ir.Node{Platform: tc.node, Op: ir.Op{Kind: ir.OpImage, Args: []string{"alpine"}}}

			if got := e.platformFor(n); got != tc.want {
				t.Errorf("pulls for %q, want %q", got, tc.want)
			}
		})
	}
}
