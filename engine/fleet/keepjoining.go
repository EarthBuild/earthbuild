package fleet

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/tmc/go-iroh/iroh"
)

// DefaultPatience is how long a worker waits for a driver it cannot yet find.
//
// Generous, because the thing being waited for is a DNS record reaching a
// resolver, and the cost of waiting too long is a worker that idles while the
// cost of not waiting long enough is a fleet that never forms. A worker started
// before its driver is the normal case in CI, where the jobs start together.
const DefaultPatience = 2 * time.Minute

// Patience is how long this worker should wait for a driver it cannot yet find.
//
// **The driver's own willingness to wait, where it has said so.** A worker
// giving up before the driver has stopped looking is a fleet that never forms
// for no reason but arithmetic, and the two numbers were unrelated: a driver
// waits `EARTH_FLEET_WAIT` for workers, a worker waited a constant two minutes
// for the driver, and nothing tied them together.
//
// The asymmetry is not symmetrical in practice either. The driver is
// systematically the slower side to appear - it builds the engine *and* the
// guest before it can listen - so the side with the shorter fuse is reliably
// the one waiting. In CI that cost a fleet roughly one run in five: measured at
// 8 failures in 40 on this branch, seven of them "both workers did not join",
// and in one the worker gave up four minutes before the driver began listening.
//
// Falls back to DefaultPatience when nothing is configured, so a fleet on a LAN
// is unchanged.
func Patience() time.Duration {
	v := os.Getenv(EnvWait)
	if v == "" {
		return DefaultPatience
	}

	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		// Not this function's error to report: the driver parses the same
		// variable and refuses the build with a message naming it. A worker
		// that cannot read it waits the default rather than not at all.
		return DefaultPatience
	}

	if d < DefaultPatience {
		return DefaultPatience
	}

	return d
}

// KeepJoining runs join until it succeeds, fails for a reason that will not
// improve, or patience runs out.
//
// **Not findable yet is not absent.** An endpoint binds with no address at all:
// it gains a relay a moment later, publishes then, and the record takes seconds
// more to become resolvable. A worker that dials once at startup loses that race
// nearly every time, and the failure it reports - `no reachable address for
// endpoint` - is exactly what a driver that does not exist looks like (E505).
//
// Only [iroh.ErrNoAddress] is waited out. A wrong secret does not get better
// with time, and a worker that retried it silently for two minutes would bury
// the one message naming the mistake.
func KeepJoining(
	ctx context.Context, patience, every time.Duration,
	join func(context.Context) error, note func(error),
) error {
	if note == nil {
		note = func(error) {}
	}

	deadline := time.Now().Add(patience)

	var last error

	for attempt := 1; ; attempt++ {
		err := join(ctx)
		if !errors.Is(err, iroh.ErrNoAddress) {
			return err
		}

		last = err

		if ctx.Err() != nil {
			return errors.Join(err, ctx.Err())
		}

		if time.Now().After(deadline) {
			return last
		}

		if attempt == 1 {
			note(errNotYet)
		}

		select {
		case <-ctx.Done():
			return errors.Join(last, ctx.Err())
		case <-time.After(every):
		}
	}
}

// errNotYet is said once, so a worker waiting on discovery looks like it is
// waiting rather than like it has hung.
var errNotYet = errors.New("the driver is not resolvable yet - waiting for it to publish where it is")
