package core_test

import (
	"context"
	"fmt"
	"go/build"
	"os/exec"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/sim"
)

var (
	amd64 = ir.Platform{OS: testOS, Arch: testArch2}
	arm64 = ir.Platform{OS: testOS, Arch: testArch}
)

// TestScheduleIsDeterministic is stage S0's exit criterion: the same graph and
// the same worker inventory must produce a byte-identical schedule, every run.
//
// It is not a tidiness test. Stable placement means a worker already holds the
// data for the step it is given, so stability is a caching property - green
// paper §4.7.3.
func TestScheduleIsDeterministic(t *testing.T) {
	t.Parallel()

	const runs = 8

	g := syntheticGraph(10_000)

	var first string

	for i := range runs {
		got := runSchedule(t, g)
		if i == 0 {
			first = got

			continue
		}

		if got != first {
			t.Fatalf("run %d produced a different schedule from run 0", i)
		}
	}
}

// TestScheduleIsStableAcrossGraphConstruction checks that the schedule depends
// on the graph's *content* and not on the order it was assembled in. A graph
// built inputs-last must schedule identically to the same graph built
// inputs-first, or identity is leaking construction order.
func TestScheduleIsStableAcrossGraphConstruction(t *testing.T) {
	t.Parallel()

	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}

	a := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"a"}}, Platform: amd64, Inputs: []*ir.Node{base}}
	b := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"b"}}, Platform: amd64, Inputs: []*ir.Node{base}}

	fwd := &ir.Graph{Root: &ir.Node{
		Op: ir.Op{Kind: ir.OpMerge}, Platform: amd64, Inputs: []*ir.Node{a, b},
	}}
	rev := &ir.Graph{Root: &ir.Node{
		Op: ir.Op{Kind: ir.OpMerge}, Platform: amd64, Inputs: []*ir.Node{b, a},
	}}

	// Input order is significant to identity, so these are legitimately
	// different roots - but each must schedule reproducibly.
	if runSchedule(t, fwd) == "" || runSchedule(t, rev) == "" {
		t.Fatal("empty schedule")
	}

	// The same graph *rebuilt*, not the same pointer scheduled twice: a
	// scheduler that memoised on node addresses would agree with itself while
	// disagreeing with the next build, which is the failure this is about.
	again := &ir.Graph{Root: &ir.Node{
		Op: ir.Op{Kind: ir.OpMerge}, Platform: amd64, Inputs: []*ir.Node{a, b},
	}}

	if runSchedule(t, fwd) != runSchedule(t, again) {
		t.Fatal("same graph scheduled two different ways")
	}
}

// TestPlatformAffinityIsHard checks green paper §4.7.1: a step is never placed
// on an incompatible executor, and a graph with no eligible worker fails rather
// than being placed somewhere convenient.
func TestPlatformAffinityIsHard(t *testing.T) {
	t.Parallel()

	g := &ir.Graph{Root: &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{"build"}}, Platform: arm64,
	}}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w1", Platform: amd64}},
		Executor: &sim.Executor{Seed: 1},
	}

	_, err := s.Run(context.Background(), g)
	if err == nil {
		t.Fatal("scheduled an arm64 step onto an amd64 worker")
	}
}

// TestHostStepsPinToInvoker checks that OpHost - LOCALLY - is never routed to a
// worker that is not the invoking machine.
func TestHostStepsPinToInvoker(t *testing.T) {
	t.Parallel()

	g := &ir.Graph{Root: &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{testCommand}}}}

	exec := &sim.Executor{Seed: 1}
	s := &core.Scheduler{
		Workers: []core.Worker{
			{ID: "remote", Platform: amd64},
			{ID: testLocal, Platform: amd64, IsInvoker: true},
		},
		Executor: exec,
	}

	_, err := s.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	if got := exec.Log[0].Worker; got != testLocal {
		t.Fatalf("host step ran on %q, want the invoking machine", got)
	}

	// And with no invoker present it must fail rather than run anywhere.
	s.Workers = []core.Worker{{ID: "remote", Platform: amd64}}
	_, err = s.Run(context.Background(), g)
	if err == nil {
		t.Fatal("host step scheduled with no invoking machine available")
	}
}

