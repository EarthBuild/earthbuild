package engine

import (
	"strings"
	"testing"
)

// A BuildKit VM is given half the host's memory, never less than 16 GiB.
//
// Every build step runs inside this one VM, so its ceiling is the whole
// build's. At 4 GiB - a quarter of a 16 GiB Mac - a Rust or C++ compile is
// killed by the guest kernel, and all the build reports is
// `rustc was terminated by a deadly signal`. The figure is a ceiling rather
// than a reservation, so a floor above a small host's memory costs nothing
// until something uses it.
func TestAppleContainerMemoryIsHalfTheHostWithAFloor(t *testing.T) {
	t.Parallel()

	const gib = uint64(1) << 30

	for _, tc := range []struct {
		host uint64
		want string
	}{
		{host: 0, want: "16384M"},        // unknown host: the floor
		{host: 8 * gib, want: "16384M"},  // smaller than the floor: the floor
		{host: 16 * gib, want: "16384M"}, // half is 8 GiB: the floor
		{host: 64 * gib, want: "32768M"},
		{host: 128 * gib, want: "65536M"},
	} {
		if got := containerMemoryFor(tc.host); got != tc.want {
			t.Errorf("containerMemoryFor(%d GiB) = %s, want %s", tc.host/gib, got, tc.want)
		}
	}
}

// A `container` CLI too old for the flags a privileged container is started
// with is refused by name, before anything is started.
//
// `--cap-add` arrived in 0.12.0 and `--read-only-path`/`--masked-path` in
// 1.2.1. Below that, starting BuildKit fails with the CLI's own "unknown
// option" error, which names a flag rather than the fix.
func TestAnAppleContainerCLITooOldIsRefusedByName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		out string
		ok  bool
	}{
		{"container CLI version 1.4.1 (build: release, commit: abc)", true},
		{"container CLI version 1.2.1 (build: release, commit: abc)", true},
		{"container CLI version 1.10.0", true},
		{"container CLI version 2.0.0", true},
		{"container CLI version 1.2.0 (build: release, commit: abc)", false},
		{"container CLI version 0.12.0", false},
		{"container CLI version 0.9.0 (build: release, commit: unspeci)", false},
	} {
		err := checkAppleContainerVersion(tc.out)
		if tc.ok && err != nil {
			t.Errorf("%q was refused: %v", tc.out, err)
		}

		if !tc.ok {
			if err == nil {
				t.Errorf("%q was accepted, and cannot start a privileged container", tc.out)

				continue
			}

			for _, want := range []string{minAppleContainerVersion, "upgrade"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal of %q does not say %q: %v", tc.out, want, err)
				}
			}
		}
	}
}

// An answer that is not a version is not taken for an old one.
//
// A CLI that changed how it prints its version would otherwise be refused
// forever for a reason that is not true. Unparseable passes, and the start
// that follows reports whatever is actually wrong.
func TestAnUnreadableAppleContainerVersionIsNotRefused(t *testing.T) {
	t.Parallel()

	for _, out := range []string{"", "container version unknown", "something else entirely"} {
		if err := checkAppleContainerVersion(out); err != nil {
			t.Errorf("%q was refused: %v", out, err)
		}
	}
}
