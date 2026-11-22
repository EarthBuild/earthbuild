package fleet

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tmc/go-iroh/iroh"
)

// A driver that cannot be found yet is not a driver that is not there.
//
// Discovery is not instant: an endpoint's address is empty when it binds, it
// gains a relay a second or two later, publishes then, and the record takes
// seconds more to be resolvable. A worker that dials once on startup loses that
// race nearly every time and reports the driver as unreachable - which is what
// E505 looked like from the outside long after publication had been fixed.
func TestJoiningWaitsOutADriverThatIsNotFindableYet(t *testing.T) {
	t.Parallel()

	tries := 0
	join := func(context.Context) error {
		tries++
		if tries < 3 {
			return fmt.Errorf("dial the driver: %w", iroh.ErrNoAddress)
		}

		return nil
	}

	err := KeepJoining(context.Background(), time.Second, time.Millisecond, join, nil)
	if err != nil {
		t.Fatalf("joined on the third try and reported: %v", err)
	}

	if tries != 3 {
		t.Errorf("%d attempt(s), want 3", tries)
	}
}

// A reason that will not improve with time is reported at once.
//
// The wrong secret, a refused platform, a worker with no room: waiting changes
// none of them, and a worker that sat in a retry loop for a minute before saying
// so would hide the one message that names the mistake.
func TestJoiningDoesNotWaitOutARealRefusal(t *testing.T) {
	t.Parallel()

	wrong := errors.New("the secret does not match")
	tries := 0
	join := func(context.Context) error {
		tries++

		return wrong
	}

	err := KeepJoining(context.Background(), time.Minute, time.Millisecond, join, nil)
	if !errors.Is(err, wrong) {
		t.Fatalf("reported %v, want the refusal itself", err)
	}

	if tries != 1 {
		t.Errorf("%d attempt(s), want 1: a refusal is not a race", tries)
	}
}

// Patience runs out, and says what it was waiting for.
func TestJoiningGivesUpOnADriverThatNeverAppears(t *testing.T) {
	t.Parallel()

	tries := 0
	join := func(context.Context) error {
		tries++

		return fmt.Errorf("dial the driver: %w", iroh.ErrNoAddress)
	}

	err := KeepJoining(context.Background(), 50*time.Millisecond, time.Millisecond, join, nil)
	if !errors.Is(err, iroh.ErrNoAddress) {
		t.Fatalf("gave up with %v, want the reason it kept failing", err)
	}

	if tries < 2 {
		t.Errorf("%d attempt(s): patience of 50ms should outlast more than one", tries)
	}
}

// A cancelled worker stops trying.
func TestJoiningStopsWhenTheWorkerIsCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	join := func(context.Context) error {
		return fmt.Errorf("dial the driver: %w", iroh.ErrNoAddress)
	}

	err := KeepJoining(ctx, time.Minute, time.Millisecond, join, nil)
	if err == nil {
		t.Fatalf("a cancelled worker reported success")
	}
}
