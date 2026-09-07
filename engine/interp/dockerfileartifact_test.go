package interp_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A Dockerfile a target produces can be built, given somewhere to get it from.
//
// `FROM DOCKERFILE +gen/` names a target's output as the build context and, with
// no `-f`, as the place the Dockerfile itself comes from. E478 refused it and
// wrote the constraint down: the Dockerfile is parsed while planning, and
// planning happens before anything is built.
//
// The constraint is real and the *refusal* was the wrong shape. This engine
// already has two capabilities a caller supplies or withholds - running a
// command to decide a condition, fetching another repository - and each is
// refused as "the caller did not provide" rather than as a gap. This is a third
// (E487).
//
// **The specification question turns out not to be one.** The worry was that a
// plan derived from an artifact is reproducible only if the artifact's key is
// part of the derived plan's key. It is stronger than that: the Dockerfile's
// *content* is parsed into the nodes, so every derived node's key covers it
// directly. Nothing is added to §4.4.
func TestADockerfileFromATargetIsBuiltWhenTheCallerCanFetchIt(t *testing.T) {
	t.Parallel()

	var asked string

	fetch := func(ref, _ string) (string, error) {
		asked = ref

		dir := t.TempDir()

		err := os.WriteFile(filepath.Join(dir, "Dockerfile"),
			[]byte("FROM alpine:3.22\nRUN echo from-the-generated-dockerfile\n"), 0o600)
		if err != nil {
			return "", err
		}

		return dir, nil
	}

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM DOCKERFILE +gen/\n    RUN echo after\n"+
		"\ngen:\n    FROM alpine:3.22\n    RUN touch Dockerfile\n"+
		"    SAVE ARTIFACT Dockerfile\n",
		testMain, interp.WithArtifacts(fetch))
	if err != nil {
		t.Fatalf("planning with a fetcher: %v", err)
	}

	if asked != "+gen/" {
		t.Errorf("the fetcher was asked for %q, and the file says +gen/", asked)
	}

	// The Dockerfile's own steps are in the graph, which is what "parsed while
	// planning" has to mean.
	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "from-the-generated-dockerfile") {
		t.Errorf("the generated Dockerfile's steps are not in the plan:\n%s", got)
	}
}

// Without a fetcher it is refused as something the caller did not provide.
//
// Not as a gap. The engine can do this; the *caller* planned without anywhere to
// get the file from, which is exactly what `earthbuild plan` and both sweeps do
// on purpose. Filed as a gap it was work somebody should build, and the work is
// passing an option (E487).
func TestADockerfileFromATargetWithNoFetcherIsNotProvided(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM DOCKERFILE +gen/\n"+
		"\ngen:\n    FROM alpine:3.22\n    RUN touch Dockerfile\n"+
		"    SAVE ARTIFACT Dockerfile\n", testMain)
	if err == nil {
		t.Fatal("a Dockerfile nothing had produced was parsed anyway")
	}

	if !errors.Is(err, interp.ErrNotProvided) {
		t.Errorf("refused with %q\n  which is filed as a gap in the engine"+
			" rather than as a capability this call withheld", err)
	}
}

// A fetcher that cannot produce the file says so, naming the target.
func TestAFetcherThatFailsIsReportedAgainstItsTarget(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM DOCKERFILE +gen/\n"+
		"\ngen:\n    FROM alpine:3.22\n    RUN touch Dockerfile\n"+
		"    SAVE ARTIFACT Dockerfile\n",
		testMain, interp.WithArtifacts(func(string, string) (string, error) {
			return "", errors.New("the build of it failed")
		}))
	if err == nil {
		t.Fatal("a fetcher that failed was treated as one that worked")
	}

	for _, want := range []string{"+gen", "the build of it failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refused with %q, which does not say %q", err, want)
		}
	}
}

// A different produced Dockerfile is a different graph.
//
// The green paper's §3.4c claim, and the one that decides whether §4.4 needs a
// term for this: **the description's content becomes the nodes it describes**,
// so a node's key covers it by (4.5) already. Keying on the producing target's
// identity instead would be weaker - two builds of that target could differ and
// key alike - and it is not needed.
//
// Asserted rather than argued, because "the key covers it" is exactly the kind
// of claim that is true when written and quietly false after a refactor that
// caches the parse (E489).
func TestADifferentProducedDockerfileIsADifferentGraph(t *testing.T) {
	t.Parallel()

	const recipe = "\nmain:\n    FROM DOCKERFILE +gen/\n" +
		"\ngen:\n    FROM alpine:3.22\n    RUN touch Dockerfile\n" +
		"    SAVE ARTIFACT Dockerfile\n"

	one := rootWithDockerfile(t, recipe, "FROM alpine:3.22\nRUN echo one\n")
	two := rootWithDockerfile(t, recipe, "FROM alpine:3.22\nRUN echo two\n")

	if one == two {
		t.Error("two builds whose produced Dockerfiles differ key alike," +
			" so the content does not reach the key and §4.4 would need a term" +
			" naming what produced it")
	}

	// And the same content keys alike, or the first half proves nothing: a key
	// that changed on every plan would pass the test above and mean nothing.
	if again := rootWithDockerfile(t, recipe, "FROM alpine:3.22\nRUN echo one\n"); again != one {
		t.Errorf("the same produced Dockerfile keyed %s and then %s", one, again)
	}
}

// rootWithDockerfile plans a recipe against a produced Dockerfile.
func rootWithDockerfile(t *testing.T, recipe, dockerfile string) string {
	t.Helper()

	p, err := interp.Build(versioned+recipe, testMain,
		interp.WithArtifacts(func(string, string) (string, error) {
			dir := t.TempDir()

			return dir, os.WriteFile(filepath.Join(dir, "Dockerfile"),
				[]byte(dockerfile), 0o600)
		}))
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	return p.Graph.Root.ID().String()
}
