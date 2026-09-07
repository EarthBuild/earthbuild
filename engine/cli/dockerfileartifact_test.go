package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// A dry run does not build the target that would produce a Dockerfile.
//
// `FROM DOCKERFILE +gen/` cannot be planned without the file, and the file does
// not exist until `+gen` has been built. The engine can be given a way to build
// it (E487), and **a dry run is precisely the caller that must not be**: it
// promises to resolve a plan and run nothing, and a dry run that quietly built a
// target would be the one command here that lies about what it does (E488).
//
// So the capability is withheld, and the refusal says what it is: a plan that
// cannot be made without running something.
func TestADryRunWillNotBuildATargetToFinishPlanning(t *testing.T) {
	t.Parallel()

	dir := dockerfileProducingProject(t)

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "+main", DryRun: true,
	})
	if err == nil {
		t.Fatal("a dry run planned a file whose Dockerfile does not exist yet")
	}

	if !strings.Contains(err.Error(), "without anywhere to build it") {
		t.Errorf("refused with %q, and a dry run is the caller that withheld"+
			" the capability", err)
	}
}

// And a real build is given one.
//
// The half that says the seam is filled. Not a full build here - this machine
// may have no sandbox, and that is a different failure - so what is asserted is
// that the engine *tried*: anything but "nowhere to build it" means the
// capability was supplied and the sub-build was attempted.
//
// Through `Run` rather than by reading the options: an option a caller sets and
// the run never passes on is *an option accepted and not provided*, which is
// what E465 caught about the project argument files.
func TestARealBuildIsGivenSomewhereToBuildADockerfile(t *testing.T) {
	t.Parallel()

	dir := dockerfileProducingProject(t)

	err := cli.Run(context.Background(), cli.Options{Dir: dir, Target: "+main"})
	if err == nil {
		// A machine that can run the sub-build: then the plan was made, which
		// is the strongest form of the same claim.
		return
	}

	if strings.Contains(err.Error(), "without anywhere to build it") {
		t.Errorf("a real build was refused with %q, so the capability the"+
			" interpreter asks for is not being supplied", err)
	}
}

// dockerfileProducingProject writes an Earthfile whose Dockerfile is made by
// one of its own targets.
func dockerfileProducingProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "Earthfile"), []byte(
		"VERSION 0.8\n"+
			"\nmain:\n    FROM DOCKERFILE +gen/\n    RUN echo built\n"+
			"\ngen:\n    FROM alpine:3.22\n"+
			"    RUN printf 'FROM alpine:3.22\\nRUN echo generated\\n' > Dockerfile\n"+
			"    SAVE ARTIFACT Dockerfile\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

// Two targets that need each other built to be planned say so.
//
// `+a` is planned from `+b`'s Dockerfile and `+b` from `+a`'s. Without a guard
// that is not an error, it is a recursion - and one no cycle detector can see,
// because each nested build is a fresh interpreter with a fresh view of the
// graph (E488).
//
// Caught while *planning* the inner target, so no machine is needed to find it -
// which is the right place, because the answer does not depend on one.
func TestATargetThatNeedsItselfToBePlannedIsRefused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Two targets, each planned from the other's Dockerfile. **Not one target
	// naming itself** - the interpreter's cycle detector catches that, and says
	// it better - because each nested build is a fresh interpreter and neither
	// one's detector can see the other's half of this.
	// `-f` naming an artifact, with a local context.
	//
	// The shape matters and took two attempts to find. A *context* that is a
	// target - `FROM DOCKERFILE +b/` - puts an edge in the graph, so the
	// interpreter's own cycle detector sees the loop and says it better. With
	// `-f +b/x .` there is no edge: the Dockerfile comes from a target and the
	// context is this directory, so nothing in either graph refers to the other
	// and only the fetcher knows both halves.
	err := os.WriteFile(filepath.Join(dir, "Earthfile"), []byte(
		"VERSION 0.8\n"+
			"\nmain:\n    FROM DOCKERFILE -f +a/Dockerfile .\n    RUN echo built\n"+
			"\na:\n    FROM DOCKERFILE -f +b/Dockerfile .\n    SAVE ARTIFACT Dockerfile\n"+
			"\nb:\n    FROM DOCKERFILE -f +a/Dockerfile .\n    SAVE ARTIFACT Dockerfile\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	runErr := cli.Run(context.Background(), cli.Options{Dir: dir, Target: "+main"})
	if runErr == nil {
		t.Fatal("a target that needs itself to be planned was planned")
	}

	for _, want := range []string{"+a/Dockerfile", "needed to plan itself"} {
		if !strings.Contains(runErr.Error(), want) {
			t.Errorf("refused with %q, which does not say %q", runErr, want)
		}
	}
}
