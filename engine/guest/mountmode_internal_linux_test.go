package guest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// A mount is staged with the mode the Earthfile asked for.
//
// `--mount=type=secret,mode=0100` is in this repository's own corpus, three
// times with three different modes, and the field was read into a map and never
// looked at again: the secret arrived 0400 whatever was written. A credential
// more readable than the author asked for is the direction that matters, and a
// step that checks the mode before using the file fails for a reason nothing in
// the build explains (E435).
//
// Against `bindMounts` itself rather than through a step, because the step
// fixture is a bare root with no `stat` in it - and what is being tested is the
// staging, which is this function's whole job.
func TestAMountIsStagedWithTheModeAsked(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	for _, tc := range []struct {
		name  string
		mount Mount
		want  os.FileMode
	}{{
		name:  "a secret with no mode is readable only by its owner",
		mount: Mount{Target: "/run/s", Secret: "shhh"},
		want:  0o400,
	}, {
		name:  "a secret takes the mode written",
		mount: Mount{Target: "/run/s", Secret: "shhh", Mode: 0o100},
		want:  0o100,
	}, {
		name:  "a cache directory with no mode is the usual 0755",
		mount: Mount{Target: "/c", ID: "cargo"},
		want:  0o755,
	}, {
		name:  "a cache directory takes the mode written",
		mount: Mount{Target: "/c", ID: "cargo", Mode: 0o700},
		want:  0o700,
	}, {
		name:  "a private cache takes the mode written",
		mount: Mount{Target: "/c", Ephemeral: true, Mode: 0o777},
		want:  0o777,
	}, {
		// **The mode a caller asks for is not evidence the file has it.**
		// An ephemeral directory is made by MkdirTemp, which makes it 0700,
		// and the mode was applied only when it differed from the default
		// the call passed - so 0755, the one value that default names, was
		// the one value never written. /dev asks for exactly that, and a
		// step running as a non-root USER could not traverse it: `ls:
		// /dev/null: Permission denied`, and an entrypoint reading the
		// failed `> /dev/null` as proof it was unprivileged (E936).
		name:  "an ephemeral directory takes 0755, which is also the default",
		mount: Mount{Target: "/c", Ephemeral: true, Mode: 0o755},
		want:  0o755,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			root, store := t.TempDir(), t.TempDir()

			undo, err := bindMounts(root, store, layerStoreForTest(t), "", []Mount{tc.mount})
			if err != nil {
				t.Fatalf("binding %+v: %v", tc.mount, err)
			}

			defer undo()

			// Through the target, which is what the step sees. The staging
			// directory is where it was written and the bind is what makes it
			// the step's, so asking the source would be asking a question the
			// step cannot ask.
			info, err := os.Stat(filepath.Join(root, tc.mount.Target))
			if err != nil {
				t.Fatalf("stat: %v", err)
			}

			if got := info.Mode().Perm(); got != tc.want {
				t.Errorf("%s is %#o, and the mount asked for %#o",
					tc.mount.Target, got, tc.want)
			}
		})
	}
}
