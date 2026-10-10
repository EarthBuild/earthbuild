package guest

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// One handle's filesystem work happens one at a time; different handles do not
// wait for each other.
//
// The server handles requests concurrently on purpose - a slow materialise must
// not hold up an exec - and for two *different* handles that is right. For one
// handle it buys nothing: both copies write the same filesystem. It costs
// something, though. The copy clears a symlink at a directory it is about to
// create, and the argument for that being sound is that the walk is top-down -
// which covers a link planted *before* the copy and not one planted by a second
// copy at the same moment. gosec's G122 names that shape; E162 found the same
// one in the mount preparation.
//
// This engine never issues two copies against one handle - `engine/exec` sends a
// step's copies in order and each step has its own handle - so this is a hole
// kept shut rather than one being closed. A protocol's guarantees should not
// rest on the habits of the client that ships with it.
//
// The mechanism is what is tested, because it is what was written. A test
// cannot watch a lock being held from outside the copy, so the copy's own
// serialisation is asserted here and argued there.
func TestOneHandlesWorkIsSerialisedAndOthersAreNot(t *testing.T) {
	t.Parallel()

	t.Run("one handle", func(t *testing.T) {
		t.Parallel()

		s := &Server{}

		var (
			inside atomic.Int32
			peak   atomic.Int32
			wg     sync.WaitGroup
		)

		// A barrier, so all eight are trying at once, and a section long enough
		// to overlap in. Without both, the first version of this test passed
		// with the lock removed: eight goroutines doing three atomic operations
		// each will happily run one after another, and a concurrency test whose
		// critical section is too short to collide is not testing anything.
		var start sync.WaitGroup

		start.Add(1)

		for range 8 {
			wg.Go(func() {
				start.Wait()

				unlock := s.lockHandle("h1")
				defer unlock()

				n := inside.Add(1)

				time.Sleep(2 * time.Millisecond)
				for {
					was := peak.Load()
					if n <= was || peak.CompareAndSwap(was, n) {
						break
					}
				}

				inside.Add(-1)
			})
		}

		start.Done()
		wg.Wait()

		if got := peak.Load(); got != 1 {
			t.Errorf("%d holders of one handle at once; the copy's symlink check"+
				" is only sound while that is 1", got)
		}
	})

	t.Run("different handles", func(t *testing.T) {
		t.Parallel()

		s := &Server{}

		// Each waits for the other to have taken its own lock. If the server
		// serialised across handles this deadlocks rather than failing, which
		// is why the test carries its own timeout instead of an assertion.
		var first, second sync.WaitGroup

		first.Add(1)
		second.Add(1)

		done := make(chan struct{})

		go func() {
			unlock := s.lockHandle("a")
			defer unlock()

			first.Done()
			second.Wait()

			close(done)
		}()

		unlock := s.lockHandle("b")
		defer unlock()

		first.Wait()
		second.Done()

		<-done
	})
}
