package cache

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// Costs remembers how long each class of step took, so a later build can price
// one before running it. Implements core.Costs.
//
// **The measurement placement has never had.** `Hints.EstimatedSeconds` has been
// declared, encoded and decoded since the fleet protocol was written and nothing
// ever set it, so the only thing a driver knows about a step's cost is `Bytes` -
// the size of its *inputs*. A base worth shipping for a ten-minute compile and
// one worth keeping for a two-second step are therefore priced identically,
// against a fleet-wide average step (`fleet.Rate.Slots`).
//
// Keyed by class rather than by chain key, which is what makes it survive an
// edit: a class is a prediction key (`core.StepClass`), so `go build ./...` is
// the same kind of step whether or not a source file changed. A key that moved
// with the source would accumulate history for nothing.
//
// A cost is a **hint** and nothing derived from it can change a result. A store
// that cannot be read, or that answers with last month's number, costs a
// placement and never a build - which is what lets every path here degrade to
// "no idea" rather than to an error.
//
// One file per class, named by the class key, inserted by rename: the same
// arrangement `Profiles` uses and for the same reason.
type Costs struct{ dir string }

// OpenCosts prepares a cost store under root.
//
// Reports a directory it cannot use rather than degrading silently, for the
// reason `OpenProfiles` does: a build whose placement is running blind is one
// whose speed nobody can account for (I11).
func OpenCosts(root string) (*Costs, error) {
	dir := filepath.Join(root, "costs")

	// 0750, as the profile store is: what a machine builds and how long it
	// takes is a description of what somebody works on.
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("prepare the cost store at %s: %w", dir, err)
	}

	return &Costs{dir: dir}, nil
}

// storedCost is the on-disk form.
//
// Explicit rather than encoding a `time.Duration` directly: this outlives the
// process and has to survive the type changing, and milliseconds are what the
// wire already speaks (`Reply.DurationMillis`).
type storedCost struct {
	Millis int64 `json:"millis"`
}

// Get is what this class of step took when it last ran.
//
// **Absent is not zero.** Zero would read as "instant", and a step priced at
// nothing is one no transfer could ever be worth - the inverted answer
// `Rate.Slots` already refuses to give for an unstated size.
func (c *Costs) Get(class core.Key) (time.Duration, bool) {
	b, err := os.ReadFile(c.path(class)) //nolint:gosec // a path named by a digest
	if err != nil {
		return 0, false
	}

	var s storedCost
	if err := json.Unmarshal(b, &s); err != nil || s.Millis <= 0 {
		return 0, false
	}

	return time.Duration(s.Millis) * time.Millisecond, true
}

// Put records what this class of step just took.
//
// **The latest run, not an average.** A machine that gets faster or a step that
// grows wants the number it costs *now*; an average takes a build to forget a
// change a single run already knows about, and the thing being priced is a
// decision that will be made again in a minute.
//
// A step that took no measurable time is not filed: zero means the backend could
// not say (E467) rather than that the step was instant, and writing it would
// displace a real measurement with an absence.
func (c *Costs) Put(class core.Key, took time.Duration) {
	millis := took.Milliseconds()
	if millis <= 0 {
		return
	}

	b, err := json.Marshal(storedCost{Millis: millis})
	if err != nil {
		return
	}

	// Written beside its destination and renamed, as a profile is: several
	// steps of one class finish at once, and a reader must see one number or
	// the other rather than half of either.
	tmp, err := os.CreateTemp(c.dir, ".cost-*")
	if err != nil {
		return
	}

	name := tmp.Name()

	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		err = os.Chmod(name, 0o600)
	}

	if err != nil {
		_ = os.Remove(name)

		return
	}

	if err := os.Rename(name, c.path(class)); err != nil {
		_ = os.Remove(name)
	}
}

// path is where one class's cost is filed.
func (c *Costs) path(class core.Key) string {
	return filepath.Join(c.dir, hex.EncodeToString(class[:])+".json")
}
