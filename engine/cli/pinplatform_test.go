package cli

import (
	"runtime"
	"testing"
)

// A reference is resolved for the platform steps run on, not the one this
// process runs on.
//
// On macOS the engine runs on darwin and every step runs on linux inside a VM,
// and a plan for the native platform names no platform at all. Defaulting to
// this process's OS asks a registry for `darwin/arm64`, which no image has:
// `no manifest for darwin/arm64`, every reference left unpinned, on the one
// platform this engine is developed on.
//
// The same lesson as E503, where a darwin worker declared `darwin/arm64` to the
// fleet and was therefore never given a step it could have run: *the platform
// that matters is the sandbox's, not the process's*.
func TestAReferenceIsResolvedForThePlatformStepsRunOn(t *testing.T) {
	t.Parallel()

	got := resolveFor("")
	if got != "linux/"+runtime.GOARCH {
		t.Errorf("a plan naming no platform resolves for %q, want linux/%s"+
			"\n  images are linux images however this engine was built", got, runtime.GOARCH)
	}
}

// A platform the plan does name is honoured.
//
// A cross build asked for `linux/amd64` means it, and substituting the sandbox's
// platform would silently build the wrong architecture.
func TestAStatedPlatformIsHonoured(t *testing.T) {
	t.Parallel()

	if got := resolveFor("linux/amd64"); got != "linux/amd64" {
		t.Errorf("asked for linux/amd64 and resolved for %q", got)
	}
}
