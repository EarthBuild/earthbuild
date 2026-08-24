package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// What was faulted in is keyed the way the capture will look for it.
//
// **Two spellings of one path is how an exclusion silently excludes nothing.** A
// fault-in names the absolute path the step opened; a capture walks a tree and
// names each entry relative to its root. The faulted-in files are the ones that
// must be left out of the step's delta - they came from the base, not from the
// step - and an exclusion list keyed the other way matches none of them.
//
// The result is not an error. The capture succeeds, the layer is produced, and
// it contains files the step never wrote: a delta claiming work that was
// somebody else's base (E295).
//
// The catalogue removes the relativisation and the package stayed green, because
// nothing asked what the keys looked like - only that a capture happened.
func TestWhatWasFaultedInIsKeyedAsTheCaptureSeesIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "merged")

	// Real files, because a fault-in is only recorded once something arrived -
	// a host that answered "not there" leaves nothing to exclude (E289).
	cc := filepath.Join(root, "usr", "bin", "cc")
	elsewhere := filepath.Join(dir, "another-delta", "lib.so")

	for _, p := range []string{cc, elsewhere} {
		err := os.MkdirAll(filepath.Dir(p), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(p, []byte("x"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	s := &Server{Fills: &Fills{}}

	// What a fault-in records: the absolute path inside the step's filesystem.
	s.Fills.remember("h1", root, cc)

	// And one from outside this delta, which must not appear at all: another
	// handle's filesystem is not this one's to exclude from.
	s.Fills.remember("h1", root, elsewhere)

	got := s.placedIn("h1", root)

	if _, ok := got["usr/bin/cc"]; !ok {
		t.Errorf("the exclusion is keyed %v, and a capture of %s looks for"+
			" \"usr/bin/cc\"\n  a list keyed the other way excludes nothing, and"+
			" the delta then claims a file the step never wrote (E295)",
			keysOf(got), root)
	}

	// The ancestors come too, deliberately: a directory the fault-in created is
	// the base's rather than the step's (E307). What must not come is anything
	// from outside this delta.
	for k := range got {
		if strings.HasPrefix(k, "..") || strings.Contains(k, "another-delta") {
			t.Errorf("placedIn carried %q, which is not in this delta"+
				"\n  another handle's filesystem is not this one's to exclude"+
				" from", k)
		}

		if filepath.IsAbs(k) {
			t.Errorf("placedIn carried the absolute spelling %q, which a"+
				" capture will never look for", k)
		}
	}
}

// keysOf is what a failure needs to print: the spellings, not the ids.
func keysOf(m map[string]ir.NodeID) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}
