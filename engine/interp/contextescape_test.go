package interp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A COPY source cannot reach outside the build context through a symlink.
//
// The source is checked as written - no `..`, nothing absolute - and a link's
// name says nothing about where it leads: `lnk -> ~/.ssh/id_ed25519` is `lnk`.
// Staging followed it, so a remote Earthfile whose checkout held one put a host
// file into the build. BuildKit resolves a link inside the context it was sent,
// where the target does not exist.
//
// The control is a link that stays inside, which BuildKit follows and so must
// this: refusing every link would pass the escapes for the wrong reason.
func TestACopySourceCannotLeaveTheContextThroughASymlink(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()

	err := os.WriteFile(filepath.Join(outside, "secret"), []byte("host only"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, link, to, copy string
		escapes              bool
	}{
		{name: "a link to a file outside", link: "lnk", to: filepath.Join(outside, "secret"), copy: "lnk", escapes: true},
		{name: "a link on the way to one", link: "sub", to: outside, copy: "sub/secret", escapes: true},
		{name: "a link that stays inside", link: "lnk", to: "data/f", copy: "lnk"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := contextHolding(t, "data/f", "in the context")

			err := os.Symlink(tc.to, filepath.Join(dir, tc.link))
			if err != nil {
				t.Fatal(err)
			}

			src := "\nmain:\n    FROM alpine:3.22\n    COPY " + tc.copy + " /f\n"

			_, err = interp.Build(versioned+src, testMain, interp.WithContext(dir))
			if tc.escapes && err == nil {
				t.Errorf("COPY %s was planned, and %s resolves outside the context to %s",
					tc.copy, tc.link, tc.to)
			}

			if !tc.escapes && err != nil {
				t.Errorf("COPY %s through a link inside the context was refused: %v", tc.copy, err)
			}
		})
	}
}
