package guest

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// A guest reachable only through one stdio pair can still be asked for a fault.
//
// A fault travels the wrong way: every other message is the host asking the
// guest. A sandbox that spawns its guest as a child passes a second descriptor;
// one that reaches its guest through a VM cannot, and `container exec` gives one
// stdio pair per invocation. So the guest listens and a relay - a second exec -
// carries the bytes.
//
// This is what makes a darwin worker able to fault in at all. Without it the
// worker says so and takes whole layers, which is slower and correct (E305).
func TestAFaultReachesTheHostThroughARelay(t *testing.T) {
	t.Parallel()

	// Short on purpose: a unix socket path lives in a 104-byte field, and
	// t.TempDir() alone is longer than that on darwin.
	at := shortSocketPath(t)

	var (
		wg     sync.WaitGroup
		guestC interface{ Close() error }
	)

	wg.Add(1)

	go func() {
		defer wg.Done()

		c, err := ListenForFills(at)
		if err != nil {
			t.Errorf("listen: %v", err)

			return
		}

		guestC = c

		// The guest end: ask for one path and report what came back.
		f := NewFills(c)

		if err := f.Fill("/usr/bin/needed"); err != nil {
			t.Errorf("fault in: %v", err)
		}
	}()

	// The relay is a pipe pair in this test; in a sandbox it is a second exec.
	hostSide, relaySide := relayPipes(t)

	relayErr := make(chan error, 1)

	go func() { relayErr <- RelayFills(at, relaySide.Reader, relaySide.Writer) }()

	// The host end: answer the fault.
	dec := json.NewDecoder(hostSide.Reader)
	enc := json.NewEncoder(hostSide.Writer)

	var asked struct {
		ID     uint64 `json:"id"`
		Handle string `json:"handle"`
		Path   string `json:"path"`
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		if err := dec.Decode(&asked); err != nil {
			t.Errorf("read the fault: %v", err)

			return
		}

		_ = enc.Encode(map[string]any{"id": asked.ID})
	}()

	select {
	case <-done:
	case e := <-relayErr:
		t.Fatalf("the relay stopped: %v", e)

	case <-time.After(10 * time.Second):
		t.Fatal("the host was never asked: a fault did not cross the relay")
	}

	wg.Wait()

	if guestC != nil {
		_ = guestC.Close()
	}

	if asked.Path != "/usr/bin/needed" {
		t.Errorf("the host was asked for %q, want the path the step needed", asked.Path)
	}
}
