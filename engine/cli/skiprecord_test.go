package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func aShape(b byte) ir.NodeID { return ir.NodeID{'s', 'h', b} }

// recorded builds the record a successful build would write for a checkout
// where `src` was placed at /w/src and the named paths were read.
func recorded(t *testing.T, root string, shape ir.NodeID, reads ...string) skipRecord {
	t.Helper()

	in, err := hostInputsFrom(map[string]bool{contextLayer: true}, placedAt(), read(reads...), root)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	return skipRecord{
		Version: skipRecordVersion, Target: "build", Platform: "linux/arm64",
		Shape: shape.String(), Inputs: in, Key: jobKey(shape, in),
	}
}

// **The joy case, through the record rather than the derivation.** A build was
// recorded; a file it never opened changed; the record still holds and the job
// does not run.
func TestARecordSurvivesAFileNothingRead(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/read.txt": "one", "src/README.md": "one"})
	rec := recorded(t, root, aShape('a'), "/w/src/read.txt")

	err := os.WriteFile(filepath.Join(root, "src/README.md"), []byte("two"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if !rec.stillHolds(aShape('a'), root) {
		t.Error("a file nothing read changed and the record stopped holding")
	}
}

// And it stops holding when a file the build read changes.
func TestARecordFailsWhenAReadFileChanges(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/read.txt": "one"})
	rec := recorded(t, root, aShape('a'), "/w/src/read.txt")

	err := os.WriteFile(filepath.Join(root, "src/read.txt"), []byte("two"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if rec.stillHolds(aShape('a'), root) {
		t.Error("a file the build read changed and the record still held")
	}
}

// **The shape is checked too, and first.** A record whose inputs all still hold
// says nothing about a build whose commands have changed underneath them.
func TestARecordDoesNotHoldForADifferentShape(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/read.txt": "one"})
	rec := recorded(t, root, aShape('a'), "/w/src/read.txt")

	if rec.stillHolds(aShape('b'), root) {
		t.Error("a record held for a build with a different shape")
	}
}

// A record with no observations behind it falls back to the plan fingerprint,
// which is what a platform with no tracer produces.
func TestARecordWithNoInputsComparesThePlanFingerprint(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rec := skipRecord{Version: skipRecordVersion, Target: "build", Plan: "a-plan-fingerprint"}

	if !rec.planHolds("a-plan-fingerprint") {
		t.Error("an unchanged plan fingerprint did not hold")
	}

	if rec.planHolds("another") {
		t.Error("a changed plan fingerprint held")
	}

	if rec.stillHolds(aShape('a'), root) {
		t.Error("a record with no shape held against one")
	}
}

// A record written comes back, keyed by the target and platform it is about:
// two targets in one Earthfile do not answer for each other.
func TestARecordStoreIsKeyedByTargetAndPlatform(t *testing.T) {
	t.Parallel()

	at := filepath.Join(t.TempDir(), "records")
	s := skipRecordStore{at: at}

	s.put(skipRecord{Version: skipRecordVersion, Target: "build", Platform: "linux/arm64", Plan: "one"})
	s.put(skipRecord{Version: skipRecordVersion, Target: "test", Platform: "linux/arm64", Plan: "two"})
	s.put(skipRecord{Version: skipRecordVersion, Target: "build", Platform: "linux/amd64", Plan: "three"})

	for _, one := range []struct{ target, platform, want string }{
		{"build", "linux/arm64", "one"},
		{"test", "linux/arm64", "two"},
		{"build", "linux/amd64", "three"},
	} {
		got, ok := s.get(one.target, one.platform)
		if !ok || got.Plan != one.want {
			t.Errorf("%s on %s gave %q (found %t), want %q",
				one.target, one.platform, got.Plan, ok, one.want)
		}
	}

	if _, ok := s.get("nothing", "linux/arm64"); ok {
		t.Error("a target nobody recorded was found")
	}
}

// Writing a target twice replaces its record rather than accumulating.
func TestRecordingATargetTwiceKeepsTheLatest(t *testing.T) {
	t.Parallel()

	at := filepath.Join(t.TempDir(), "records")
	s := skipRecordStore{at: at}

	s.put(skipRecord{Version: skipRecordVersion, Target: "build", Plan: "first"})
	s.put(skipRecord{Version: skipRecordVersion, Target: "build", Plan: "second"})

	got, ok := s.get("build", "")
	if !ok || got.Plan != "second" {
		t.Errorf("the store holds %q, want the later record", got.Plan)
	}

	if n := len(s.all()); n != 1 {
		t.Errorf("the store holds %d records for one target", n)
	}
}

// **A store that cannot be read is a build.** Absent, corrupt, or written by a
// different engine: each has to read as "nothing recorded", because the
// alternative is a flag that turns a damaged cache file into a damaged build.
func TestAnUnusableRecordStoreIsNotAnError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	at := filepath.Join(dir, "corrupt")
	err := os.WriteFile(at, []byte("{not json"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	for what, s := range map[string]skipRecordStore{
		"absent":                             {at: filepath.Join(dir, "absent")},
		"corrupt":                            {at: at},
		"a directory where a file should be": {at: dir},
	} {
		if _, ok := s.get("build", ""); ok {
			t.Errorf("%s: a record was found", what)
		}

		// And writing into it must not panic.
		s.put(skipRecord{Version: skipRecordVersion, Target: "build"})
	}
}

// A record from a different format is refused rather than read as this one.
func TestARecordFromAnotherFormatIsRefused(t *testing.T) {
	t.Parallel()

	at := filepath.Join(t.TempDir(), "records")
	s := skipRecordStore{at: at}

	s.put(skipRecord{Version: skipRecordVersion + 1, Target: "build", Plan: "one"})

	if _, ok := s.get("build", ""); ok {
		t.Error("a record written by a different engine was used")
	}
}

// Placements and observations together are what a build records, and a build
// the gates refuse produces no record at all rather than a weak one.
func TestAGatedBuildRecordsNoInputs(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})

	obs := read("/w/src/a.txt")
	obs.Incomplete = true

	_, err := hostInputsFrom(map[string]bool{contextLayer: true},
		[]core.Placement{{Layer: contextLayer, From: "src", To: "/w/src"}}, obs, root)
	if err == nil {
		t.Error("an incomplete observation produced inputs")
	}
}

// **Written, read back, and still holding.**
//
// Everything above tests a record in memory. This is the one that matters in
// CI, where the record is a file that was serialised by one process, carried
// through a cache, and parsed by another - and where a field quietly lost on the
// way through JSON shows up not as an error but as a build that behaves
// differently from the one that wrote it.
//
// Both directions, because only one of them is dangerous. A record that fails to
// hold after a round trip costs a rebuild; one that holds when it should not is
// the green tick on a build nobody ran.
func TestARecordSurvivesBeingWrittenAndReadBack(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		"src/read.txt": "one", "src/README.md": "one", "src/dir/x": "one",
	})

	obs := read("/w/src/read.txt")
	obs.Listings["/w/src/dir"] = ir.NodeID{}
	obs.Negative = []string{"/w/src/build.rs"}

	in, err := hostInputsFrom(map[string]bool{contextLayer: true}, placedAt(), obs, root)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	at := filepath.Join(t.TempDir(), "records")
	s := skipRecordStore{at: at}

	s.put(skipRecord{
		Version: skipRecordVersion, Target: "build", Platform: "linux/arm64",
		Shape: aShape('a').String(), Inputs: in, Key: jobKey(aShape('a'), in),
	})

	got, ok := s.get("build", "linux/arm64")
	if !ok {
		t.Fatal("the record that was just written could not be read")
	}

	if len(got.Inputs) != len(in) {
		t.Fatalf("wrote %d inputs and read %d back", len(in), len(got.Inputs))
	}

	for i := range in {
		if got.Inputs[i] != in[i] {
			t.Errorf("input %d came back as %+v, wrote %+v", i, got.Inputs[i], in[i])
		}
	}

	if !got.stillHolds(aShape('a'), root) {
		t.Fatal("a record written and read back does not hold against the checkout it describes")
	}

	// And the read-back record is still load-bearing: each of the three kinds
	// of entry it carries must still move it. Each case gets its own checkout,
	// so they are independent of one another and of the order they run in.
	for what, change := range map[string]func(string){
		"a file that was read": func(at string) {
			_ = os.WriteFile(filepath.Join(at, "src/read.txt"), []byte("two"), 0o600)
		},
		"a file appearing where it listed": func(at string) {
			_ = os.WriteFile(filepath.Join(at, "src/dir/y"), []byte("new"), 0o600)
		},
		"a file it found absent": func(at string) {
			_ = os.WriteFile(filepath.Join(at, "src/build.rs"), []byte("new"), 0o600)
		},
	} {
		t.Run(what, func(t *testing.T) {
			t.Parallel()

			fresh := tree(t, map[string]string{
				"src/read.txt": "one", "src/README.md": "one", "src/dir/x": "one",
			})

			its, deriveErr := hostInputsFrom(
				map[string]bool{contextLayer: true}, placedAt(), obs, fresh)
			if deriveErr != nil {
				t.Fatal(deriveErr)
			}

			own := recordStoreAt(t)
			own.put(skipRecord{
				Version: skipRecordVersion, Target: "build", Platform: "linux/arm64",
				Shape: aShape('a').String(), Inputs: its, Key: jobKey(aShape('a'), its),
			})

			back, found := own.get("build", "linux/arm64")
			if !found {
				t.Fatal("the record vanished")
			}

			change(fresh)

			if back.stillHolds(aShape('a'), fresh) {
				t.Errorf("%s changed and the read-back record still held", what)
			}
		})
	}
}

// recordStoreAt is a store in a directory of this test's own.
func recordStoreAt(t *testing.T) skipRecordStore {
	t.Helper()

	return skipRecordStore{at: filepath.Join(t.TempDir(), "records")}
}

// **A record that lost its inputs must not hold.**
//
// The dangerous shape of a serialisation bug: comparing inputs one at a time,
// a record that arrived with none satisfies "every input still holds" for the
// same reason an empty conjunction is true - and skips the build. The key is
// stored so that the comparison is over all of them together, where losing
// three of three is a difference rather than a vacuum.
func TestARecordThatLostItsInputsDoesNotHold(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/read.txt": "one"})
	rec := recorded(t, root, aShape('a'), "/w/src/read.txt")

	if !rec.stillHolds(aShape('a'), root) {
		t.Fatal("the record does not hold against the checkout it describes")
	}

	lost := rec
	lost.Inputs = nil

	if lost.stillHolds(aShape('a'), root) {
		t.Error("a record with its inputs lost held anyway")
	}

	// And one that never had a key - written by something that did not compute
	// it - is refused rather than compared on the inputs alone.
	keyless := rec
	keyless.Key = ""

	if keyless.stillHolds(aShape('a'), root) {
		t.Error("a record with no key held")
	}
}
