package fstime

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The same thing again against a real git, because the parse above was derived
// from one observed stream and a recorded sample cannot notice git changing its
// mind about the shape.
func TestARealCheckoutIsStampedFromItsOwnHistory(t *testing.T) {
	t.Parallel()

	_, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}

	root := t.TempDir()
	committedAt := ""

	run := func(args ...string) {
		t.Helper()

		// No hooks: this repository is a fixture, and the machine's global
		// hooks path would run the whole pre-commit suite inside it.
		cmd := exec.CommandContext(t.Context(), "git", append([]string{
			"-C", root, "-c", "core.hooksPath=", "-c", "commit.gpgsign=false",
		}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_COMMITTER_DATE="+committedAt, "GIT_AUTHOR_DATE="+committedAt,
		)

		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("git %v: %v\n%s", args, runErr, out)
		}
	}

	write := func(rel, body string) {
		t.Helper()

		dirErr := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o750)
		if dirErr != nil {
			t.Fatal(dirErr)
		}

		writeErr := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600)
		if writeErr != nil {
			t.Fatal(writeErr)
		}
	}

	// Committer dates, not author dates. An author date survives a rebase or a
	// cherry-pick, which sounds like the more stable choice and is the wrong
	// one: it lets a two-year-old patch land on today's tree carrying a
	// two-year-old stamp, and cargo would call the file it just changed fresh.
	// A committer date is rewritten whenever the commit is, so it only ever
	// moves forward - and it is what `%ct`, and SOURCE_DATE_EPOCH, mean.
	run("init", "-q", "-b", "main")
	write("src/first.rs", "one")
	write("src/second.rs", "two")
	run("add", "-A")
	committedAt = "@1700000100"
	run("commit", "-qm", "one")

	write("src/second.rs", "two, changed")
	run("add", "-A")
	committedAt = "@1700000200"
	run("commit", "-qm", "two")

	// Committed, then edited but not committed - the developer loop.
	write("src/first.rs", "one, working")
	// Never committed at all.
	write("src/loose.rs", "three")

	names := []string{"src", "src/first.rs", "src/second.rs", "src/loose.rs"}

	at := FromHistory(t.Context(), root, names)
	if at == nil {
		t.Fatal("a git checkout was not recognised as one")
	}

	// The one file that is still as committed takes its commit's time, and
	// would take the same one in any other clone of this history.
	if got := at("src/second.rs"); !got.Equal(time.Unix(1_700_000_200, 0)) {
		t.Errorf("src/second.rs got %v, wanted the commit that changed it", got)
	}

	// Both locally-changed files take the clock, and land after everything the
	// history knows about - which is the ordering the compiler reads.
	for _, rel := range []string{"src/first.rs", "src/loose.rs"} {
		if got := at(rel); !got.After(time.Unix(1_700_000_200, 0)) {
			t.Errorf("%s got %v, wanted a time after the last commit", rel, got)
		}
	}

	// **Touched but unchanged is not modified.** `git diff-index` alone answers
	// from stat data and calls this dirty, which would hand it the local clock
	// and lose the one property the commit time was chosen for. Reading the
	// status instead compares content.
	touched := time.Now().Add(time.Hour)
	touchErr := os.Chtimes(filepath.Join(root, "src/second.rs"), touched, touched)
	if touchErr != nil {
		t.Fatal(touchErr)
	}

	if got := FromHistory(t.Context(), root, names)("src/second.rs"); !got.Equal(time.Unix(1_700_000_200, 0)) {
		t.Errorf("a touched but unchanged file got %v, wanted its commit time", got)
	}

	if got := at("src"); !got.Equal(epoch) {
		t.Errorf("the directory got %v, wanted the epoch", got)
	}

	// Not a checkout, and it says so rather than inventing times.
	if FromHistory(t.Context(), t.TempDir(), names) != nil {
		t.Error("a directory with no history was stamped from one")
	}
}

// A context rooted below the top of the checkout, which is the ordinary case
// for a monorepo and the one where git's own path conventions disagree with
// each other.
func TestAContextBelowTheRootIsStampedInItsOwnTerms(t *testing.T) {
	t.Parallel()

	_, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}

	root := t.TempDir()
	committedAt := "@1700000100"

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", append([]string{
			"-C", root, "-c", "core.hooksPath=", "-c", "commit.gpgsign=false",
		}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_COMMITTER_DATE="+committedAt, "GIT_AUTHOR_DATE="+committedAt,
		)

		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("git %v: %v\n%s", args, runErr, out)
		}
	}

	write := func(rel, body string) {
		t.Helper()

		dirErr := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o750)
		if dirErr != nil {
			t.Fatal(dirErr)
		}

		writeErr := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600)
		if writeErr != nil {
			t.Fatal(writeErr)
		}
	}

	run("init", "-q", "-b", "main")
	write("app/kept.rs", "one")
	write("app/changed.rs", "two")
	write("elsewhere.rs", "three")
	run("add", "-A")
	run("commit", "-qm", "one")

	write("app/changed.rs", "two, edited")
	write("app/loose.rs", "four")

	at := FromHistory(t.Context(), filepath.Join(root, "app"), []string{"kept.rs", "changed.rs", "loose.rs"})
	if at == nil {
		t.Fatal("a subdirectory of a checkout was not recognised as one")
	}

	if got := at("kept.rs"); !got.Equal(time.Unix(1_700_000_100, 0)) {
		t.Errorf("kept.rs got %v, wanted its commit time", got)
	}

	for _, rel := range []string{"changed.rs", "loose.rs"} {
		if got := at(rel); !got.After(time.Unix(1_700_000_100, 0)) {
			t.Errorf("%s got %v, wanted the local clock", rel, got)
		}
	}
}
