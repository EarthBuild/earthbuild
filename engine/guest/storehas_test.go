package guest_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The host learns what the store holds by asking, not by looking.
//
// Every store question until now was answered on the host with a stat, which
// worked because the store was a directory both sides could see. It is the
// assumption the disk removes: a store on a device the guest owns is not on the
// host's filesystem at all, and a host that stats it reads an empty answer and
// rebuilds everything it already had.
//
// So the question crosses the wire, and this is the first one that does.
func TestTheStoreAnswersWhatItHolds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	held := ir.NodeID{1}
	absent := ir.NodeID{2}

	err := os.MkdirAll(filepath.Join(root, "layers", held.String()), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	c := pairWith(t, &guest.Server{LayerDir: root})

	got, err := c.StoreHas(context.Background(), []ir.NodeID{held, absent})
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0] != held {
		t.Fatalf("the store holds %s and not %s, and reported %v",
			held, absent, got)
	}
}

// An empty question costs no round trip.
//
// The scheduler asks about the layers a step needs, and a step whose base is
// already materialised needs none. Sending that is a round trip - on a real
// backend, into a VM - for an answer that is known before asking.
//
// Proved with a context that is already cancelled: anything that reached the
// wire would come back as that cancellation, so returning cleanly is the
// evidence that nothing was sent.
func TestAskingAboutNoLayersDoesNotAsk(t *testing.T) {
	t.Parallel()

	c := pairWith(t, &guest.Server{LayerDir: t.TempDir()})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := c.StoreHas(ctx, nil)
	if err != nil {
		t.Fatalf("asking about no layers reached the wire: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("asked about nothing and was told about %v", got)
	}
}

// A guest with no store says so, rather than answering from where it stands.
//
// `DirStore("")` joins to `layers/<id>`, which is relative: the answer would
// come from the process's working directory, and "not held" from the wrong
// place reads exactly like "not held". The build would rebuild everything it
// already had and report success, which is the failure this engine spends most
// of its invariants avoiding.
func TestAGuestWithNoStoreRefusesToGuess(t *testing.T) {
	t.Parallel()

	c := pairWith(t, &guest.Server{})

	_, err := c.StoreHas(context.Background(), []ir.NodeID{{1}})
	if err == nil {
		t.Fatal("a guest with no layer directory answered a store question")
	}

	if !strings.Contains(err.Error(), "without a layer directory") {
		t.Fatalf("the diagnosis does not say what is wrong: %v", err)
	}
}

// A store that answers about a layer nobody asked about is not believed.
//
// The caller's use of this is "the cache's claim is backed by something that is
// really there". A reply naming an id outside the question could confirm a
// claim against a layer that was never checked, which is the one failure mode
// the check exists to close (green paper §5.3, A5: a peer's reply is data).
func TestAStoreThatAnswersAboutOtherLayersIsNotBelieved(t *testing.T) {
	t.Parallel()

	c := dialFrom(t, func(req guest.Request) guest.Response {
		resp := guest.Response{ID: req.ID}

		if req.Kind == guest.KindHello {
			resp.Version = guest.Version
		} else {
			// Every other question gets the same answer: one id of the
			// double's own, which is not the id anybody asked about.
			resp.Held = []string{ir.NodeID{9}.String()}
		}

		return resp
	})

	_, err := c.StoreHas(context.Background(), []ir.NodeID{{1}})
	if err == nil {
		t.Fatal("a store's answer about a layer nobody asked about was accepted")
	}

	if !strings.Contains(err.Error(), "not") {
		t.Fatalf("the diagnosis does not say what is wrong: %v", err)
	}
}

// dialFrom runs a guest that answers each request with reply, and dials it.
//
// Framed by hand because the protocol is a u32 length prefix and a JSON body: a
// double that speaks a different framing hangs rather than fails, which is a
// test that never finishes rather than one that reports.
func dialFrom(t *testing.T, reply func(guest.Request) guest.Response) *guest.Client {
	t.Helper()

	hostSide, guestSide := net.Pipe()

	t.Cleanup(func() { _ = hostSide.Close(); _ = guestSide.Close() })

	go func() {
		for {
			var hdr [4]byte

			_, err := io.ReadFull(guestSide, hdr[:])
			if err != nil {
				return
			}

			body := make([]byte, binary.BigEndian.Uint32(hdr[:]))

			_, err = io.ReadFull(guestSide, body)
			if err != nil {
				return
			}

			var req guest.Request

			err = json.Unmarshal(body, &req)
			if err != nil {
				return
			}

			b, err := json.Marshal(reply(req))
			if err != nil {
				return
			}

			binary.BigEndian.PutUint32(hdr[:], uint32(len(b))) // a marshalled reply

			_, err = guestSide.Write(append(hdr[:], b...))
			if err != nil {
				return
			}
		}
	}()

	c, err := guest.Dial(hostSide)
	if err != nil {
		t.Fatal(err)
	}

	return c
}
