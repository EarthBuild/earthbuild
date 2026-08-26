package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// schedulerPorts says, for every field of core.Scheduler, whether the front end
// sets it - and if it does not, why that is deliberate.
//
// An empty reason means "the front end must set this". Anything else is a
// statement that leaving it alone is correct, and the test then requires the
// front end *not* to set it, so a reason cannot quietly go stale by being
// overtaken.
//
// This exists because three ports were found unwired in two iterations, each by
// accident and each while looking for something else. A port is optional by
// design here - "with no cache every step executes, which is slower and never
// wrong" - and that same tolerance is what lets one go unset for the life of a
// branch with nothing to say so. **A field whose absence is harmless is a field
// nothing will notice the absence of**.
type portRole int

const (
	// mustSet is an input the front end has to provide.
	mustSet portRole = iota
	// mustRead is an *output*: filled in by Run and meaningless if nobody looks
	// at it afterwards. A third role rather than a special case of the first,
	// because the two fail in opposite directions and this table found its
	// first defect by not having it - Stats had been counting cache hits,
	// misses and flattenings on every build and showing them to nobody.
	mustRead
	// inert is a port deliberately left alone, and reason says why.
	inert
)

type port struct {
	role   portRole
	reason string
}

var schedulerPorts = map[string]port{
	"Workers":  {role: mustSet},
	"Executor": {role: mustSet},
	"Cache":    {role: mustSet},
	"Blobs":    {role: mustSet},
	"Writer":   {role: mustSet},
	"Record":   {role: mustSet},
	"MaxStack": {role: mustSet},

	// An input the front end supplies, like the rest of mustSet: the CLI passes
	// `Options.NoCache` straight through, so a build told to redo everything
	// reads nothing already in the store and still writes what it produces
	// (E462). `mustRead` is for what Run fills in, which this is not.
	"NoCache": {role: mustSet},

	"Stats": {role: mustRead},

	"Trusted": {role: inert, reason: "nil accepts every writer, which is right for a cache with" +
		" one writer in it. Green paper A5 makes an entry from outside the trust domain data" +
		" rather than a result, and there is no outside until the fleet transport exists" +
		" (S6, not started)."},

	"Materialiser": {role: inert, reason: "nil on purpose: where a step runs in a VM the executor" +
		" owns the filesystem and assembles the same stack on its own side. Scheduler-side" +
		" materialisation is for the case where the scheduler owns it, so that a leaked mount" +
		" is impossible on the failure path."},

	// Inert until E125, and the history is worth keeping where somebody
	// arriving at the port will read it: nothing set Result.Observed, so every
	// profile would have been empty, and an empty observation agrees with every
	// base (E112). A real source exists now for COPY steps (E119) and the
	// empty-observation rule is stated on the base rather than the opcode, so a
	// profile naming nothing about a base it stood on is refused on both sides.
	"Profiles": {role: mustSet},
	// The other half: both are required for L2 to run at all.
	// store.LayerStore.View reads the merged stack without mounting it (E114),
	// and its digests are asserted to equal the ones an observer records inside
	// the mount (E121) - which is the comparison Consistent makes.
	"Views": {role: mustSet},

	"Capabilities": {role: inert, reason: "nil means no restriction here, and the refusal happens" +
		" earlier instead: the interpreter refuses an unsupported construct while reading the" +
		" Earthfile, so a graph containing one never reaches a scheduler. Green paper I10 is met" +
		" by that path, and this one is a second gate for a caller that builds a graph directly."},

	// Wired since EARTH_PARALLELISM. Unset it is still zero, which is NumCPU
	// and the default every build had; what changed is that a caller can now
	// ask for fewer. A serial build is a diagnostic instrument - a build that
	// stops with eight steps in flight could not be told apart from one that
	// would stop anyway - and the *bound* is what
	// `TestParallelismBoundsWhatRunsAtOnce` exercises with 1, 2 and 3 (E136).
	"Parallelism": {role: mustSet},
}

// Every port is either wired or has a reason.
//
// The shape `seam_test.go` established over interp.Plan, pointed at the other
// seam: a field added to core.Scheduler and never set by the front end fails
// here, and listing it is a statement about where it is acted on rather than a
// silence.
func TestEverySchedulerPortIsWiredOrDeclaredInert(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile(filepath.Clean("cli.go"))
	if err != nil {
		t.Fatal(err)
	}

	// The composite literal the front end builds. Looking at the text rather
	// than at a value because a nil interface and an unset field are the same
	// at runtime, and the difference between them is the whole question.
	body := string(src)

	sched := reflect.TypeFor[core.Scheduler]()

	for f := range sched.Fields() {
		if !f.IsExported() {
			continue
		}

		p, listed := schedulerPorts[f.Name]
		if !listed {
			t.Errorf("core.Scheduler.%s is not accounted for"+
				"\n  add it to schedulerPorts as mustSet, mustRead, or inert with a reason", f.Name)

			continue
		}

		set := strings.Contains(body, "\t\t"+f.Name+":")
		read := strings.Contains(body, "s."+f.Name)

		switch p.role {
		case mustSet:
			if !set {
				t.Errorf("core.Scheduler.%s is required and the front end does not set it", f.Name)
			}
		case mustRead:
			if !read {
				t.Errorf("core.Scheduler.%s is filled in by Run and nothing reads it"+
					"\n  an output nobody looks at is work the engine does for no one", f.Name)
			}
		case inert:
			if set {
				t.Errorf("core.Scheduler.%s is now set, so its reason for being unset is stale:"+
					"\n  %s", f.Name, p.reason)
			}
		}
	}
}

// A reason is a sentence, not a shrug.
//
// "optional" and "not needed" are the two that would pass the test above while
// telling a reader nothing, and they are what this kind of table degenerates
// into when it is filled in under time pressure. The bar is that somebody
// arriving at an unset port can find out from here whether it is a decision or
// an oversight.
func TestEveryReasonSaysSomething(t *testing.T) {
	t.Parallel()

	for name, p := range schedulerPorts {
		if p.role != inert {
			if p.reason != "" {
				t.Errorf("%s is not inert, so its reason describes nothing: %q", name, p.reason)
			}

			continue
		}

		if len(strings.Fields(p.reason)) < 8 {
			t.Errorf("the reason for leaving %s unset is too short to be one: %q", name, p.reason)
		}
	}
}
