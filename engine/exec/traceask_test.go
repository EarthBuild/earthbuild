package exec_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step asks to be observed, and an interactive one does not.
//
// `Trace` is the only observation source a `RUN` has: a step that is not traced
// can be built and cached and can never be reused against a base it did not run
// on. It costs a round trip per path call - 8.4µs against 1.0µs, measured at 8x
// on a path operation (E213) - which is why it is off for an interactive step,
// where every keystroke's worth of shell completion would trap and nobody is
// producing a layer anybody will reuse.
//
// Both halves of that had no test at all. `Trace: !n.Op.Interactive` is one line
// with two claims in it, and **a rule that cannot fire is indistinguishable from
// one that is satisfied**: deleting it would have left every test in this
// repository green while the L2 tier quietly stopped having anything to work
// from (E480).
//
// Read off the wire rather than from a helper. The request is what the guest
// acts on, so the request is the observable - a predicate tested on its own
// passes while the field it feeds is dropped one line later, which is exactly
// how E465's project files were being tested.
func TestAStepAsksToBeObservedUnlessItIsInteractive(t *testing.T) {
	t.Parallel()

	for name, interactive := range map[string]bool{
		"an ordinary step":   false,
		"an interactive one": true,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			asked, ok := traceAskedFor(t, interactive)
			if !ok {
				t.Fatal("no exec request reached the guest, so this test is" +
					" watching the wrong thing")
			}

			if asked == interactive {
				t.Errorf("interactive=%v and the step asked for trace=%v",
					interactive, asked)
			}
		})
	}
}

// traceAskedFor runs one step through a real guest and reports what its exec
// request said about tracing.
func traceAskedFor(t *testing.T, interactive bool) (asked, found bool) {
	t.Helper()

	tap := &requestTap{Conn: exec.LoopbackConn()}

	e, err := exec.New(&tappedSandbox{conn: tap, store: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = e.Close() })

	n := &ir.Node{Op: ir.Op{
		Kind:        ir.OpExec,
		Args:        []string{"/bin/sh", "-c", "true"},
		Interactive: interactive,
	}}

	// The result is not the point and the loopback guest cannot run anything
	// real: what is under test is what was *asked for*, which is on the wire
	// before any of that matters.
	_, _ = e.Run(context.Background(), n, core.Worker{}, nil, nil)

	return tap.trace()
}

// requestTap forwards the protocol and reads the exec requests going past.
type requestTap struct {
	exec.Conn

	mu     sync.Mutex
	asked  bool
	sawOne bool
}

func (t *requestTap) Write(p []byte) (int, error) {
	var req struct {
		Kind  string `json:"kind"`
		Trace bool   `json:"trace"`
	}

	// One request per write is how this protocol is framed; a decoder over the
	// stream would be the same answer with a goroutine.
	for line := range strings.SplitSeq(string(p), "\n") {
		if line == "" || json.Unmarshal([]byte(line), &req) != nil {
			continue
		}

		if req.Kind == "exec" {
			t.mu.Lock()
			t.asked, t.sawOne = req.Trace, true
			t.mu.Unlock()
		}
	}

	return t.Conn.Write(p)
}

func (t *requestTap) trace() (asked, found bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.asked, t.sawOne
}

// tappedSandbox hands out the one connection this test is listening to.
type tappedSandbox struct {
	conn  exec.Conn
	store string
}

func (s *tappedSandbox) Start(context.Context) (exec.Conn, error) { return s.conn, nil }
func (s *tappedSandbox) Stop() error                              { return nil }
func (s *tappedSandbox) StoreDir() string                         { return s.store }
func (s *tappedSandbox) Confines() bool                           { return true }

var _ io.Writer = (*requestTap)(nil)
