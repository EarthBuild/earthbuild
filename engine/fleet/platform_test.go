package fleet_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

var amd64 = ir.Platform{OS: "linux", Arch: "amd64"}

// A worker says what it is, in every reply.
//
// Placement refuses a worker whose platform it does not know (E267), so a fleet
// that never announced itself would be a fleet that never receives a step - and
// the build would look local while the machines idled. The driver cannot derive
// this: a worker is the only party that knows what it can execute.
func TestAWorkerSaysWhatItIs(t *testing.T) {
	t.Parallel()

	run := fleet.Runner(&countingLocal{}, core.Worker{ID: "w1", Platform: amd64})

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.Platform != amd64.String() {
		t.Errorf("the reply says %q; a worker that does not say what it is"+
			" receives nothing", reply.Platform)
	}
}

// A worker refuses a step for a machine it is not.
//
// The safety net under placement rather than a substitute for it. The driver
// should not send an amd64 step to an arm64 worker, and if the two ever disagree
// - a stale inventory, a worker that was replaced - the worker is the party that
// knows, and refusing lets the driver run it somewhere that can (I10, I11).
//
// Building it anyway would succeed and produce binaries for the wrong machine,
// which is the failure that has no symptom until somebody runs them.
func TestAWorkerRefusesAStepForAMachineItIsNot(t *testing.T) {
	t.Parallel()

	local := &countingLocal{}

	run := fleet.Runner(local, core.Worker{ID: "w1", Platform: amd64})

	reply, err := run(t.Context(), fleet.Assignment{
		Version:  fleet.Version,
		Op:       fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Platform: "linux/arm64",
	})
	if err != nil {
		t.Fatalf("a platform mismatch must be a refusal, not a failure: %v", err)
	}

	if reply.Refused == "" {
		t.Fatal("an amd64 worker built a step for arm64")
	}

	if !strings.Contains(reply.Refused, "arm64") ||
		!strings.Contains(reply.Refused, "amd64") {
		t.Errorf("refused with %q, which does not say which two disagreed",
			reply.Refused)
	}

	if local.runs != 0 {
		t.Error("the step ran anyway")
	}
}

// A worker that does not know its own platform runs what it is given.
//
// Every in-process fleet is like this, and so is a single-machine build before
// anybody configures one. A worker with nothing to compare against cannot
// detect a mismatch, and refusing on that basis would refuse every such build.
func TestAWorkerWithNoPlatformOfItsOwnStillRuns(t *testing.T) {
	t.Parallel()

	local := &countingLocal{}

	run := fleet.Runner(local, core.Worker{ID: "w1"})

	reply, err := run(t.Context(), fleet.Assignment{
		Version:  fleet.Version,
		Op:       fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Platform: "linux/arm64",
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.Refused != "" {
		t.Errorf("refused: %s", reply.Refused)
	}

	if local.runs != 1 {
		t.Error("the step did not run")
	}
}

// The inventory carries what each worker said it was.
//
// Placement chooses among the workers it is given, and it now refuses any whose
// platform it does not know. An inventory that dropped the announcement would
// therefore turn a working fleet into an idle one - silently, because a build
// with no eligible worker simply runs everything locally.
func TestTheInventoryCarriesEachWorkersPlatform(t *testing.T) {
	t.Parallel()

	r := &fleet.Rendezvous{}
	r.AddForTest()
	r.NoteForTest("fleet-0", "", amd64.String(), 4)

	got := r.Inventory()
	if len(got) != 1 {
		t.Fatalf("inventory of %d, want 1", len(got))
	}

	if got[0].Platform != amd64 {
		t.Errorf("the inventory says %v; placement refuses a worker whose"+
			" platform it does not know, so this fleet would receive nothing",
			got[0].Platform)
	}

	if got[0].IsInvoker {
		t.Error("a fleet worker is marked as the invoker; host steps would" +
			" be sent to another machine's filesystem")
	}
}
