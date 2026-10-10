package exec

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A sandbox that shares a filesystem is asked where the guest sees the file,
// and nothing is copied.
func TestASharedBlobIsNotCopied(t *testing.T) {
	t.Parallel()

	sb := &seeingSandbox{at: "/guest/blobs/x"}

	got, err := placeBlob(context.Background(), sb, "/host/blobs/x")
	if err != nil {
		t.Fatal(err)
	}

	if got != "/guest/blobs/x" {
		t.Errorf("the guest was told %s", got)
	}

	if sb.placed {
		t.Error("the bytes were copied into a sandbox that can already see them")
	}
}

// A sandbox with no filesystem in common is given the bytes.
func TestABlobIsPlacedWhereNothingIsShared(t *testing.T) {
	t.Parallel()

	sb := &placingSandbox{at: "/store/blobs/x"}

	got, err := placeBlob(context.Background(), sb, "/host/blobs/x")
	if err != nil {
		t.Fatal(err)
	}

	if got != "/store/blobs/x" || !sb.placed {
		t.Errorf("the blob was not placed: %s, placed=%v", got, sb.placed)
	}
}

// **Placing wins over seeing.** A sandbox implementing both would otherwise be
// told to share a path its guest cannot open, and the failure lands in the
// guest as a missing layer rather than here as a refusal.
func TestPlacingIsPreferredToSeeing(t *testing.T) {
	t.Parallel()

	sb := &bothSandbox{seeingSandbox{at: "/wrong"}, placingSandbox{at: "/right"}}

	got, err := placeBlob(context.Background(), sb, "/host/blobs/x")
	if err != nil {
		t.Fatal(err)
	}

	if got != "/right" {
		t.Errorf("the shared path won over the placed one: %s", got)
	}
}

// A sandbox that can do neither says so, naming the blob: a build that gets
// this far and reports nothing fails inside the guest looking for a layer.
func TestASandboxThatCanDoNeitherSaysSo(t *testing.T) {
	t.Parallel()

	_, err := placeBlob(context.Background(), &plainSandbox{}, "/host/blobs/deadbeef")
	if err == nil {
		t.Fatal("a blob was handed to a sandbox that cannot take one")
	}

	if !strings.Contains(err.Error(), "deadbeef") {
		t.Errorf("the refusal does not name the blob: %v", err)
	}
}

// A guest that cannot see the path is a refusal too, not an empty string
// passed on as if it were a path.
func TestAnInvisiblePathIsRefused(t *testing.T) {
	t.Parallel()

	_, err := placeBlob(context.Background(), &seeingSandbox{}, "/host/blobs/deadbeef")
	if err == nil {
		t.Fatal("an invisible path was handed to the guest")
	}
}

type plainSandbox struct{}

func (plainSandbox) Start(context.Context) (Conn, error) { return nil, errors.New("no") }
func (plainSandbox) Stop() error                         { return nil }
func (plainSandbox) StoreDir() string                    { return "" }
func (plainSandbox) Confines() bool                      { return true }

type seeingSandbox struct {
	plainSandbox

	at     string
	placed bool
}

func (s *seeingSandbox) GuestPath(string) (string, bool) { return s.at, s.at != "" }

type placingSandbox struct {
	plainSandbox

	at     string
	placed bool
}

func (s *placingSandbox) PlaceBlob(context.Context, string) (string, error) {
	s.placed = true

	return s.at, nil
}

type bothSandbox struct {
	seeingSandbox
	placingSandbox
}

func (b *bothSandbox) StoreDir() string { return "" }
func (b *bothSandbox) Confines() bool   { return true }
func (b *bothSandbox) Stop() error      { return nil }
func (b *bothSandbox) Start(ctx context.Context) (Conn, error) {
	return b.seeingSandbox.Start(ctx)
}
