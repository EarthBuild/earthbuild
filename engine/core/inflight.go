package core

import "runtime"

// inFlight is how many steps this build may have running at once.
//
// **The fleet's width, not the driver's.** The limit used to default to the
// invoking machine's `runtime.NumCPU()` and gate every step through one
// semaphore, delegated ones included - so a build across two sixteen-core
// machines ran sixteen steps at a time and both machines sat half idle.
// Measured on 64 steps: 95.90s on one machine against 92.27s on two, four waves
// of sixteen either way. Adding a machine added no concurrency, which is the
// one thing adding a machine is for (E-F1).
//
// Two questions were living in one field. How much work *this* machine takes at
// once is a property of this machine, and the executor that runs it is where
// that belongs; how much the *build* has in flight is a property of the fleet.
//
// `asked` wins whenever it is positive, because it is a diagnostic instrument
// before it is a tuning knob: a serial build is how a hang with eight steps in
// flight is told apart from one that would hang anyway, and a limit the fleet
// could talk its way out of would not be one.
func inFlight(workers []Worker, asked int) int {
	if asked > 0 {
		return asked
	}

	total := 0
	for _, w := range workers {
		total += w.Capacity
	}

	// Nobody said anything, which is every build before a fleet existed and
	// every in-process test. The machine this is running on is the honest
	// answer then.
	if total <= 0 {
		return runtime.NumCPU()
	}

	return total
}
