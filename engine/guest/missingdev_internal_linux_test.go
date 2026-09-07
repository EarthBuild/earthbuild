package guest

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A device a step should have and did not get is reported.
//
// `deviceMounts` skips a device the sandbox does not have, deliberately: a
// sandbox image is entitled to differ and a build must not stop because the
// machine has no /dev/full. What was missing is the other half - nothing said
// which. A step without /dev/null does not report that; it reports whatever
// tried to use it, and in CI that was `line 53: can't create /dev/null:
// Permission denied` followed by an entrypoint concluding, wrongly, that the
// container was unprivileged (E845).
//
// The same rule as /sys, /sys/fs/cgroup and /dev/pts, which this engine already
// reports: degrade, and say so.
func TestMissingDevicesAreNamed(t *testing.T) {
	t.Parallel()

	t.Run("nothing to say when the machine has them all", func(t *testing.T) {
		t.Parallel()

		// The real root: this machine has /dev/null, and if it does not, the
		// test's own harness is the least of anyone's problems.
		if got := missingDevices("/"); len(got) != 0 {
			t.Errorf("this machine reports missing devices: %v", got)
		}
	})

	t.Run("names the ones that are absent", func(t *testing.T) {
		t.Parallel()

		// An empty directory as the device root: everything is missing.
		got := missingDevices(t.TempDir())

		for _, want := range []string{"/dev/null", "/dev/urandom"} {
			if !slices.Contains(got, want) {
				t.Errorf("%s is absent and was not named: %v", want, got)
			}
		}
	})

	t.Run("names only what is absent", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()

		err := os.MkdirAll(filepath.Join(root, "dev"), 0o755)
		if err != nil {
			t.Fatal(err)
		}

		// A regular file is enough: this asks whether the path is there, not
		// what kind of thing it is - the bind that follows answers that, and
		// answers it with an error rather than a silence.
		err = os.WriteFile(filepath.Join(root, "dev", "null"), nil, 0o644)
		if err != nil {
			t.Fatal(err)
		}

		if got := missingDevices(root); slices.Contains(got, "/dev/null") {
			t.Errorf("/dev/null is present and was named missing: %v", got)
		}
	})
}
