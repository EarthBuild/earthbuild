package fleet

import (
	"context"
	"io"
	"sync/atomic"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// warmable is a source that can be opened before it is needed.
type warmable struct{ warmed atomic.Int64 }

func (w *warmable) Name() string { return "warmable" }

func (w *warmable) Fetch(context.Context, []ir.NodeID) (map[ir.NodeID]io.Reader, error) {
	return nil, nil
}

func (w *warmable) Warm(context.Context) { w.warmed.Add(1) }

// cold is a source with no such notion, which most are.
type cold struct{}

func (cold) Name() string { return "cold" }

func (cold) Fetch(context.Context, []ir.NodeID) (map[ir.NodeID]io.Reader, error) {
	return nil, nil
}

// TestHoldersAreOpenedBeforeTheyAreNeeded.
//
// **Reaching a peer costs more than reading from it.** Measured on GitHub:
// 7.9 MiB read in 302ms, and 403ms to 3363ms spent getting to the machine that
// had it - discovery, handshake, hole punching, none of it proportional to what
// is being fetched (E-F1). A fleet's cost on a small build is almost entirely
// this, and it is paid on the critical path because a connection is opened by
// the first fetch that wants one.
//
// The holders are known one line after an assignment arrives, which on the
// prime is well before any step needs them. Opening them there costs nothing
// and takes the whole of it off the path.
func TestHoldersAreOpenedBeforeTheyAreNeeded(t *testing.T) {
	t.Parallel()

	w := &warmable{}

	// A source that cannot be warmed must not stop the ones that can, and must
	// not panic: `unreachable` is a Source and has no connection at all.
	warmAll(t.Context(), []Source{cold{}, w, cold{}})

	if got := w.warmed.Load(); got != 1 {
		t.Errorf("a holder was opened %d time(s), want 1 - so the first fetch"+
			" pays to reach it", got)
	}

	// Nothing to do, and nothing to trip over.
	warmAll(t.Context(), nil)
}
