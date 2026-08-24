package exec

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/EarthBuild/earthbuild/engine/guestd"
)

// guestBinaryName is what the agent is called wherever it is found.
const guestBinaryName = "earth-guestd"

// findGuestBinary locates earth-guestd.
//
// It does *not* build it. An earlier version ran `go build` on demand, which
// worked in this repository's tests and failed the first time the binary was run
// from a user's project directory: there is no go.mod there, and the error
// arrived as a module resolution failure from a build tool the user was not
// aware they were running.
//
// Looked for, in order:
//
//  1. $EARTH_GUESTD, for development and for tests in stripped containers;
//  2. beside the running executable, which is how the Apple backend gets a
//     linux agent for its VM.
//
// A third answer, the running executable itself, is [findGuestCommand]'s and
// not this function's: it holds only where the sandbox runs the host's own kind
// of binary, which is not true of a linux VM on a Mac.
func findGuestBinary() (string, error) {
	if p := os.Getenv("EARTH_GUESTD"); p != "" {
		// $EARTH_GUESTD is set by whoever runs the engine, so a path from it
		// is the operator's own choice rather than anything a build can
		// influence. Statting it says only whether they were right.
		_, err := os.Stat(p) //nolint:gosec // an operator's own environment
		if err != nil {
			return "", fmt.Errorf("EARTH_GUESTD is set to %s, which is not there: %w", p, err)
		}

		return p, nil
	}

	exe, err := os.Executable()
	if err == nil {
		beside := filepath.Join(filepath.Dir(exe), guestBinaryName)
		_, err := os.Stat(beside)
		if err == nil {
			return beside, nil
		}
	}

	return "", fmt.Errorf(
		"cannot find %s, the agent that runs inside the sandbox"+
			"\n  it is expected beside this binary, or at $EARTH_GUESTD"+
			"\n  in a checkout: %sgo build -o $(dirname $(command -v"+
			" earth-native))/%s ./cmd/%s",
		guestBinaryName, crossPrefix(), guestBinaryName, guestBinaryName)
}

// crossPrefix is what a `go build` of the guest needs in front of it here.
//
// The guest runs *inside* the sandbox, which is Linux whatever this machine is.
// On darwin the advice above omitted that, and following it produced a Mach-O
// binary the VM rejected with `Exec format error` - naming neither the cause nor
// the fix. **Advice that cannot be followed successfully is worse than none,
// because it is followed** (E490).
//
// `CGO_ENABLED=0` as well, and not as belt and braces: a cross-build with cgo on
// fails to compile against the host SDK, which is the very next thing that
// happens to whoever follows this.
func crossPrefix() string {
	if runtime.GOOS == "linux" {
		return ""
	}

	return "CGO_ENABLED=0 GOOS=linux GOARCH=" + runtime.GOARCH + " "
}

// findGuestCommand locates the agent and the arguments that select it, for a
// sandbox that runs the host's own kind of binary.
//
// **Not for the Apple backend**, which needs a linux agent for its VM while the
// process asking is a darwin one - there, a separate binary is the only answer
// and [findGuestBinary] is what to call.
func findGuestCommand() (string, []string, error) {
	bin, err := findGuestBinary()
	if err == nil {
		return bin, nil, nil
	}

	// **This binary is the agent as well.** `earth guestd ...` runs it, so a CLI
	// that travelled somewhere on its own - copied into a step, which is what a
	// nested build does - has the agent with it and needs no second file. Tried
	// second, because an operator who put a separate one beside us meant it.
	exe, exeErr := os.Executable()
	if exeErr != nil {
		return "", nil, err
	}

	return exe, []string{guestd.Command}, nil
}
