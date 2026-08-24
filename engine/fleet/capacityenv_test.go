package fleet_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// A worker takes every core unless told otherwise.
//
// The right default for a machine that exists to build, and the wrong one for a
// machine somebody is also using - which is most of the second machines anybody
// has. So it is configurable, and the default stays the whole machine because a
// worker that quietly used half of a dedicated builder would be a puzzle nobody
// thinks to look for.
func TestAWorkerTakesEveryCoreUnlessTold(t *testing.T) {
	t.Parallel()

	got, err := fleet.CapacityFromEnv()
	if err != nil {
		t.Fatalf("%v", err)
	}

	if got != fleet.DefaultCapacity() {
		t.Errorf("an unconfigured worker takes %d of %d core(s)",
			got, fleet.DefaultCapacity())
	}
}

// A number is honoured, and a nonsense one is refused.
//
// Refused rather than clamped to the default: a worker silently ignoring
// `EARTH_FLEET_CAPACITY=eight` would take the whole machine on the one occasion
// somebody was explicitly trying to stop it, which is the failure the setting
// exists to prevent.
func TestACapacityIsHonouredOrRefused(t *testing.T) {
	for _, tc := range []struct {
		set  string
		want int
		bad  bool
	}{
		{set: "1", want: 1},
		{set: "12", want: 12},
		{set: "eight", bad: true},
		{set: "0", bad: true},
		{set: "-4", bad: true},
	} {
		t.Setenv(fleet.EnvCapacity, tc.set)

		got, err := fleet.CapacityFromEnv()

		switch {
		case tc.bad && err == nil:
			t.Errorf("%s=%q was accepted as %d", fleet.EnvCapacity, tc.set, got)

		case tc.bad:
			if !strings.Contains(err.Error(), fleet.EnvCapacity) {
				t.Errorf("%v\n  the message must name the variable to fix", err)
			}

			// And which kind of wrong it was. A typo and a deliberate zero are
			// different mistakes with different fixes, and a number that will
			// not parse reads as zero to anything that clamps - so the two
			// refusals have to be distinguishable or the message is worth
			// nothing.
			_, numeric := strconv.Atoi(tc.set)
			if numeric != nil {
				if !strings.Contains(err.Error(), "not a number") {
					t.Errorf("%q refused with %q, which does not say it is not"+
						" a number", tc.set, err)
				}
			}

		case err != nil:
			t.Errorf("%s=%q: %v", fleet.EnvCapacity, tc.set, err)

		case got != tc.want:
			t.Errorf("%s=%q gave %d, want %d",
				fleet.EnvCapacity, tc.set, got, tc.want)
		}
	}
}

// A capacity of zero is a worker that never starts anything.
//
// Which is a configuration mistake and not a way of pausing a machine: the
// worker would join the fleet, be counted, be placed on, and never answer -
// so the driver would wait out its patience on every step. Refusing at startup
// is the difference between a mistake and a mystery.
func TestACapacityOfZeroIsRefusedByName(t *testing.T) {
	t.Setenv(fleet.EnvCapacity, "0")

	_, err := fleet.CapacityFromEnv()
	if err == nil {
		t.Fatal("a capacity of zero was accepted")
	}

	if !strings.Contains(err.Error(), "never") && !strings.Contains(err.Error(), "nothing") {
		t.Errorf("%v\n  the message should say what a worker with no room"+
			" would do, which is join and then answer nothing", err)
	}
}
