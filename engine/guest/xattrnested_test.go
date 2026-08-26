//go:build unix

package guest

import "testing"

// TestANestedOverlaysBookkeepingIsNotOurs.
//
// **overlayfs escapes its own attributes when it sees them.** A build inside a
// build mounts an overlay whose upper lives in the outer step's filesystem,
// which is itself an overlay - so the outer one finds `trusted.overlay.origin`
// on those files and, to keep the two from being confused with each other,
// presents it as `trusted.overlay.overlay.origin`.
//
// Carrying that into a layer fails outright, and takes the whole nested build
// with it (E706):
//
//	carry the extended attribute trusted.overlay.overlay.origin onto …
//
// It is the same case as `com.apple.provenance` and answered by the same rule:
// this is a filesystem's private record of how it is storing something, not
// anything the build put there. Nothing a layer contains is described by it.
//
// The unescaped names stay. `trusted.overlay.opaque` carries a deletion and is
// wanted; the others are in the digests of every layer already stored, and
// dropping them is a separate decision with a cache behind it.
func TestANestedOverlaysBookkeepingIsNotOurs(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		want bool
	}{
		{"trusted.overlay.overlay.origin", false},
		{"trusted.overlay.overlay.opaque", false},
		{"trusted.overlay.overlay.impure", false},
		{"com.apple.provenance", false},

		{"trusted.overlay.opaque", true},
		{"trusted.overlay.origin", true},
		{"user.something", true},
		{"security.capability", true},
	} {
		if got := ours(c.name); got != c.want {
			t.Errorf("ours(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
