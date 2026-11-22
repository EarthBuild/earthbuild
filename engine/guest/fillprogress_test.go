package guest_test

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// TestAskingTheHostHowFarABlobHasGot.
//
// **A guest unpacking a blob as it arrives has to know how far the host has
// written**, and asking the shared filesystem gave an answer about 460ms old -
// so the guest spent the fetch waiting rather than unpacking, and streaming
// bought nothing at all (E688).
//
// The channel to ask on already exists. A fault-in is the one message that
// travels guest-to-host, over a socket with no filesystem in it, and this is a
// second question asked the same way: how far is this blob, given that I have
// already read `have`? The host answers when there is something to say, so the
// guest waits on a wakeup rather than on a poll.
func TestAskingTheHostHowFarABlobHasGot(t *testing.T) {
	t.Parallel()

	t.Run("the host says how far", func(t *testing.T) {
		t.Parallel()

		here, there := net.Pipe()

		go func() {
			_ = guest.ServeFillsAnd(there,
				func(string, string) error { return nil },
				func(blob string, have int64) (int64, error) {
					if blob != "sha256-abc" {
						return 0, errors.New("asked about the wrong blob: " + blob)
					}

					return have + 4096, nil
				})
		}()

		f := guest.NewFills(here)

		n, err := f.Progress("sha256-abc", 8192)
		if err != nil {
			t.Fatal(err)
		}

		if n != 12288 {
			t.Errorf("the host said %d, want 12288", n)
		}
	})

	t.Run("a fetch that failed is not a fetch that is slow", func(t *testing.T) {
		t.Parallel()

		here, there := net.Pipe()

		go func() {
			_ = guest.ServeFillsAnd(there,
				func(string, string) error { return nil },
				func(string, int64) (int64, error) {
					return 0, errors.New("the registry hung up")
				})
		}()

		f := guest.NewFills(here)

		_, err := f.Progress("sha256-abc", 0)
		if err == nil || !strings.Contains(err.Error(), "hung up") {
			t.Errorf("a failed fetch came back as %v, want the reason", err)
		}
	})

	// A host that was never given a progress answerer must say so rather than
	// hang: an older guest paired with a newer host, or a sandbox that does not
	// stream, would otherwise wait for an answer nobody is going to give.
	t.Run("a host that cannot answer says so", func(t *testing.T) {
		t.Parallel()

		here, there := net.Pipe()

		go func() {
			_ = guest.ServeFills(there, func(string, string) error { return nil })
		}()

		f := guest.NewFills(here)

		done := make(chan error, 1)

		go func() {
			_, err := f.Progress("sha256-abc", 0)
			done <- err
		}()

		select {
		case err := <-done:
			if err == nil {
				t.Error("a host with no answerer reported progress anyway")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("asking a host that cannot answer hung, which is the one" +
				"\n  outcome a reader must never have")
		}
	})
}
