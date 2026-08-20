package ir_test

import (
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A node's identity may be asked for from several goroutines at once.
//
// ID memoises, so the first caller writes the field every later caller reads.
// The scheduler happens to walk the whole graph once before it fans out, which
// fills every memo while still single-threaded - but that is an ordering
// invariant nobody wrote down, and it does not hold for a node reached by two
// schedulers, which is exactly what a shared subgraph is.
//
// The value written is the same either way, so this never produces a wrong
// digest; it is a data race, which the memory model does not oblige to be
// harmless, and it makes -race unusable for anything that shares a graph.
//
// Found by parallelising the test suite: two tests over one package-level
// fixture are two goroutines over one node.
func TestIdentityCanBeAskedForConcurrently(t *testing.T) {
	t.Parallel()

	// A chain, so the recursion into inputs races too, not just the root.
	n := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"base"}}}
	for i := range 8 {
		n = &ir.Node{
			Op:      ir.Op{Kind: ir.OpExec, Args: []string{"step"}},
			Inputs:  []*ir.Node{n},
			Sources: []*ir.Node{{Op: ir.Op{Kind: ir.OpImage, Args: []string{"src"}}}},
			Meta:    ir.Meta{Source: "Earthfile:" + string(rune('a'+i))},
		}
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[ir.NodeID]bool{}
	)

	for range 16 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			id := n.ID()

			mu.Lock()
			defer mu.Unlock()

			seen[id] = true
		}()
	}

	wg.Wait()

	if len(seen) != 1 {
		t.Errorf("concurrent callers computed %d different identities", len(seen))
	}
}
