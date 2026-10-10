package fleet_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// A worker announces what it can emulate, not only what it is.
//
// **Otherwise a heterogeneous fleet cannot share work.** A Mac with Rosetta
// runs linux/amd64 perfectly well and joins as linux/arm64, so every amd64 step
// goes to the one machine that is natively amd64 and the Mac sits idle beside
// it - which is the opposite of what a fleet is for.
//
// Announced at join for the reason the platform is (E503): placement refuses a
// worker that has not declared what it runs, so a worker that waited to be
// asked would never be given a first step to be asked about.
func TestAWorkerAnnouncesWhatItEmulates(t *testing.T) {
	t.Parallel()

	r := &fleet.Rendezvous{}
	r.AddForTest()
	r.NoteForTest(r.Inventory()[0].ID, "", "linux/arm64", 4, "linux/amd64")

	inv := r.Inventory()
	if len(inv) != 1 {
		t.Fatalf("inventory holds %d workers", len(inv))
	}

	w := inv[0]
	if w.Platform.Arch != "arm64" {
		t.Errorf("the worker is %v, and it joined as arm64", w.Platform)
	}

	if len(w.Emulates) != 1 || w.Emulates[0].Arch != "amd64" {
		t.Errorf("the worker emulates %v, and it announced amd64", w.Emulates)
	}
}

// A worker that emulates nothing says so, and is not given a platform it cannot
// run.
//
// The common case, and the one that must not change: most machines register no
// interpreter, and a fleet of them places every step exactly as it always did.
func TestAWorkerThatEmulatesNothingAnnouncesNothing(t *testing.T) {
	t.Parallel()

	r := &fleet.Rendezvous{}
	r.AddForTest()
	r.NoteForTest(r.Inventory()[0].ID, "", "linux/amd64", 4)

	if got := r.Inventory()[0].Emulates; len(got) != 0 {
		t.Errorf("a worker with no interpreters announced %v", got)
	}
}
