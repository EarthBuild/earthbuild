package cli

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// storedStep is a step record on disk.
//
// A purpose-built form rather than tags on core.StepRecord, and the reason is
// what it leaves out. `Observation` is a map of every path the step read, which
// is unbounded, and B.5's file-level report needs it - but S5, the observation
// source, is simulated, so nothing populates it and nothing can use it. Writing
// an empty map to every record to support a report that cannot run would be
// storage spent on a promise.
//
// What is here is exactly what `Diverge` reads (green paper B.4) plus what the
// report names the step by. When S5 lands, this grows a field and the omission
// stops being correct - which is why it is stated here rather than implied by
// the absence.
type storedStep struct {
	Ident string `json:"ident"`
	Node  string `json:"node"`
	// The component digests, which are what turns "this step reran" into a
	// reason: base, command, environment, platform.
	Base  string `json:"base"`
	Op    string `json:"op"`
	Env   string `json:"env"`
	Plat  string `json:"plat"`
	Layer string `json:"layer"`
	// Where the step came from, so a divergence is attributed to a line rather
	// than to twelve characters of hash.
	Source      string `json:"source,omitempty"`
	Description string `json:"description,omitempty"`
}

type storedRecord struct {
	// Version so a future format change is a miss rather than a
	// misinterpretation: an older record read by newer code with different
	// field meanings would attribute a divergence to the wrong cause, which is
	// worse than reporting none.
	Version int          `json:"version"`
	Steps   []storedStep `json:"steps"`
}

// recordVersion is the on-disk format. Bump it when a field changes meaning.
const recordVersion = 1

// recordPath is where a target's last record lives.
//
// Beside the layers, like the action cache, so a store somebody deleted takes
// its diagnostics with it rather than leaving records describing layers that
// are gone.
func recordPath(store, target string) string {
	// A target name reaches this from an Earthfile and a command line, so it is
	// not a filename until it has been made one.
	safe := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == '.' || r == os.PathSeparator {
			return '-'
		}

		return r
	}, target)

	return filepath.Join(store, "records", safe+".json")
}

// saveRecord writes what this build did, for the next one to compare against.
func saveRecord(store, target string, r *core.Record) error {
	rec := storedRecord{Version: recordVersion, Steps: make([]storedStep, 0, len(r.Steps))}

	for _, s := range r.Steps {
		rec.Steps = append(rec.Steps, storedStep{
			Ident: s.Ident,
			Node:  s.Node.String(),
			Base:  s.Base.String(),
			Op:    s.Op.String(),
			Env:   s.Env.String(),
			Plat:  s.Plat.String(),
			Layer: s.Layer.String(),

			Source:      s.Meta.Source,
			Description: s.Meta.Description,
		})
	}

	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode the build record: %w", err)
	}

	path := recordPath(store, target)

	err = os.MkdirAll(filepath.Dir(path), 0o750)
	if err != nil {
		return fmt.Errorf("prepare the record directory: %w", err)
	}

	// Replaced rather than kept alongside: this is the *previous* build, one per
	// target, and a history would need an eviction policy to go with it. I9
	// governs the cache, where a consumer holds a digest and must find the same
	// bytes; nobody holds a record.
	return os.WriteFile(path, b, 0o600)
}

// loadRecord reads the previous build's record, if there is a readable one.
//
// Absent, unreadable and unrecognised are one answer, and it is "no comparison
// available" rather than an error. The action cache follows the same rule for
// the same reason, and it is stronger here: this is a diagnostic, and a
// diagnostic with the power to fail a build is worse than no diagnostic.
func loadRecord(store, target string) (*core.Record, bool) {
	b, err := os.ReadFile(recordPath(store, target)) // a path this engine wrote
	if err != nil {
		return nil, false
	}

	var rec storedRecord

	err = json.Unmarshal(b, &rec)
	if err != nil || rec.Version != recordVersion {
		return nil, false
	}

	out := &core.Record{Steps: make([]core.StepRecord, 0, len(rec.Steps))}

	for _, s := range rec.Steps {
		step := core.StepRecord{
			Ident: s.Ident,
			Meta:  ir.Meta{Source: s.Source, Description: s.Description},
		}

		// A digest that will not parse makes the whole record unusable rather
		// than a step with a zero digest in it: a zero compares equal to
		// nothing else, so it would attribute every divergence to whichever
		// component happened to be damaged.
		for _, f := range []struct {
			hex  string
			into *ir.NodeID
		}{
			{s.Node, &step.Node},
			{s.Base, &step.Base},
			{s.Op, &step.Op},
			{s.Env, &step.Env},
			{s.Plat, &step.Plat},
			{s.Layer, &step.Layer},
		} {
			id, err := parseNodeID(f.hex)
			if err != nil {
				return nil, false
			}

			*f.into = id
		}

		out.Steps = append(out.Steps, step)
	}

	return out, true
}

func parseNodeID(s string) (ir.NodeID, error) {
	var id ir.NodeID

	b, err := hex.DecodeString(s)
	if err != nil {
		return id, err
	}

	if len(b) != len(id) {
		return id, fmt.Errorf("digest is %d bytes, not %d", len(b), len(id))
	}

	copy(id[:], b)

	return id, nil
}

// whyItReran describes the first step at which this build differs from the last.
//
// Green paper B.4, which has been implemented and tested and listed as real
// since S0 without a caller: a record was assembled every build, three of its
// fields were printed, and it was dropped when the process exited. There has
// never been a second record for `Diverge` to compare the first against.
//
// Empty when the builds agree, when there is no previous record, or when the
// difference is one nobody asked about - the same rule the cache summary and
// the conflict warning follow, because a line that appears on an ordinary build
// stops being read before it is needed.
func whyItReran(store, target string, now *core.Record) string {
	before, ok := loadRecord(store, target)
	if !ok {
		return ""
	}

	d := core.Diverge(before, now)
	if d.Cause == core.CauseNone {
		return ""
	}

	// Indented under the step table it follows, and prefixed so the reader
	// knows which of the two builds is which.
	var b strings.Builder

	b.WriteString("  since the last build of this target:\n")

	for line := range strings.SplitSeq(strings.TrimRight(core.Report(d), "\n"), "\n") {
		fmt.Fprintf(&b, "    %s\n", line)
	}

	return b.String()
}
