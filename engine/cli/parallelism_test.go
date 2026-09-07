package cli

import "testing"

// TestParallelismComesFromTheEnvironment.
//
// `Scheduler.Parallelism` has always existed and nothing ever set it, so there
// was no way to ask for a serial build. That matters for more than tuning: a
// build that hangs with several steps in flight cannot be told apart from one
// that hangs anyway until you can run it one step at a time.
//
// Zero means "as many as there are cores", which is what the scheduler already
// does with an unset field - so an unset, empty or unreadable variable changes
// nothing.
func TestParallelismComesFromTheEnvironment(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		set  string
		want int
	}{
		{"", 0},
		{"1", 1},
		{"8", 8},
		// Not a number, or not a sensible one: the build runs as it would have.
		{"lots", 0},
		{"0", 0},
		{"-3", 0},
		{"3.5", 0},
	} {
		if got := parallelismFrom(func(string) string { return c.set }); got != c.want {
			t.Errorf("parallelismFrom(%q) = %d, want %d", c.set, got, c.want)
		}
	}
}
