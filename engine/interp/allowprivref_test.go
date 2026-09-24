package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestAReferenceMayGrantPrivilegeAcrossARepositoryBoundary.
//
// **The grant is per reference, and that is the point of it.** A remote
// Earthfile is not the reader's to trust, so `RUN --privileged` inside one is
// refused however the build was started - the CLI's `--allow-privileged` says
// "this build may use privilege", not "anything it fetches may". What crosses
// the boundary is the *referring* line saying so:
//
//	FROM --allow-privileged github.com/org/repo:main+privileged
//
// `tests/allow-privileged.earth` is eight targets of exactly this distinction:
// `reject-*` reference a privileged remote target plainly and must fail,
// `allow-*` reference the same target with the flag and must build. Reading
// only the CLI flag collapses the pair - either every reject builds, or every
// allow fails.
func TestAReferenceMayGrantPrivilegeAcrossARepositoryBoundary(t *testing.T) {
	t.Parallel()

	f := privilegedRemote(t)

	// Without the flag on the line, refused - whatever the CLI was given.
	_, err := interp.Build(versioned+
		"\nmain:\n    FROM github.com/org/repo+privileged\n", testMain,
		interp.WithRemotes(f.fetch), interp.WithAllowPrivileged(true))
	if err == nil {
		t.Error("a remote target used privilege with no line granting it;" +
			" the CLI flag is about this build, not about what it fetches")
	}

	// With it, built.
	p, err := interp.Build(versioned+
		"\nmain:\n    FROM --allow-privileged github.com/org/repo+privileged\n", testMain,
		interp.WithRemotes(f.fetch))
	if err != nil {
		t.Fatalf("the reference granted privilege and was still refused: %v", err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "privileged-step") {
		t.Errorf("the granted target did not reach the plan:\n%s", got)
	}

	// **All three references, because the corpus writes all three.**
	// `allow-privileged.earth` has a target per referring command - FROM, COPY
	// and BUILD - and a grant implemented on one of them passes its own test
	// and fails the other two.
	for _, line := range []string{
		"    BUILD --allow-privileged github.com/org/repo+privileged\n",
		"    COPY --allow-privileged github.com/org/repo+privileged/out .\n",
	} {
		_, err = interp.Build(versioned+"\nmain:\n    FROM alpine:3.22\n"+line,
			testMain, interp.WithRemotes(privilegedRemote(t).fetch))
		if err != nil {
			t.Errorf("%s granted privilege and was refused: %v", strings.TrimSpace(line), err)
		}
	}
}

// privilegedRemote is a repository whose target needs privilege.
func privilegedRemote(t *testing.T) *fetcher {
	t.Helper()

	return &fetcher{dir: ctxWith(t, map[string]string{
		testEarthfile: versioned +
			"\nprivileged:\n    FROM alpine:3.22\n    RUN --privileged privileged-step\n" +
			"    SAVE ARTIFACT /out\n",
	})}
}