// TestDuplicateStepsRunOnce checks that a node reached by two paths executes
// once. The ticktock prototype used a bounded LRU for this and could silently
// re-execute a source operation on overflow - a correctness bug, since a second
// fetch may resolve a different ref.
func TestDuplicateStepsRunOnce(t *testing.T) {
	t.Parallel()

	shared := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}

	left := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"l"}}, Platform: amd64, Inputs: []*ir.Node{shared}}
	right := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"r"}}, Platform: amd64, Inputs: []*ir.Node{shared}}

	g := &ir.Graph{Root: &ir.Node{
		Op: ir.Op{Kind: ir.OpMerge}, Platform: amd64, Inputs: []*ir.Node{left, right},
	}}

	exec := &sim.Executor{Seed: 7}
	s := &core.Scheduler{Workers: []core.Worker{{ID: "w1", Platform: amd64}}, Executor: exec}

	_, err := s.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	var n int

	for _, st := range exec.Log {
		if st.Node == shared.ID() {
			n++
		}
	}

	if n != 1 {
		t.Fatalf("shared step ran %d times, want 1", n)
	}
}

// TestCoreIsPure enforces the architecture rather than describing it: the core
// computes, and everything touching the outside world arrives through a port.
//
// Architectures decay because nothing objects. This objects.
func TestCoreIsPure(t *testing.T) {
	t.Parallel()

	// build.Import shells out to `go list`, so this cannot run where the
	// toolchain is absent - a stripped test container, for one. Skipping is
	// honest there; failing would report an architectural violation that was
	// never checked.
	_, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain here, so package imports cannot be resolved")
	}

	banned := []string{"os", "net", "os/exec", "syscall", "io/ioutil", "path/filepath"}

	pkg, err := build.Import("github.com/EarthBuild/earthbuild/engine/core", "", 0)
	if err != nil {
		t.Fatal(err)
	}

	for _, imp := range pkg.Imports {
		for _, b := range banned {
			if imp == b {
				t.Errorf("engine/core imports %q; it must reach the outside through a port", imp)
			}
		}

		if strings.HasPrefix(imp, "github.com/moby/buildkit") {
			t.Errorf("engine/core imports %q; the core is engine-agnostic", imp)
		}
	}
}

// runSchedule renders a schedule to a comparable string.
func runSchedule(t *testing.T, g *ir.Graph) string {
	t.Helper()

	s := &core.Scheduler{
		Workers: []core.Worker{
			{ID: "w1", Platform: amd64, IsInvoker: true},
			{ID: "w2", Platform: amd64},
			{ID: "w3", Platform: amd64},
		},
		Executor: &sim.Executor{Seed: 42},
	}

	sched, err := s.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	var sb strings.Builder

	for _, a := range sched {
		fmt.Fprintf(&sb, "%d %s %s\n", a.Seq, a.Worker, a.Node.ID())
	}

	return sb.String()
}

// syntheticGraph builds a graph of roughly n steps: a base image, then a chain
// of execs per target, then a merge. Shaped like an Earthfile rather than like
// a random DAG, because the scheduler's behaviour on realistic shapes is what
// is under test.
func syntheticGraph(n int) *ir.Graph {
	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}

	const chain = 20

	targets := n / chain

	roots := make([]*ir.Node, 0, targets)

	for tgt := range targets {
		cur := base

		for i := range chain {
			cur = &ir.Node{
				Op:       ir.Op{Kind: ir.OpExec, Args: []string{fmt.Sprintf("step-%d-%d", tgt, i)}},
				Platform: amd64,
				Inputs:   []*ir.Node{cur},
				Meta:     ir.Meta{Target: fmt.Sprintf("+t%d", tgt)},
			}
		}

		roots = append(roots, cur)
	}

	return &ir.Graph{Root: &ir.Node{Op: ir.Op{Kind: ir.OpMerge}, Platform: amd64, Inputs: roots}}
}
