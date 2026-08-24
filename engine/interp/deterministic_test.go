package interp_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/interp"
)

// Planning the same Earthfile twice produces the same graph.
//
// Go randomises map iteration deliberately, so anything that walks a map on the
// way to a node identity - an environment, a set of build arguments, a table of
// resolved targets - produces a different key each run. The damage is not a
// failed build: it is a cache that never hits, on a build tool whose entire
// argument is that it does. And it appears intermittently, which is the worst
// way to find anything.
//
// Compared as the whole traversal rather than as root identities, so that a
// difference is reported where it happens instead of as one opaque digest that
// no longer matches.
func TestPlanningIsDeterministic(t *testing.T) {
	t.Parallel()

	// Skipped under -short, which is how the race-instrumented run stays
	// usable: these walk every Earthfile in the repository, and instrumentation
	// multiplies that by about ten. They run in full on every ordinary pass.
	if testing.Short() {
		t.Skip("corpus sweep")
	}

	const runs = 3

	var compared int

	// A snapshot, because a target whose context is the *whole repository* - //nolint:gosec // a fixture this test wrote
	// `COPY . .`, which `+markdown-spellcheck` does - hashes every file in it.
	// Anything editing the tree while this test reads it changes the answer
	// legitimately, and the test then reports the planner as non-deterministic:
	// three investigations went into that before anybody noticed the editor was
	// open (E70).
	//
	// The engine is what is under test, not the filesystem's willingness to hold
	// still, so the tree is copied once and every run reads the copy.
	for _, f := range corpusSnapshot(t) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}

		for _, target := range targetsIn(string(src)) {
			first, err := plainPlan(string(src), target, filepath.Dir(f))
			if err != nil {
				continue
			}

			compared++

			for i := 1; i < runs; i++ {
				again, err := plainPlan(string(src), target, filepath.Dir(f))
				if err != nil {
					t.Errorf("%s [%s]: planned once and then refused: %v", f, target, err)

					break
				}

				if again != first {
					t.Errorf("%s [%s]: run %d differs from run 1\n  first: %s\n  again: %s",
						f, target, i+1, firstDifference(first, again), "")

					break
				}
			}
		}
	}

	if compared == 0 {
		t.Fatal("nothing was compared")
	}

	t.Logf("compared %d targets over %d runs each", compared, runs)
}

// plainPlan renders a plan as text: every node in traversal order, then what it
// declares. Text rather than digests so a difference names itself.
func plainPlan(src, target, dir string) (string, error) {
	p, err := interp.Build(src, target, interp.WithContext(dir))
	if err != nil {
		return "", err
	}

	var b strings.Builder

	for _, n := range p.Graph.Nodes() {
		b.WriteString(n.ID().String())
		b.WriteString(" ")
		b.WriteString(n.Op.Kind.String())
		b.WriteString(" ")
		b.WriteString(strings.Join(n.Op.Args, "\x1f"))
		b.WriteString(" dir=" + n.Op.Dir + " user=" + n.Op.User)

		// The chain key as well as the identity. They are separate hashers over
		// overlapping fields, so a map walked in one and sorted in the other is
		// a bug this would otherwise miss entirely - and it is the *key* that
		// decides whether the cache hits.
		b.WriteString(" key=" + core.DeriveChainKey(n, nil, nil).String())
		b.WriteString("\n")
	}

	for _, a := range p.Artifacts {
		b.WriteString("artifact " + a.Path + " -> " + a.LocalDest + "\n")
	}

	for _, img := range p.Images {
		b.WriteString("image " + img.Ref + "\n")
	}

	return b.String(), nil
}

// firstDifference names the first line that differs, which is where to look.
func firstDifference(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")

	for i := range al {
		if i >= len(bl) {
			return "run 1 has extra line: " + al[i]
		}

		if al[i] != bl[i] {
			return "line " + itoa(i+1) + ":\n    " + al[i] + "\n    " + bl[i]
		}
	}

	if len(bl) > len(al) {
		return "the second run has extra line: " + bl[len(al)]
	}

	return "the difference is not in the rendered text"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	var d []byte

	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}

	return string(d)
}

// corpusSnapshot copies the whole tree and returns the Earthfiles in the copy.
//
// The whole tree, recursively, because `COPY . .` hashes every file underneath
// it - a snapshot of one level per Earthfile would make this test fast by
// making it read almost nothing, which is the way a determinism check stops
// checking determinism over anything.
//
// Deliberately a copy rather than a lock or a retry: a test that asks the world
// to hold still fails on a busy machine, and one that retries passes for the
// wrong reason on a moving one.
func corpusSnapshot(t *testing.T) []string {
	t.Helper()

	root := t.TempDir()

	// The same exclusions the corpus itself uses, plus what a build leaves
	// behind. None of it is input, and `examples` alone is most of a gigabyte
	// of node_modules on a machine that has built them.
	skip := map[string]bool{
		".git": true, "node_modules": true, ".next": true, "build": true, "vendor": true,
	}

	err := filepath.Walk("../..", func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner is not this test's problem
		}

		if fi.IsDir() {
			if skip[fi.Name()] {
				return filepath.SkipDir
			}

			return nil
		}

		if !fi.Mode().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel("../..", p)
		if err != nil {
			return nil //nolint:nilerr // as above
		}

		dst := filepath.Join(root, rel)

		err = os.MkdirAll(filepath.Dir(dst), 0o750)
		if err != nil {
			return err //nolint:wrapcheck // the caller reports the path
		}

		b, err := os.ReadFile(p)
		if err != nil {
			// A file that vanished while the snapshot was taken is exactly what
			// the snapshot is for: skipped, rather than failing the test with
			// somebody else's editor.
			return nil //nolint:nilerr // deliberate
		}

		return os.WriteFile(dst, b, 0o600) //nolint:wrapcheck // as above
	})
	if err != nil {
		t.Fatalf("snapshot the corpus: %v", err)
	}

	return corpusUnder(t, root)
}

// corpusUnder finds the Earthfiles in a snapshot.
func corpusUnder(t *testing.T, root string) []string {
	t.Helper()

	var found []string

	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner is not this test's problem
		}

		if !fi.IsDir() && fi.Name() == testEarthfile {
			found = append(found, p)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	sort.Strings(found)

	return found
}
