package cli

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// `native` is a word the Earthfile uses and no registry has ever heard of.
//
// `COPY --platform=native` means this machine, and the interpreter resolves it
// (`resolveNative`). The pinning path passed it through, so a resolver was asked
// for a manifest matching the literal string and said so:
//
//	note: alpine:3.24.1 was not pinned: alpine:3.24.1: no manifest for native
//	  this image provides: linux/amd64, linux/arm/v6, linux/arm/v7, ...
//
// Which reads as the image being at fault for not providing a platform that
// cannot exist. Not fatal - an unpinned build is a build - but the note is what
// a reader has to act on, and it named the wrong party (E955).
func TestPinningResolvesTheWordNative(t *testing.T) {
	t.Parallel()

	here := exec.DefaultPlatform()

	for _, tc := range []struct{ given, want string }{
		{"", here},
		{"native", here},
		{"linux/arm64", "linux/arm64"},
		{"linux/arm/v7", "linux/arm/v7"},
	} {
		if got := resolveFor(tc.given); got != tc.want {
			t.Errorf("resolveFor(%q) = %q, want %q", tc.given, got, tc.want)
		}
	}
}
