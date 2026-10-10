package guestd

import (
	"net/http"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The agent listens, serves, and holds itself open while it does.
//
// **Separate from the handler's own tests, because this is the wiring.** The
// handler is tested in engine/remote; what is untested until here is that the
// agent starts a listener at all, hands it the store it owns, and passes the
// hold that keeps the machine from stopping mid-request. Each of those is a
// line that can be dropped without any other test noticing.
func TestTheAgentServesAndHoldsItselfOpen(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	var held int

	stop, err := serveCache(t.TempDir(), "127.0.0.1:0", func() func() {
		held++

		return func() {}
	})
	if err != nil {
		t.Fatal(err)
	}

	defer stop()

	at := listenedOn(t)

	resp, err := http.Get("http://" + at + "/cas/" + ir.NodeID{1}.String())
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("an empty store answered %s, want 404", resp.Status)
	}

	if held == 0 {
		t.Error("the request did not hold the machine open, so the agent can" +
			" stop itself while a client is waiting on it")
	}
}

// A store hashed the other way refuses to serve rather than missing silently.
func TestTheAgentRefusesToServeABlake3Store(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashBLAKE3)
	defer restore()

	if _, err := serveCache(t.TempDir(), "127.0.0.1:0", nil); err == nil {
		t.Error("a BLAKE3 store was served over a protocol that names blobs by" +
			" SHA-256, so every request misses and the cache looks empty")
	}
}

// listenedOn is where the last serveCache bound.
func listenedOn(t *testing.T) string {
	t.Helper()

	at, _ := serving.Load().(string)
	if at == "" {
		t.Fatal("the agent reported no address")
	}

	return at
}
