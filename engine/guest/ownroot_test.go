package guest

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/trace"
)

// The step's own root is not something the step read.
//
// The tracer resolves a path as the *tracer* sees it, outside the step's root,
// so a lookup of the root itself arrives as
// `/var/lib/earthbuild/scratch/mounts/h-3452187907/merged` - engine machinery,
// with a handle id in it that is different on every build.
//
// Found in a real profile (E497). It was recorded as a negative lookup, which is
// a claim about the base: "this path was not there". It is not a path in the
// base at all - it is the directory the base was assembled into - and the id in
// it means the claim can never describe a later build.
//
// E222 drew this line for mounts and named the reason: what this engine put
// there is regenerated or shared, so recording it says nothing about the step.
// **The root is the first thing this engine puts there** and was not on the
// list.
func TestAStepsOwnRootIsNotRecorded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := &Server{}
	h := ownRootHandle{root: root}

	// A file inside the base, named the way the tracer saw it.
	err := os.WriteFile(filepath.Join(root, "etc-hosts"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s.recordSightings(h, root, trace.Sightings{Paths: []string{
		root,
		filepath.Join(root, "etc-hosts"),
		"/etc/hosts",
	}}, nil, nil)

	got := s.watcherFor(h).observation()

	// The root itself is neither read nor absent: it is not a path in a base,
	// and recording it either way is a claim about one it is not part of.
	if _, recorded := got.Reads["/"]; recorded {
		t.Error("the step's own root was recorded as a read of `/`, which is" +
			" the base's root and not the directory it was assembled into")
	}

	if _, recorded := got.Reads[root]; recorded {
		t.Errorf("%s was recorded as a read, and it is this engine's own"+
			" directory rather than anything in the base", root)
	}

	for _, n := range got.Negative {
		if n == root {
			t.Errorf("%s was recorded as absent, which is a claim about a"+
				" base it is not part of", root)
		}
	}

	// A path *under* the root is a real file named from outside, and is kept -
	// under the name the step would use for it.
	//
	// Dropping it would lose a genuine input; keeping the outside name would
	// store a path with a per-build id in it, which can match nothing later. In
	// a real profile half the entries were the same files twice, once each way
	// (E498).
	if _, recorded := got.Reads["/etc-hosts"]; !recorded {
		t.Errorf("a file under the step's root was not recorded as /etc-hosts:"+
			" %v", sortedReads(got))
	}

	if _, recorded := got.Reads[filepath.Join(root, "etc-hosts")]; recorded {
		t.Error("a read was recorded under this machine's own path, which" +
			" carries a handle id and can match nothing on a later build")
	}

	// And an ordinary path still is, or the filter has eaten the observation.
	if len(got.Reads)+len(got.Negative) == 0 {
		t.Error("nothing at all was recorded, so this filter drops everything")
	}
}

// ownRootHandle is a handle whose root is where the test put it.
type ownRootHandle struct{ root string }

func (h ownRootHandle) Root() string                   { return h.root }
func (h ownRootHandle) Delta() string                  { return h.root }
func (h ownRootHandle) Release() error                 { return nil }
func (h ownRootHandle) Observations() core.Observation { return core.Observation{} }

// sortedReads is what was recorded, for a diagnostic.
func sortedReads(o core.Observation) []string {
	out := make([]string, 0, len(o.Reads))
	for k := range o.Reads {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

// A directory opened under its outside name still records a listing.
//
// The tracer names paths as *it* sees them, from outside the step's root, and
// recordSightings renames each to the name the base holds. `Opened` is keyed on
// the outside name, so a membership test against the renamed one matches nothing
// for precisely the paths that get renamed - which is every path in the step's
// own root. The narrowing would then look correct and record no listing at all,
// putting E794's stale build back with a test suite that still passed.
func TestAnOpenedDirectoryIsRecognisedByItsOutsideName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := &Server{}
	h := ownRootHandle{root: root}

	err := os.MkdirAll(filepath.Join(root, "d"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(root, "d", "f.txt"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(root, "d")

	s.recordSightings(h, root, trace.Sightings{
		Paths:  []string{outside},
		Opened: []string{outside},
	}, nil, nil)

	got := s.watcherFor(h).observation()

	if _, ok := got.Listings["/d"]; !ok {
		t.Errorf("a directory the step opened recorded no listing: %v"+
			"\n  Opened holds the tracer's outside name and the lookup used"+
			" the renamed one", got.Listings)
	}
}
