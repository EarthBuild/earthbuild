package exec

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"

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
//  2. beside the running executable - and beside what it points at, if it is a
//     symlink - which is how the Apple backend gets a linux agent for its VM.
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

	if exe, err := os.Executable(); err == nil {
		for _, dir := range besideExecutable(exe) {
			beside := filepath.Join(dir, guestBinaryName)
			if _, statErr := os.Stat(beside); statErr == nil {
				return beside, nil
			}
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
	return guestCommandGiven(selfIsGuest.Load())
}

// guestCommandGiven is [findGuestCommand] with the declaration passed in.
//
// Split out so a test can ask both questions without writing to the package
// variable: every sandbox test in this package calls the lookup, they run in
// parallel with each other, and a test that flipped the flag would hand them
// its own binary as the agent - which is the failure this whole arrangement
// exists to have caught once.
func guestCommandGiven(selfServes bool) (string, []string, error) {
	bin, err := findGuestBinary()
	if err == nil {
		return bin, nil, nil
	}

	// **This binary is the agent as well - if it says so.** `earth guestd ...`
	// runs the agent, so a CLI that travelled somewhere on its own - copied
	// into a step, which is what a nested build does - has the agent with it
	// and needs no second file. Tried second, because an operator who put a
	// separate one beside us meant it.
	//
	// Declared rather than assumed. The first version took `os.Executable()`
	// and appended the subcommand, which is right for the CLI and wrong for
	// every other process that links this package: a test binary is not the
	// CLI, has no `guestd` subcommand, and answered the lookup with itself -
	// so three sandbox tests launched `exec.test guestd` and hung until their
	// deadline. Only a main that dispatches the subcommand can know it does.
	if !selfServes {
		return "", nil, err
	}

	exe, exeErr := os.Executable()
	if exeErr != nil {
		return "", nil, err
	}

	return exe, []string{guestd.Command}, nil
}

// selfIsGuest records that this executable dispatches [guestd.Command].
var selfIsGuest atomic.Bool

// SelfServesAsGuest declares that this binary runs the sandbox agent when it is
// given [guestd.Command], which lets the engine use it instead of looking for a
// separate file.
//
// Called by the mains that dispatch it. Anything that does not call it gets the
// old behaviour, which is what a test binary and an embedding program want.
func SelfServesAsGuest() { selfIsGuest.Store(true) }

// besideExecutable is where to look for something shipped with this binary.
//
// **Two places, because a symlinked install is the ordinary one.** `ln -s
// ~/src/build/earth ~/bin/arth` puts one build on PATH without copying it, so a
// rebuild is live immediately - and `os.Executable()` on darwin answers with the
// *link*, not what it points at (it reads the path the process was started with;
// only Linux's `/proc/self/exe` is already resolved). Looking only there finds no
// agent, and the diagnosis then tells you to put the file somewhere it already
// is.
//
// The link's own directory stays a candidate and comes first: an agent dropped
// beside the link is as deliberate as one built beside the binary, and a
// packaged installation has no symlink at all.
//
// Deduplicated, so an ordinary binary is not stated twice - a diagnosis that
// prints one path twice reads as a bug in the tool rather than in the setup.
func besideExecutable(exe string) []string {
	paths := []string{exe}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		paths = append(paths, resolved)
	}

	var (
		dirs []string
		seen = map[string]bool{}
	)

	for _, p := range paths {
		at := filepath.Dir(p)

		// **Compared resolved, returned as written.** On macOS `/var` is itself
		// a symlink to `/private/var`, so an ordinary binary under a temporary
		// directory yields two spellings of one place - and a list that stats
		// the same directory twice prints the same path twice when it fails,
		// which reads as a bug in the tool rather than in the setup.
		key := at
		if real, err := filepath.EvalSymlinks(at); err == nil {
			key = real
		}

		if !seen[key] {
			seen[key] = true

			dirs = append(dirs, at)
		}
	}

	return dirs
}
