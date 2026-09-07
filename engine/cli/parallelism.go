package cli

import "strconv"

// EnvParallelism bounds how many steps a build runs at once.
//
// Unset is one per core, which is what the scheduler does with an unset field
// and what every build did before this existed.
//
// **A serial build is a diagnostic instrument.** `Scheduler.Parallelism` has
// always been there and nothing set it, so a build that stops with eight steps
// in flight could not be run one step at a time to find out whether the
// concurrency was the cause.
const EnvParallelism = "EARTH_PARALLELISM"

// parallelismFrom reads the limit, or zero for the default.
//
// A value that is not a positive number is the default rather than an error: it
// bounds how fast the build goes and nothing about what it produces, so a typo
// should not stop a build that would otherwise have run.
func parallelismFrom(look func(string) string) int {
	n, err := strconv.Atoi(look(EnvParallelism))
	if err != nil || n <= 0 {
		return 0
	}

	return n
}
