package main

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// A worker can join without being told where the driver is.
//
// It refused to start without `EARTH_FLEET_DRIVER=host:port`, on the reasoning
// that a worker derives *who* the driver is and has to be told *where*. The
// first half is right and the second is not: `netaddr.NewEndpointAddr(id,
// addrs...)` takes addresses variadically, and iroh finds a peer by node id
// through discovery and relays when it has none (E505).
//
// **That refusal is what stopped a fleet forming across GitHub runners**, which
// have no route to each other and no address worth telling anybody. rebuck2 does
// exactly this and has for months: N runners derive the driver's key from
// `$GITHUB_RUN_ID` and find each other with no service, no secrets and no
// addresses.
//
// The address stays as a *hint*. On one machine or one LAN it is the fast path,
// and skipping discovery is worth having when the answer is already known.
func TestAWorkerJoinsWithoutBeingToldWhere(t *testing.T) {
	t.Parallel()

	session := fleet.Session{Session: "s", RunID: "1", Attempt: 1, Repo: "r"}

	id, err := fleet.DriverID(session, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	// No address: the id alone, which iroh resolves.
	at, err := driverAt(id, "")
	if err != nil {
		t.Fatalf("a worker with no address was refused: %v", err)
	}

	if at.ID != id {
		t.Error("the address does not name the driver this worker derived")
	}

	if len(at.Addrs()) != 0 {
		t.Errorf("a worker told nothing invented an address: %v", at.Addrs())
	}

	// An address: kept, because knowing it beats discovering it.
	at, err = driverAt(id, "127.0.0.1:5000")
	if err != nil {
		t.Fatalf("a worker given an address was refused: %v", err)
	}

	if len(at.Addrs()) == 0 {
		t.Error("a worker given an address dropped it, so it will discover" +
			" what it was already told")
	}
}

// An address that is not an address is still refused.
//
// The one thing worse than no address is a typo read as none: a worker that
// quietly fell back to discovery would take longer to fail and never say why.
func TestAWorkerRefusesAnAddressThatIsNotOne(t *testing.T) {
	t.Parallel()

	session := fleet.Session{Session: "s", RunID: "1", Attempt: 1, Repo: "r"}

	id, err := fleet.DriverID(session, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = driverAt(id, "not-an-address")
	if err == nil {
		t.Fatal("a worker accepted something that is not host:port")
	}

	if !strings.Contains(err.Error(), fleet.EnvDriver) {
		t.Errorf("refused with %q, which does not name what to fix", err)
	}
}
