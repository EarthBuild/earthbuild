package exec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A guest older than the engine that dials it is worth saying so about.
//
// The guest is a separate binary. `go run ./cmd/earth-native` rebuilds the
// engine and not the agent, so a change to `engine/guest` is *not in the guest
// that runs* until somebody rebuilds it by hand - and nothing says so, because
// the protocol version is the same on both sides and the version check passes.
//
// This cost an increment. Two guest-side fixes were measured against a guest
// built before them, the measurements showed the fixes doing nothing, and the
// conclusion written down was that a third bug existed. There was no third bug
// (E498, E499).
//
// A warning rather than a refusal: a released install ships both together with
// whatever timestamps the packaging gave them, and refusing to build over a
// file date would be refusing the common case to catch an uncommon one. The
// same call E26's case-insensitivity note makes, and for the same reason.
func TestAGuestOlderThanTheEngineIsReported(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	engine := filepath.Join(dir, "earth-native")
	guest := filepath.Join(dir, "earth-guestd")

	for _, p := range []string{engine, guest} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(guest, old, old); err != nil {
		t.Fatal(err)
	}

	note := staleGuestNote(engine, guest)
	if note == "" {
		t.Fatal("a guest two hours older than the engine was not mentioned")
	}

	for _, want := range []string{"earth-guestd", "older", "rebuild"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note is %q and does not say %q", note, want)
		}
	}

	// The other way round says nothing: a guest built *after* the engine is the
	// ordinary case for anybody working on the engine.
	if got := staleGuestNote(guest, engine); got != "" {
		t.Errorf("a guest newer than the engine was reported: %q", got)
	}
}

// A guest and an engine of the same age say nothing.
//
// The case that matters for a released install, where both are unpacked at
// once: a note on every build would be the E491 mistake made again, in a place
// where it is even easier to ignore.
func TestAGuestTheSameAgeIsNotReported(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	engine := filepath.Join(dir, "earth-native")
	guest := filepath.Join(dir, "earth-guestd")

	at := time.Now().Add(-time.Hour)

	for _, p := range []string{engine, guest} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}

	if got := staleGuestNote(engine, guest); got != "" {
		t.Errorf("two binaries of the same age produced %q", got)
	}
}
