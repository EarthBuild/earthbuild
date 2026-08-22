// Package timing reports where a build's time went, while it is still going.
//
// One switch for the whole engine, host and guest alike: the guest's stderr is
// forwarded, so a phase timed inside the sandbox lands in the same output as one
// timed outside it, and the two can be read as one sequence.
package timing

import (
	"fmt"
	"io"
	"os"
	"time"
)

// Env makes a build say where its time went. Any non-empty value.
const Env = "EARTH_TIMINGS"

// To is where phases are reported, or nil when nobody asked. Read once, because
// a build with a thousand steps asks several thousand times.
var To io.Writer = func() io.Writer {
	if os.Getenv(Env) == "" {
		return nil
	}

	return os.Stderr
}()

// Phase starts timing one phase and returns the function that ends it. Off, that
// function is empty and the clock is never read.
//
// Reported as each phase ends rather than summarised at exit: a build that is
// slow at step 900 of 1000 should not have to finish before it says so.
func Phase(name, where string) func() {
	if To == nil {
		return func() {}
	}

	start := time.Now()

	return func() {
		_, _ = fmt.Fprintf(To, "earth: %-11s %7.3fs  %s\n",
			name, time.Since(start).Seconds(), where)
	}
}
