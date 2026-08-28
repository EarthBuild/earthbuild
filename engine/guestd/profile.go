package guestd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
)

// EnvProfile writes profiles of the guest's own work to a directory when the
// build ends.
//
// **Because everything outside the guest has been eliminated and the answer is
// not there.** A wide build ceilings near 175 steps a second, and the host
// spends that time in `__psynch_cvwait` - it is waiting, not working. Mounts
// are free (200 bind mounts in 1ms), dentry relief never fires, and eight times
// the vCPUs buys 19%, so the cost is inside the sandbox and none of the
// hypotheses reachable from outside it survived (E812, E813).
//
// CPU, mutex contention and blocking are all collected: the shape of the answer
// decides which one names it, and a build that has to be re-run to add the right
// profile is a build measured twice.
//
// Written where the store is, so the host can read it without a second channel,
// and only when asked - a guest that profiles itself unasked is a guest whose
// measurements include the profiler.
const EnvProfile = "EARTH_GUEST_PROFILE"

// profiling starts the profiles the environment asked for and returns the
// function that writes them.
//
// Fractions of 1 rather than the sampled defaults: this runs for the length of
// one build, which is seconds, and a sampled contention profile over that is
// mostly zeroes.
func profiling() func() {
	dir := os.Getenv(EnvProfile)
	if dir == "" {
		return func() {}
	}

	// The path is an operator's own environment variable, and the guest is
	// already the thing that mounts and unmounts arbitrary paths on request -
	// but the linter is right that it is tainted, and saying so beats a bare
	// exception.
	err := os.MkdirAll(filepath.Clean(dir), 0o700)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: profile: %v\n", label(), err)

		return func() {}
	}

	runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(1)

	cpu, err := os.Create(filepath.Join(dir, "cpu.pprof")) //nolint:gosec // an operator's own path
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: profile: %v\n", label(), err)

		return func() {}
	}

	err = pprof.StartCPUProfile(cpu)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: profile: %v\n", label(), err)
		_ = cpu.Close()

		return func() {}
	}

	return func() {
		pprof.StopCPUProfile()
		_ = cpu.Close()

		for _, name := range []string{"mutex", "block", "goroutine"} {
			f, cerr := os.Create(filepath.Join(dir, name+".pprof")) //nolint:gosec // an operator's own path
			if cerr != nil {
				continue
			}

			_ = pprof.Lookup(name).WriteTo(f, 0)
			_ = f.Close()
		}

		fmt.Fprintf(os.Stderr, "%s: profiles written to %s\n", label(), dir)
	}
}
