package fleet_test

import (
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// A worker never gives up before the driver has stopped looking.
//
// **The two halves of one agreement, which used to be unrelated.** A driver
// waits `EARTH_FLEET_WAIT` for workers; a worker waited a constant two minutes
// for the driver. Nothing tied them together, and the driver is systematically
// the slower side to appear - it builds the engine and the guest before it can
// listen - so the side with the shorter fuse was reliably the one waiting.
//
// Measured on this branch before the fix: 8 fleet-e2e failures in 40 runs,
// seven of them "both workers did not join". In one the worker gave up four
// minutes before the driver began listening.
func TestAWorkerOutwaitsItsDriver(t *testing.T) {
	for _, c := range []struct {
		name, wait string
		want       time.Duration
	}{
		{"nothing configured, so a LAN fleet is unchanged", "", fleet.DefaultPatience},
		{"the driver's own wait, when it is longer", "8m", 8 * time.Minute},
		{"a shorter wait never shortens ours", "30s", fleet.DefaultPatience},
		{"nor does an unreadable one", "not-a-duration", fleet.DefaultPatience},
		{"nor a nonsensical one", "0s", fleet.DefaultPatience},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(fleet.EnvWait, c.wait)

			if got := fleet.Patience(); got != c.want {
				t.Errorf("with %s=%q a worker waits %v, want %v\n  a worker that"+
					" gives up first is a fleet that never forms",
					fleet.EnvWait, c.wait, got, c.want)
			}
		})
	}
}
