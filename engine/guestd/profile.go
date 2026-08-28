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

// EnvProfileMode selects what is collected. `all` adds mutex and block
// profiling, which is expensive enough to change the answer - see profiling.
const EnvProfileMode = "EARTH_GUEST_PROFILE_MODE"

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

	// **Contention profiling is opt-in on top, because it is not free.**
	// `SetBlockProfileRate(1)` records a stack on every blocking event, and a
	// guest doing nothing but blocking on syscalls blocks constantly: the first
	// build measured this way took 15.2s where the same build takes 1.5s. A
	// profile that slows its subject tenfold is measuring the profiler, which is
	// the trap this whole line of work keeps walking into.
	//
	// So `=cpu` (the default) collects only what is nearly free, and `=all` asks
	// for contention as well, on the understanding that the timings alongside it
	// are then worthless.
	if os.Getenv(EnvProfileMode) == "all" {
		runtime.SetMutexProfileFraction(1)
		runtime.SetBlockProfileRate(1)
	}

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

		names := []string{"goroutine"}
		if os.Getenv(EnvProfileMode) == "all" {
			names = append(names, "mutex", "block")
		}

		for _, name := range names {
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
