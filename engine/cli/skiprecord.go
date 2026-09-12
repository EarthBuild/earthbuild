package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// skipRecordVersion is the format. A record written by a different engine is
// refused rather than compared, because a field whose meaning changed is a
// comparison that is wrong rather than one that fails.
const skipRecordVersion = 2

// skipRecordsKept bounds the store, for the reason skipStoreEntries gives: the
// mechanism this stands beside keeps everything it has ever seen, which is a
// file a CI cache then carries for the life of the repository.
const skipRecordsKept = 256

// skipRecord is what a successful build wrote down so a later one can ask whether
// to run at all. See docs-internals/job-skipping.md.
//
// Two answers, and a reader takes the first it can:
//
//   - Shape with Inputs is Κ_job - the build's commands, arguments and base
//     images, together with what it actually read from the checkout. A file the
//     build never opened does not move it;
//   - Plan is the whole plan fingerprint, which every declared input moves. It
//     is what a platform with no read tracing records, and what a build the
//     gates refused records.
type skipRecord struct {
	Version  int    `json:"version"`
	Target   string `json:"target"`
	Platform string `json:"platform,omitempty"`
	// Plan is key A: the plan fingerprint, context content and all.
	Plan string `json:"plan,omitempty"`
	// Shape is the build without what its copied files contain.
	Shape string `json:"shape,omitempty"`
	// Inputs is 𝑅: what the build read, as paths in the checkout.
	Inputs []hostInput `json:"inputs,omitempty"`
	// Key is Κ_job as it stood when this was written: the shape and the inputs
	// above, hashed together.
	//
	// **Stored rather than recomputed from the record, so that losing an input
	// fails closed.** Comparing the inputs one at a time reads as correct and
	// is not: a record that arrived with none - a field dropped in
	// serialisation, a file truncated in a cache - satisfies "every input still
	// holds" vacuously and skips the build. Re-deriving the key over the paths
	// the record names and comparing it to this catches that, because the key
	// over no inputs is not the key over three.
	Key string `json:"key,omitempty"`
	// MustRun says this build contains something no record can stand in for,
	// and names it. Non-empty means neither key answers.
	//
	// **Recorded rather than asked at the ask.** `askAutoSkip` runs before
	// planning - which is the whole point of the flag - so there is no plan in
	// front of it to inspect. A build that never records a skippable answer
	// cannot be skipped however the question arrives, including by a record
	// this engine did not write.
	MustRun string `json:"must_run,omitempty"`
}

// stillHolds asks whether this record describes the checkout in front of it.
//
// **The shape first, and separately.** A record whose every input still holds
// says nothing about a build whose commands changed underneath them, and
// checking the cheap half first means a changed Earthfile costs no file reads at
// all.
//
// A record with no shape cannot answer this question - it was written where
// nothing watched what the build read - and says so rather than guessing. Its
// caller falls back to planHolds.
func (r skipRecord) stillHolds(shape ir.NodeID, root string) bool {
	if r.MustRun != "" {
		return false
	}

	if r.Version != skipRecordVersion || r.Shape == "" || r.Shape != shape.String() {
		return false
	}

	if r.Key == "" {
		return false
	}

	// Re-read every path the record names, as the checkout has it now, and ask
	// whether that is the same build. `holds` short-circuits nothing: the key
	// is over all of them together.
	now := make([]hostInput, 0, len(r.Inputs))

	for _, was := range r.Inputs {
		now = append(now, hostInput{Path: was.Path, Kind: was.Kind, Digest: was.now(root)})
	}

	return jobKey(shape, now) == r.Key
}

// planHolds is the fallback: the whole plan fingerprint, unchanged.
func (r skipRecord) planHolds(plan string) bool {
	return r.MustRun == "" &&
		r.Version == skipRecordVersion && r.Plan != "" && r.Plan == plan
}

// now is what the checkout holds at this input's path today.
//
// A kind this engine does not know is not one it can re-read, and an unchecked
// input must never look unchanged - so it answers with something no digest can
// equal rather than with the digest it was written with.
func (in hostInput) now(root string) string {
	switch in.Kind {
	case inputListing:
		return listingOf(root, in.Path).String()

	case inputEarthfile:
		// Absolute already, and not under the context root: a build may read an
		// Earthfile outside it.
		return earthfileDigest(in.Path).String()

	case inputFile, inputAbsent:
		return sealOf(root, in.Path).String()

	default:
		return "unknown kind " + in.Kind
	}
}

// skipRecordStore is the records this machine has, one per target and platform.
//
// JSON rather than a database: a CI cache carries a file, a reviewer can read
// one, and what it holds is small - a few hundred paths for a target that reads
// a few hundred files.
type skipRecordStore struct {
	at string
	// max caps the records kept, zero meaning skipRecordsKept.
	max int
}

func (s skipRecordStore) cap() int {
	if s.max > 0 {
		return s.max
	}

	return skipRecordsKept
}

// skipStored is the file's shape. A wrapper rather than a bare list so the file can
// gain a field later without every reader of it having to change at once.
type skipStored struct {
	Records []skipRecord `json:"records"`
}

// all is every record the store holds, oldest first.
//
// **Every failure is an empty store**, for the reason the skip store gives:
// absent, corrupt, half-written and written-by-another-engine all have to read
// as "nothing recorded", because the alternative is a flag that turns a damaged
// cache file into a damaged build.
func (s skipRecordStore) all() []skipRecord {
	if s.at == "" {
		return nil
	}

	b, err := os.ReadFile(s.at)
	if err != nil {
		return nil
	}

	var held skipStored

	err = json.Unmarshal(b, &held)
	if err != nil {
		return nil
	}

	return held.Records
}

// get is the record for one target on one platform.
func (s skipRecordStore) get(target, platform string) (skipRecord, bool) {
	for _, r := range s.all() {
		if r.Target == target && r.Platform == platform && r.Version == skipRecordVersion {
			return r, true
		}
	}

	return skipRecord{}, false
}

// put writes a record down, replacing whatever this target and platform had.
//
// Best effort: a record that cannot be written costs the next build a build.
// Rewritten whole and renamed into place, so a store that is half a file never
// exists and a torn write is not a record that half-describes something.
func (s skipRecordStore) put(r skipRecord) {
	if s.at == "" || r.Target == "" {
		return
	}

	kept := make([]skipRecord, 0, s.cap())

	for _, held := range s.all() {
		if held.Target != r.Target || held.Platform != r.Platform {
			kept = append(kept, held)
		}
	}

	kept = append(kept, r)

	// Newest last, so the oldest go first when there are too many.
	if len(kept) > s.cap() {
		kept = kept[len(kept)-s.cap():]
	}

	// Sorted for a stable file: a store whose lines move about on every build
	// is one nobody can diff, and it would defeat a content-addressed cache key
	// over the file itself.
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Target != kept[j].Target {
			return kept[i].Target < kept[j].Target
		}

		return kept[i].Platform < kept[j].Platform
	})

	b, err := json.MarshalIndent(skipStored{Records: kept}, "", "  ")
	if err != nil {
		return
	}

	err = os.MkdirAll(filepath.Dir(s.at), 0o750)
	if err != nil {
		return
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.at), ".records-*")
	if err != nil {
		return
	}

	_, err = tmp.Write(append(b, '\n'))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		_ = os.Remove(tmp.Name())

		return
	}

	err = os.Rename(tmp.Name(), s.at)
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
}
