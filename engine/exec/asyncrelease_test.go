package exec

import (
	"runtime"
	"sync"
	"testing"
)

// TestWhenAStepsBaseComesDown.
//
// **What is deferred is when, never whether.** A release moved behind the step's
// answer is still a release, and `Close` waits for it - a build that exited
// leaving mounts up would trade 18.55ms a step for a sandbox that eventually
// cannot mount anything.
func TestWhenAStepsBaseComesDown(t *testing.T) {
	t.Parallel()

	t.Run("in front of the answer unless asked", func(t *testing.T) {
		t.Parallel()

		var (
			r    releaser
			done bool
		)

		r.release(0, func() { done = true })

		if !done {
			t.Error("the base was still mounted when the step answered," +
				"\n  and nothing had been asked to take it down later")
		}
	})

	t.Run("behind the answer when asked, and waited for", func(t *testing.T) {
		t.Parallel()

		var (
			r  releaser
			mu sync.Mutex
			n  int
		)

		for range 16 {
			r.release(4, func() { mu.Lock(); n++; mu.Unlock() })
		}

		r.wait()

		mu.Lock()
		defer mu.Unlock()

		if n != 16 {
			t.Errorf("%d of 16 releases ran before the wait returned"+
				"\n  Close waits so that a build cannot exit with mounts still up", n)
		}
	})
}

// TestHowManyReleasesAreInFlight.
//
// **The kernel bounds this whatever the machine.** Thirty-two overlay unmounts
// take 87ms one at a time and 36ms sixteen at a time - `namespace_sem` is held
// for write through each, so past a handful the releases queue on the kernel
// instead of finishing sooner. Asking for ninety-six on a ninety-six-core
// machine would lengthen the queue and nothing else (E818).
func TestHowManyReleasesAreInFlight(t *testing.T) {
	for _, c := range []struct {
		set  string
		want int
	}{
		{"", 0},
		{"0", 0},
		{"no", 0},
		{"false", 0},
		{"-1", 0},
		{"1", 1},
		{"6", 6},
		{"yes", min(runtime.NumCPU(), 8)},
	} {
		t.Run(c.set, func(t *testing.T) {
			t.Setenv(EnvAsyncRelease, c.set)

			if got := releaseWidth(); got != c.want {
				t.Errorf("%s=%q gives width %d, want %d",
					EnvAsyncRelease, c.set, got, c.want)
			}
		})
	}
}
