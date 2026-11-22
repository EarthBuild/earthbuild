package exec_test

import (
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// The sandbox VM asks for every capability where the CLI can grant them.
//
// **0.12.0 reduced the default set** (apple/container#1260), and the guest
// agent mounts filesystems and makes namespaces - CAP_SYS_ADMIN and friends,
// which a Docker-shaped default does not hold. Before 0.12.0 every container
// had them and `--cap-add` is an option the CLI does not know, so passing it
// there refuses the run. Unknown asks for nothing: an old CLI is still the
// commoner install, because Homebrew's formula lags.
func TestTheSandboxAsksForCapabilitiesWhereTheCLICanGrantThem(t *testing.T) {
	t.Parallel()

	for version, want := range map[string][]string{
		"container CLI version 1.4.1 (build: release, commit: abc)":     {"--cap-add", "ALL"},
		"container CLI version 0.12.0":                                  {"--cap-add", "ALL"},
		"container CLI version 0.11.0":                                  nil,
		"container CLI version 0.9.0 (build: release, commit: unspeci)": nil,
		"":                     nil,
		"something unexpected": nil,
	} {
		if got := exec.AppleCapArgs(version); !slices.Equal(got, want) {
			t.Errorf("%q: got %q, want %q", version, got, want)
		}
	}
}
