package exec

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A fault-in hands on the base its path is relative to.
//
// **Two spellings of one path is how a fault-in silently fetches nothing**
// (E295, E305). The guest names a path inside its own root - `/usr/bin/cc` - and
// this engine knows that root as the directory it primed. Whoever fetches needs
// both: the absolute place to put the file, and the base it belongs to, because
// the same relative path exists in every base and answering from the wrong one
// is worse than answering from none.
//
// The catalogue pins the base argument, and dropping it left the package green:
// `FillFor` still called the fetcher, still with a plausible path, and nothing
// asserted which base the fetcher was told about.
func TestAFaultInSaysWhichBaseThePathIsRelativeTo(t *testing.T) {
	t.Parallel()

	into := t.TempDir()

	var (
		gotInto string
		gotAt   string
		called  bool
	)

	e := &Executor{
		Fetch: func(_ context.Context, _ []ir.NodeID, base, at string) error {
			called = true
			gotInto = base
			gotAt = at

			return nil
		},
	}

	e.remember("h1", primedBase{stack: []ir.NodeID{{1}}, into: into})

	err := e.FillFor(context.Background(), "h1", "/usr/bin/cc")
	if err != nil {
		t.Fatalf("%v", err)
	}

	if !called {
		t.Fatal("nothing was fetched, so this measured nothing")
	}

	if gotInto != into {
		t.Errorf("the fetcher was told the base is %q, want %q"+
			"\n  without it a fault-in is a path with no base to resolve it"+
			" against, and every base has a /usr/bin/cc (E305)", gotInto, into)
	}

	// And the place to put it is inside that base, not the guest's spelling of
	// it - the other half of the same mistake.
	if want := filepath.Join(into, "usr/bin/cc"); gotAt != want {
		t.Errorf("the fetcher was told to write %q, want %q", gotAt, want)
	}

	if strings.HasPrefix(gotAt, "/usr/") {
		t.Errorf("the guest's own spelling reached the fetcher: %q", gotAt)
	}
}
