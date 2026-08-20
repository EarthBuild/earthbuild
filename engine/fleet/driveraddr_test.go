package fleet

import (
	"strings"
	"testing"
)

// The driver announces where a worker should dial it.
//
// A worker derives *who* the driver is from the shared secret and has to be told
// *where*: `EARTH_FLEET_DRIVER` is host:port. The driver's announcement said
//
//	fleet: 37c84ef61b0145f9…, waiting 25s for 2 worker(s)
//
// which is the identity - the one term a worker does not need, because it can
// derive it - and omitted the address, which is the one term it cannot (E502).
//
// Its own failure note then says "check that the workers were given
// `EARTH_FLEET_DRIVER=<this driver's address>`", naming something it has never
// printed. **A diagnostic that refers to a value the tool did not emit is a
// diagnostic nobody can act on.**
func TestTheDriverAnnouncementSaysWhereToDial(t *testing.T) {
	t.Parallel()

	line := announcement("37c84ef61b0145f9", "127.0.0.1:51820", 25, 2)

	if !strings.Contains(line, "127.0.0.1:51820") {
		t.Errorf("the announcement is %q and does not say where to dial", line)
	}

	// The identity stays: a worker does not need it, and a person reading two
	// builds' logs side by side does.
	if !strings.Contains(line, "37c84ef61b0145f9") {
		t.Errorf("the announcement is %q and no longer identifies the driver", line)
	}

	// And it names the variable, so the line can be acted on without going to
	// look for what to set it to.
	if !strings.Contains(line, EnvDriver) {
		t.Errorf("the announcement is %q and does not name %s", line, EnvDriver)
	}
}

// With no address to give, it says so rather than printing an empty one.
//
// The endpoint may not know its address yet, and `EARTH_FLEET_DRIVER=` is worse
// than no advice at all: it looks like something to copy.
func TestTheDriverAnnouncementWithNoAddress(t *testing.T) {
	t.Parallel()

	line := announcement("37c84ef61b0145f9", "", 25, 2)

	if strings.Contains(line, EnvDriver+"=\n") || strings.Contains(line, EnvDriver+"= ") {
		t.Errorf("the announcement offers an empty address: %q", line)
	}

	if !strings.Contains(line, "37c84ef61b0145f9") {
		t.Errorf("the announcement is %q and does not identify the driver", line)
	}
}
