package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestAFunctionCopiesFromTheCallersContext.
//
// **Documented, and the distinction is the point.** `docs/earthfile/earthfile.md`:
// *"Unlike performing a `BUILD +target`, functions inherit the build context
// and the build environment from the caller"* - and, two lines on, that global
// imports and args come from the Earthfile where the function is *defined*. So
// a function resolves `+other` against its own file and `COPY x` against the
// caller's directory, and those are different answers.
//
// Locally they are the same directory and nothing can tell them apart. A
// *remote* function separates them:
// `DO github.com/EarthBuild/earthly-command-example:main+COPY_CAT` runs
// `COPY message.txt ./`, the caller makes `message.txt`, and the remote holds
// only an Earthfile, a licence and a readme. This engine looked in the clone
// and reported the file missing from a cache directory the author never wrote
// (E716).
func TestAFunctionCopiesFromTheCallersContext(t *testing.T) {
	t.Parallel()

	f := &fetcher{dir: ctxWith(t, map[string]string{
		testEarthfile: versioned +
			"\nCOPY_CAT:\n    FUNCTION\n    COPY message.txt ./\n    RUN cat message.txt\n",
	})}

	dir := ctxWith(t, map[string]string{"message.txt": "hello function\n"})

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    DO github.com/org/repo+COPY_CAT
`, testMain, interp.WithRemotes(f.fetch), interp.WithContext(dir))
	if err != nil {
		t.Fatalf("a function read its own repository rather than the caller's"+
			" context: %v", err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "message.txt") {
		t.Errorf("the copy did not reach the plan:\n%s", got)
	}
}

// TestARemoteTargetKeepsItsOwnContext.
//
// The other half of the same rule, and the reason the first one is scoped to
// functions rather than to fetched units. `BUILD github.com/org/repo+build`
// copying `src/` means *that repository's* `src/` - the reference is to a
// target, and a target brings its own context.
//
// `wildcard-copy.earth+wildcard-remote` is the corpus case, and a rule written
// as "a fetched unit reads the caller's context" would break it while making
// the function test pass.
func TestARemoteTargetKeepsItsOwnContext(t *testing.T) {
	t.Parallel()

	f := &fetcher{dir: ctxWith(t, map[string]string{
		testEarthfile: versioned +
			"\nbuild:\n    FROM alpine:3.22\n    COPY theirs.txt ./\n",
		"theirs.txt": "from the remote\n",
	})}

	// The caller has a different file, and must not be what is read.
	dir := ctxWith(t, map[string]string{"ours.txt": "from the caller\n"})

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    BUILD github.com/org/repo+build
`, testMain, interp.WithRemotes(f.fetch), interp.WithContext(dir))
	if err != nil {
		t.Fatalf("a remote target could not read its own repository: %v", err)
	}
}
