package core_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// occupancy records how many steps were inside Run at once.
type occupancy struct {
	mu   sync.Mutex
	now  int
	most int
}

func (o *occupancy) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	o.mu.Lock()
	o.now++

	if o.now > o.most {
		o.most = o.now
	}

	o.mu.Unlock()

	// Actually held. The first version of this sent to a *buffered* channel
	// and called it "held long enough that a scheduler willing to overlap
	// would" - which returned instantly, so no two steps ever overlapped and
	// the test blamed the scheduler for being serial. A probe that does not do
	// what its comment says is the third one this session (E97, E128, E136).
	time.Sleep(40 * time.Millisecond)

	o.mu.Lock()
	o.now--
	o.mu.Unlock()

	return core.Result{Layer: n.ID(), Captured: true}, nil
}

func (o *occupancy) peak() int {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.most
}

// Parallelism bounds how many steps run at once, and nothing had checked it.
//
// `Scheduler.Parallelism` is read in one place - `limit := s.Parallelism` - and
// set in none: not by the front end, which leaves it zero for NumCPU, and not by
// any test. So *"bounds how many steps run at once"* was a sentence with no
// evidence behind it.
//
// **Sixth instance this session of written-and-unreachable**, after flattening
// (E49), the view (E114), Κ₂ (E125), placement (E130) and `Verify` (E135). The
// port register calls this one inert with the reason *"zero means NumCPU, which
// is a documented default"* - true of the zero value and silent about whether
// the non-zero path works.
//
// It matters beyond tidiness. A build that ignored the bound would run every
// ready step at once, and E54's leading hypothesis for an intermittent
// `fork/exec ... operation not permitted` is exactly the pressure of too many
// sandboxes at a time.
func TestParallelismBoundsWhatRunsAtOnce(t *testing.T) {
	t.Parallel()

	// Six independent steps under a merge: all ready at once, so nothing but
	// the limit decides how many overlap.
	graph := func() *ir.Graph {
		var in []*ir.Node

		for i := range 6 {
			in = append(in, &ir.Node{
				Op: ir.Op{Kind: ir.OpExec, Args: []string{testLeaf, string(rune('a' + i))}},
			})
		}

		return &ir.Graph{Root: &ir.Node{
			Op: ir.Op{Kind: ir.OpMerge, Args: []string{"all"}}, Inputs: in,
		}}
	}

	for _, limit := range []int{1, 2, 3} {
		t.Run(map[int]string{1: "one at a time", 2: "two", 3: "three"}[limit], func(t *testing.T) {
			t.Parallel()

			o := &occupancy{}

			s := &core.Scheduler{
				Workers:     []core.Worker{{ID: "w", IsInvoker: true}},
				Executor:    o,
				Cache:       newMemCache(),
				Blobs:       allBlobs{},
				Parallelism: limit,
				Writer:      testStep,
				Record:      &core.Record{},
			}

			_, err := s.Run(context.Background(), graph())
			if err != nil {
				t.Fatal(err)
			}

			if got := o.peak(); got > limit {
				t.Errorf("Parallelism=%d and %d steps ran at once:"+
					"\n  a build that ignores the bound oversubscribes the machine,"+
					"\n  which is E54's leading explanation for an intermittent"+
					"\n  `fork/exec ... operation not permitted`", limit, got)
			}

			// And it is a bound rather than a serialisation: with six ready
			// steps and a limit above one, the limit must actually be reached
			// or the field is doing more than it claims.
			if limit > 1 && o.peak() < 2 {
				t.Errorf("Parallelism=%d never got past one step at a time,"+
					" so the scheduler is serial and the bound is not what"+
					" decides it", limit)
			}
		})
	}
}

// Zero means NumCPU, which is the documented default and the one production
// uses.
//
// Asserted rather than assumed, because "zero means unbounded" and "zero means
// one" are both plausible readings of an unset int, and the difference between
// them is a build that runs nothing in parallel or one that runs everything.
func TestZeroParallelismIsNotSerialAndNotUnbounded(t *testing.T) {
	t.Parallel()

	o := &occupancy{}

	var in []*ir.Node

	for i := range 4 {
		in = append(in, &ir.Node{
			Op: ir.Op{Kind: ir.OpExec, Args: []string{"z", string(rune('a' + i))}},
		})
	}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: o,
		Cache:    newMemCache(),
		Blobs:    allBlobs{},
		Writer:   testStep,
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: &ir.Node{
		Op: ir.Op{Kind: ir.OpMerge, Args: []string{"all"}}, Inputs: in,
	}})
	if err != nil {
		t.Fatal(err)
	}

	if o.peak() < 2 {
		t.Errorf("an unset Parallelism ran %d step at a time, so the default is"+
			" serial rather than NumCPU", o.peak())
	}
}
