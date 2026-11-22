package image

import (
	"fmt"
	"sync"
	"time"
)

// Ledger is how far each blob of a fetch has got, kept where the fetch is.
//
// **The file marker was the whole of why streaming did not pay.** A guest
// unpacking a blob as it arrives has to know how far the writer has reached,
// and asking the shared filesystem gave an answer about 460ms old - so it spent
// the fetch waiting rather than unpacking, and the head start and the waiting
// cancelled exactly (E688).
//
// Kept in memory on the host and answered over the socket the guest already
// has, there is no filesystem in the path. The wait is a condition variable
// rather than a poll, so an answer costs a wakeup rather than a round trip
// across a mount.
//
// Keyed by the blob's file name rather than its path: the host and the guest
// see the same file at different paths, and the name is the one thing they
// agree on.
type Ledger struct {
	mu   sync.Mutex
	cond *sync.Cond
	at   map[string]int64
	bad  map[string]error
}

// NewLedger is an empty ledger, ready for a fetch to report into.
func NewLedger() *Ledger {
	l := &Ledger{at: map[string]int64{}, bad: map[string]error{}}
	l.cond = sync.NewCond(&l.mu)

	return l
}

// Set records how many of a blob's bytes are on disk.
//
// Monotonic: a lower figure than the last is dropped rather than recorded. A
// reader that saw the higher one has already read that far, and telling it the
// blob shrank would send it backwards over bytes it has consumed.
func (l *Ledger) Set(blob string, n int64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if n <= l.at[blob] {
		return
	}

	l.at[blob] = n

	// Everyone, not one: several layers wait on the same ledger and a wakeup
	// for the wrong one would leave the right one asleep.
	l.cond.Broadcast()
}

// Fail records that a blob will get no further, and why.
func (l *Ledger) Fail(blob string, cause error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.bad[blob] == nil {
		l.bad[blob] = cause
	}

	l.cond.Broadcast()
}

// Await blocks until a blob has more than `have` bytes, or will not.
//
// Three outcomes and no fourth: it grew, the fetch gave up and said why, or
// nothing happened for `patience`. **A reader with no deadline is a build that
// hangs with nothing to say**, which this engine has produced before and taken
// some trouble to diagnose (E673).
func (l *Ledger) Await(blob string, have int64, patience time.Duration) (int64, error) {
	deadline := time.Now().Add(patience)

	// A waker, because sync.Cond cannot wait with a timeout. One timer for the
	// whole wait rather than one per turn round the loop.
	stop := time.AfterFunc(patience, func() {
		l.mu.Lock()
		defer l.mu.Unlock()

		l.cond.Broadcast()
	})

	defer stop.Stop()

	l.mu.Lock()
	defer l.mu.Unlock()

	for {
		err := l.bad[blob]
		if err != nil {
			return 0, fmt.Errorf("the fetch of %s failed: %w", blob, err)
		}

		if n := l.at[blob]; n > have {
			return n, nil
		}

		if time.Now().After(deadline) {
			return 0, fmt.Errorf("waited %s for %s to pass %d bytes and it did"+
				" not; the fetch has neither progressed nor reported a failure",
				patience, blob, have)
		}

		l.cond.Wait()
	}
}
