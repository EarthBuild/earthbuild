package interp

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestAReferenceMayNameSeveralTargets.
//
// **`BUILD ./wildcard/*+test` builds every matching directory's target.** The
// corpus writes it five ways - `*`, `**`, a character class, a bare `./*`, and a
// path climbing out with `..` - and the engine took every one of them literally,
// looking for a directory called `*` and saying it was not there. Eighteen of the
// corpus's invocations turn on this one form.
//
// The order is sorted, and that is not decoration: a reference expanding to
// several targets contributes them to a build in the order given, and a glob
// whose order came from the filesystem would key the same Earthfile differently
// on two machines.
func TestAReferenceMayNameSeveralTargets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, d := range []string{"wildcard/bar", "wildcard/baz", "wildcard/foo", "wildcard/deep/inner", "plain"} {
		err := os.MkdirAll(filepath.Join(root, d), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(root, d, "Earthfile"), []byte("VERSION 0.8\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	// A directory with no Earthfile is not a target and must not be offered as
	// one: the glob is over places a target could live, not over names.
	err := os.MkdirAll(filepath.Join(root, "wildcard/notatarget"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		ref  string
		want []string
	}{
		{"./wildcard/*+test", []string{"./wildcard/bar+test", "./wildcard/baz+test", "./wildcard/foo+test"}},
		{"./wildcard/b*[rz]+test", []string{"./wildcard/bar+test", "./wildcard/baz+test"}},
		// `wildcard/` holds no Earthfile of its own, only its children do, so
		// it is not a place a target could live and does not match.
		{"./*+test", []string{"./plain+test"}},

		// No metacharacter: returned as written, and never touched by the
		// filesystem - a plain reference to a directory that does not exist yet
		// must still reach the resolver, which says so properly.
		{"./plain+test", []string{"./plain+test"}},
		{"+test", []string{"+test"}},
		{"./nowhere+test", []string{"./nowhere+test"}},
	} {
		got, expandErr := expandRef(root, c.ref)
		if expandErr != nil {
			t.Errorf("%s: %v", c.ref, expandErr)
			continue
		}

		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("expandRef(%q) = %v, want %v", c.ref, got, c.want)
		}
	}
}

// TestADoubleStarCrossesDirectories.
//
// `**` is not `*`, and `filepath.Glob` knows only the second: it treats `**` as
// a single level, so `./wildcard/**/*+test` matched one directory down and
// stopped. The corpus uses it to mean any depth.
func TestADoubleStarCrossesDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, d := range []string{"w/a", "w/a/b", "w/a/b/c"} {
		err := os.MkdirAll(filepath.Join(root, d), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(root, d, "Earthfile"), []byte("VERSION 0.8\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	got, err := expandRef(root, "./w/**/*+test")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"./w/a+test", "./w/a/b+test", "./w/a/b/c+test"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
