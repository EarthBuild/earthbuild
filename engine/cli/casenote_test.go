package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// The case-insensitivity note appears where it can explain something.
//
// It is five lines ending in an `hdiutil create` command, and it was printed at
// the *start of every build* on every Mac - before anything had happened, let
// alone failed. Beside a real error it reads as that error's diagnosis, and
// twice it was not: once beside `cannot find earth-guestd`, once beside a guest
// built for the wrong platform. **Three increments of work rested on a paragraph
// that was true and irrelevant** (E491).
//
// It is not noise in general - `storeDir` in this package's own fixtures cites
// E26 for it, where 19 of 26 failures in a corpus sweep were the disk rather
// than the engine. The knowledge is worth keeping and the moment was wrong.
//
// So: on a failure, where it might be the cause. Not on a build that worked,
// where there is nothing for it to explain.
func TestTheCaseNoteIsNotPrintedWhenNothingFailed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "Earthfile"),
		[]byte("VERSION 0.8\n\nmain:\n    FROM alpine:3.22\n    RUN echo hi\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder

	// A dry run resolves the plan and reports it: nothing fails, so nothing
	// needs explaining.
	err = cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "+main", DryRun: true, Out: &out,
	})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	if strings.Contains(out.String(), "case-insensitive") {
		t.Errorf("a build that did not fail was told about the filesystem:\n%s",
			out.String())
	}
}

// And on a failure it is there, where the store is one.
func TestTheCaseNoteIsPrintedWhenSomethingFailed(t *testing.T) { //nolint:paralleltest // t.Setenv
	store := t.TempDir()
	if sensitive, known := cli.CaseSensitive(store); !known || sensitive {
		t.Skip("this machine's temporary directory is case-sensitive, so there" +
			" is no note to print")
	}

	dir := t.TempDir()

	// Refused while planning, which is a failure like any other as far as the
	// reader is concerned: they asked for a build and did not get one.
	err := os.WriteFile(filepath.Join(dir, "Earthfile"),
		[]byte("VERSION 0.8\n\nmain:\n    FROM alpine:3.22\n    STOPSIGNAL SIGTERM\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("EARTH_CACHE_DIR", store)

	var out strings.Builder

	err = cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "+main", Out: &out,
	})
	if err == nil {
		t.Fatal("the build was expected to fail")
	}

	if !strings.Contains(out.String(), "case-insensitive") {
		t.Errorf("a build failed on a case-insensitive store and nothing"+
			" mentioned it:\n%s", out.String())
	}
}

// And on a failure that happens *after* planning.
//
// The half a mutant asked for: the note is emitted from a deferred check, and a
// `return build(...)` leaves the local error nil while the caller gets the
// failure - **a deferred check that reads the wrong variable is a check that
// cannot fire**. A plan that resolves and then cannot run is the case that tells
// the two apart, and a refusal while planning is not (E491).
//
// No sandbox needed: a guest binary that is not there fails the build for a
// reason this machine can produce on demand.
func TestTheCaseNoteReachesAFailureAfterPlanning(t *testing.T) { //nolint:paralleltest // t.Setenv
	store := t.TempDir()
	if sensitive, known := cli.CaseSensitive(store); !known || sensitive {
		t.Skip("this machine's temporary directory is case-sensitive")
	}

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "Earthfile"),
		[]byte("VERSION 0.8\n\nmain:\n    FROM alpine:3.22\n    RUN echo hi\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("EARTH_CACHE_DIR", store)
	t.Setenv("EARTH_GUESTD", filepath.Join(t.TempDir(), "no-such-guest"))

	var out strings.Builder

	err = cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "+main", Out: &out,
	})
	if err == nil {
		t.Fatal("a build with no guest binary was expected to fail")
	}

	if !strings.Contains(out.String(), "case-insensitive") {
		t.Errorf("a build failed after planning on a case-insensitive store"+
			" and nothing mentioned it:\n%s", out.String())
	}
}
