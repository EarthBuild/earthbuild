package guestd

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"syscall"
	"time"
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

// profileAll is the one value EnvProfileMode reads; anything else is CPU and
// goroutines only.
const profileAll = "all"

// contended reports whether the operator asked for the expensive profiles.
func contended() bool { return os.Getenv(EnvProfileMode) == profileAll }

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
	if contended() {
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

	// **Written on a signal as well as on a return, because the guest is not
	// always allowed to return.** On macOS the connection closes and `Serve`
	// comes back; on Linux the daemon is killed when the build ends, and the
	// first profile taken there was a zero-byte file - the profile had started
	// and nothing ever stopped it. A diagnostic that works on one platform is
	// worse than none, because it is trusted on both.
	dying := make(chan os.Signal, 1)
	signal.Notify(dying, syscall.SIGTERM, syscall.SIGINT)

	var once sync.Once

	write := func() {
		pprof.StopCPUProfile()
		_ = cpu.Close()

		names := []string{"goroutine"}
		if contended() {
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

	go func() {
		<-dying
		once.Do(write)
		os.Exit(0)
	}()

	// **And on a timer, because the guest is usually not allowed to die
	// politely.** A signal handler covers SIGTERM; the daemon is killed
	// outright when a build ends, and a SIGKILL cannot be caught. The snapshot
	// profiles are cheap to re-take and overwrite in place, so what survives is
	// whatever the last tick saw - which for a question about what goroutines
	// are waiting on is the whole of the answer.
	go func() {
		for range time.Tick(500 * time.Millisecond) {
			snapshot(dir)
		}
	}()

	return func() { once.Do(write) }
}

// snapshot writes the profiles that are a picture of now rather than a
// recording of a period, overwriting whatever the last tick left.
func snapshot(dir string) {
	names := []string{"goroutine"}
	if contended() {
		names = append(names, "mutex", "block")
	}

	for _, name := range names {
		f, err := os.Create(filepath.Join(dir, name+".pprof")) //nolint:gosec // an operator's own path
		if err != nil {
			continue
		}

		_ = pprof.Lookup(name).WriteTo(f, 0)
		_ = f.Close()
	}
}
