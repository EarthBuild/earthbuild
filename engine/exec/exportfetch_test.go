package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A sandbox that shares a filesystem hands back the path it staged at, and
// nothing is fetched.
func TestASharedExportIsReadInPlace(t *testing.T) {
	t.Parallel()

	at := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(at, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, done, err := stagedOnHost(context.Background(), &plainSandbox{}, at, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	defer done()

	if got != at {
		t.Errorf("a shared export was moved: %s", got)
	}
}

// A sandbox with nothing in common fetches, and the path comes back inside the
// directory it was given.
func TestAnExportIsFetchedWhereNothingIsShared(t *testing.T) {
	t.Parallel()

	into := t.TempDir()
	sb := &fetchingSandbox{}

	got, done, err := stagedOnHost(context.Background(), sb, "/store/exports/out.txt", into)
	if err != nil {
		t.Fatal(err)
	}

	defer done()

	if !sb.asked {
		t.Error("nothing was fetched from a sandbox that shares no filesystem")
	}

	if !strings.HasPrefix(got, into) {
		t.Errorf("the fetched artifact is at %s, outside %s", got, into)
	}

	// Named for what was staged, because the caller copies it to the
	// destination the author asked for and a directory would arrive as its
	// contents otherwise.
	if filepath.Base(got) != "out.txt" {
		t.Errorf("the fetched artifact is named %s", filepath.Base(got))
	}
}

// The guest is asked about a path *it* can open.
func TestTheGuestIsAskedForItsOwnPath(t *testing.T) {
	t.Parallel()

	sb := &fetchingSandbox{}

	_, done, err := stagedOnHost(context.Background(), sb, "/store/exports/out.txt", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	defer done()

	if sb.at != "/store/exports/out.txt" {
		t.Errorf("the guest was asked for %q", sb.at)
	}
}

type fetchingSandbox struct {
	plainSandbox

	asked bool
	at    string
}

func (s *fetchingSandbox) GuestStore() string { return "/store" }

func (s *fetchingSandbox) FetchExport(_ context.Context, guestPath, into string) error {
	s.asked, s.at = true, guestPath

	return os.WriteFile(filepath.Join(into, filepath.Base(guestPath)), []byte("x"), 0o600)
}
