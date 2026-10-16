package exec

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fstime"
)

// EnvContextTimes decides what timestamps a packed build context carries.
//
// **`history`, the default.** Each committed file carries the time of the commit
// that last changed it, and each locally-modified one its mtime on disk. A
// commit time is a property of the history rather than of the clone, so two
// machines packing one commit still produce one archive; a modified working tree
// has no shared answer to preserve, so spending it there costs nothing.
//
// **`epoch` is what this did before**, and every entry gets one fixed stamp.
// Byte-identical archives from any tree with the same content - and a tree an
// incremental compiler cannot read, because cargo, make and ninja all decide
// what to rebuild by comparing mtimes, and a flat tree answers every comparison
// the same way. Set it where a context must pack identically whichever commit it
// came from.
//
// The default costs one L1 miss where two histories hold the same content under
// different commits - a rebase, a cherry-pick. L2 does not notice: its digest
// excludes mtimes by construction (`layer.PathDigest`), which is what makes the
// default affordable.
const EnvContextTimes = "EARTH_CONTEXT_TIMES"

func contextTimesFromHistory() bool {
	switch os.Getenv(EnvContextTimes) {
	case "", "history":
		return true
	default:
		return false
	}
}

// contextStamps memoises the history walk.
//
// A build has one context and many COPY steps, and each walk is a pass over the
// history, so asking per step asks one question a dozen times. The walk covers
// every tracked path under the root rather than the step's own names, so a later
// step is answered from the memo whatever it asks about - the alternative is
// recording which paths each walk covered, and a path in no commit at all (an
// ignored one) would send every later step back for another full walk.
type contextStamps struct {
	mu    sync.Mutex
	roots map[string]stampedTree
}

type stampedTree struct {
	head string
	at   func(rel string) time.Time
}

// stampsFor is the time each path under root should carry, or nil to leave the
// archive at the fixed epoch.
func (e *Executor) stampsFor(ctx context.Context, root string) func(rel string) time.Time {
	if !contextTimesFromHistory() {
		return nil
	}

	return e.stamps.of(ctx, root)
}

func (c *contextStamps) of(ctx context.Context, root string) func(rel string) time.Time {
	head := fstime.Head(ctx, root)

	c.mu.Lock()
	defer c.mu.Unlock()

	if was, ok := c.roots[root]; ok && was.head == head {
		return was.at
	}

	at := fstime.FromHistory(ctx, root, nil)

	if c.roots == nil {
		c.roots = map[string]stampedTree{}
	}

	c.roots[root] = stampedTree{head: head, at: at}

	return at
}
