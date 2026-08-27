// Package core is the engine's pure half: the graph, the scheduler and the
// policies over them.
//
// It touches no file descriptor. Everything outside - execution, storage,
// transport, the clock - arrives through a port, so the whole package runs at
// memory speed against fakes and is exercised long before any of that exists
// (docs-internals/plan-native-engine.md §2.0, stage S0).
//
// The purity is enforced by TestCoreIsPure rather than by convention.
package core

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/timing"
)

// Worker is a place a step can run. Identity is a stable string so that
// placement can be reproduced across runs.
type Worker struct {
	ID        string
	Platform  ir.Platform
	IsInvoker bool // the machine that started the build; the only place OpHost may run
	// Emulates are platforms this machine can run through emulation - binfmt,
	// with qemu registered for the architecture - rather than natively.
	//
	// **A fallback and never a preference.** A machine of the architecture runs
	// the step; one that can only emulate it runs the step when there is no such
	// machine. Emulated work runs on the order of a hundred times slower - every
	// instruction through an interpreter - so no amount of load on the native
	// machine makes the emulator the better answer. This widens what a build *can* do without
	// changing what it does when it has the choice.
	Emulates []ir.Platform
}

// canEmulate reports whether this machine can run that platform under emulation.
func (w Worker) canEmulate(p ir.Platform) bool {
	return slices.Contains(w.Emulates, p)
}

// Executor runs one step against a base stack, reading from zero or more
// sources.
//
// sources are the *result layers* of the step's Sources, in order - not their
// node identities. The two coincide for a staged build context, whose layer is
// named after its node, and diverge for an artifact from another target, whose
// layer is whatever that target produced. Passing node identities worked until
// the first artifact copy and then looked for a layer that had never existed.
//
// **Run is called concurrently.** Independent steps are evaluated at the same
// time, so an implementation with any shared state needs its own lock. This is
// a real obligation rather than a note: the simulator had an unguarded slice
// append and the race detector found it the moment the scheduler stopped being
// serial.
//
// The base is passed rather than a materialised handle, because on a real
// backend the executor is inside a VM and the scheduler cannot see its
// filesystem at all (experiment E1b). Naming the layers lets whichever side
// owns the filesystem assemble them; handing over a handle would assume the two
// share one.
type Executor interface {
	// sources are the *stacks* of the step's Sources, in order. Stacks rather
	// than single layers, because an artifact need not be produced by its
	// target's last step: a build that makes a jar, reads a version out of it,
	// and then saves the jar has that jar two layers down.
	Run(ctx context.Context, n *ir.Node, w Worker, base []ir.NodeID, sources [][]ir.NodeID) (Result, error)
}

// Result is what running a step yields. Green paper (3.3), reduced to what the
// scheduler reads: the observation set belongs to S5.
type Result struct {
	// Layer identifies the produced filesystem delta.
	Layer ir.NodeID

	// Layers is the stack this step produced, when it produced more than one.
	//
	// **An image is many layers.** A registry hands over one directory per
	// layer, and the puller merged them into one because a result could name
	// only one - which is why unpacking has to be serial (E641) and why nothing
	// can be assembled at once. A step that has several says so here, oldest
	// first, and every one of them joins the stack.
	//
	// Empty for almost every step: a RUN produces one delta, and Layer above
	// carries it.
	Layers []ir.NodeID
	// Declares is what this step says about how the steps after it should run -
	// an image's environment, working directory, user, entrypoint and command.
	// Zero when it says nothing, which is most steps.
	//
	// A stack element rather than a file beside the layer (green paper §3.2a).
	// That is what makes it travel, since a worker fetches every id in the stack,
	// and what puts it in ids(𝑏) so it reaches every key derived from the base
	// without an exception being made for it. A worker that received the
	// filesystem and not the declaration ran steps without the PATH their image
	// sets.
	Declares ir.NodeID
	// Exit is the step's exit code.
	Exit int
	// CPU and MaxRSS are what the step's process spent, zero where the platform
	// or the backend cannot say (E467).
	CPU    time.Duration
	MaxRSS uint64
	// Bytes is the output layer's size, which the cost model needs and which a
	// scheduler that estimates only time will get wrong on a fleet.
	Bytes int64
	// Streamed says the executor already showed this output to whoever is
	// watching, so an error about it should point rather than repeat (E73).
	Streamed bool
	// Output is what the step printed, truncated by the guest. Carried because a
	// step that failed and whose message was discarded is a step nobody can
	// diagnose - the engine would report an exit code and nothing else.
	Output string
	// Content is the layer digest with timestamps excluded. Determinism
	// screening (green paper §6) compares this rather than Layer: creating a
	// directory stamps it with the wall clock, so two runs of an identical step
	// differ in Layer while agreeing on Content.
	Content ir.NodeID
	// Captured says the Layer is a real digest of what the step produced,
	// rather than an absent one. Mirrors Observed, and for the same reason: a
	// zero value that means "nothing" is indistinguishable from one that means
	// "not measured", and the two must never be conflated in a cache.
	Captured bool
	// Observation is what the step looked at, and Observed says whether anyone
	// was watching. An unobserved step must not publish a Κ₂ entry: an empty
	// observation would claim the step read nothing, and every later step over
	// any base would falsely hit it.
	Observation Observation
	Observed    bool
}

// Assignment is one scheduling decision: a step, a worker and a position.
// Green paper (4.9) - 𝑔 : 𝕊 ⇀ (worker, ℕ).
type Assignment struct {
	Node   *ir.Node
	Worker string
	Seq    int
}

// Schedule is the ordered set of assignments a build produced.
type Schedule []Assignment

// ErrNoEligibleWorker reports that a step's hard constraints exclude every
// worker. It is a scheduling failure, never a silent placement elsewhere.
var ErrNoEligibleWorker = errors.New("no eligible worker")

// noWorkerFor explains which constraint excluded everything.
//
// "no eligible worker" is true and unusable: it names neither what the step
// asked for nor what this machine has, so the reader goes looking for a broken
// worker when what they have is a cross-platform build (I10 requires a refusal
// to say where and what to do). Two of this repository's own targets end here -
// `BUILD --platform=linux/amd64` on an arm64 machine - and the message sent
// them nowhere (E68).
func noWorkerFor(n *ir.Node, workers []Worker) error {
	if len(workers) == 0 {
		return fmt.Errorf("%w: this build has no workers at all", ErrNoEligibleWorker)
	}

	have := make([]string, 0, len(workers))
	for _, w := range workers {
		have = append(have, w.Platform.String())
	}

	// Sorted and deduplicated, because it is a message and a message is part of
	// what a build produces (I12, E66).
	sort.Strings(have)
	have = slices.Compact(have)

	want := n.Platform.String()

	// The platform matches and something else excluded them, so saying "the
	// platform is wrong" would send the reader somewhere there is nothing to
	// find.
	if slices.Contains(have, want) {
		return fmt.Errorf("%w: %d worker(s) run %s and none of them accepted this step",
			ErrNoEligibleWorker, len(workers), strings.Join(have, ", "))
	}

	return fmt.Errorf(
		"%w: this step is for %s and this build has %s"+
			"\n  building one architecture on another needs emulation, and no machine"+
			"\n  in this build offers it - register binfmt for %s, build the target"+
			"\n  for %s, or use --engine=buildkit",
		ErrNoEligibleWorker, want, strings.Join(have, ", "), want, have[0])
}

// Scheduler is Sched-1 from docs-internals/scheduling.md: correct,
// deterministic, and requiring no cost estimates.
//
// It respects every hard constraint in green paper §4.7.1 and makes no attempt
// at optimality. HEFT and locality scoring are Sched-2, which needs recorded
// durations and more than one worker to place work on.
type Scheduler struct {
	Workers  []Worker
	Executor Executor

	// Cache, Blobs and Trusted are the L1 lookup path. All are optional: with
	// no cache every step executes, which is slower and never wrong.
	Cache   ActionCache
	Blobs   BlobStore
	Trusted map[string]bool
	// Materialiser prepares a step's base before it runs. Optional: with none,
	// steps run against nothing, which is what stages before S3 do.
	Materialiser Materialiser
	// Profiles and Views are the L2 path. Both optional, and both required for
	// L2 to run at all: without a prediction there is nothing to check, and
	// without a view there is no way to check it.
	Profiles Profiles
	Views    ViewSource

	// MaxStack is the deepest stack a step may be given before Φ collapses its
	// oldest layers. Zero means MaxStackDepth.
	//
	// A field rather than the constant alone because the binding limit belongs
	// to whatever does the mounting, and it is not the one this constant
	// describes: overlayfs stops at 500 layers, and the mount option page stops
	// this engine's guest at about 90 (E49). The scheduler cannot know which
	// applies, so it is told.
	MaxStack int

	// Capabilities is what this engine can evaluate. Nil means no restriction.
	// A graph containing anything outside it is refused before any step runs
	// (green paper I10).
	Capabilities *Capabilities

	// Record is the build record this run produced, available after Run.
	Record *Record

	// NoCache builds everything, reading no entry that is already there.
	//
	// **Reads nothing, writes everything.** A build that ignored the cache in
	// both directions would leave the store as it found it, so the next build
	// would miss too - turning one instruction to redo the work into a project
	// whose cache never warms again. The instruction is about this build (E462).
	//
	// Distinct from `Op.NoCache`, which is a *step* the author marked: this is
	// the invocation saying so about all of them.
	NoCache bool
	// Parallelism bounds how many steps run at once. NumCPU when zero.
	Parallelism int
	// claims serialise steps sharing a `--sharing=locked` cache, before they
	// take a slot rather than after (E434).
	claims claims

	// mu guards everything below it: these are written by every step and read
	// by every other one.
	mu     sync.Mutex
	done   map[ir.NodeID]Result
	placed map[ir.NodeID]Worker
	load   map[string]int
	sched  Schedule
	stacks map[ir.NodeID][]ir.NodeID
	// nodes remembers which step produced which result, so an input that cannot
	// be obtained can be rebuilt rather than failing the build (E278). The
	// scheduler is the only party that knows: an executor holds a digest it
	// cannot fetch and nothing about how it was made.
	nodes map[ir.NodeID]*ir.Node
	// inputs is what each step was run with, so a step can be run again to
	// replace a layer that went out of reach.
	inputs map[ir.NodeID]ranWith
	// Writer identifies this engine when publishing entries.
	Writer string

	// tolerated collects failures that did not stop the build where they
	// happened, so it can fail once everything that had to run has run.
	tolerated []*StepError

	// failed and skipped are what a CATCH handler needs to know: whether the
	// step it guards went wrong, and whether anything it stands on was itself
	// skipped. Kept as sets rather than read back off the records, because a
	// record carries a step's outcome and these are questions about the build's
	// control flow.
	failed  map[ir.NodeID]bool
	skipped map[ir.NodeID]bool

	// Stats records what the lookup path decided, which is the cheapest
	// version of the cache-outcome telemetry Dagger's dagql emits.
	Stats Stats
}

// Stats counts lookup outcomes. Not a result, so it never affects one.
type Stats struct {
	Hits   int
	Misses int
	// CPU and MaxRSS are what this build's steps spent: the sum of their CPU
	// time, and the largest peak any single step reached.
	//
	// **Summed and maxed, because they are different quantities.** CPU adds up -
	// two steps each spending a second cost the machine two - and peak memory
	// does not: two steps each peaking at a gigabyte, run one after the other,
	// never needed two (E467).
	CPU    time.Duration
	MaxRSS uint64
	// L2Hits counts steps that missed on the chain key and hit on what they
	// actually read. This is the number that says what observed-input caching
	// is worth: every one is a rebuild avoided that L1 could not avoid.
	L2Hits int
	// L2Stale counts predictions that no longer described the base. High and
	// rising means the profiles are being invalidated faster than they are
	// useful.
	L2Stale int
	// StaleWhy is the first way a prediction stopped describing its base:
	// which path, and how it differed.
	//
	// The count says the tier is being invalidated and not by what, which is a
	// number that can be quoted and not acted on. The engine knows the path at
	// the moment it refuses (E127).
	StaleWhy string
	// L2Unpredicted, L2Empty and L2Unstored are the three ways the observed
	// tier declines *before* it decides a prediction is stale, and they were
	// each silent: a step that missed said nothing about which of them happened.
	//
	// Separated because the answers differ. Unpredicted is ordinary on a first
	// build; empty means the step will never be reusable; unstored means
	// everything the tier needed was true and there was nothing to serve, which
	// is a publish-side problem rather than a lookup-side one (E223).
	L2Unpredicted int
	L2Empty       int
	L2Unstored    int
	// L2UnpredictedAt is where such steps are written, distinct and sorted.
	//
	// A few rather than the first, because the first is often uninteresting -
	// on a corpus sweep it was reliably the perturbation the measurement had
	// just inserted, and the step actually worth looking at was the one behind
	// it (E224). Sorted so two runs of the same build report the same thing
	// (I12).
	L2UnpredictedAt []string
	// Uncacheable counts steps this engine refused to cache, and UncacheableAt
	// says which and why.
	//
	// The refusal is deliberate - a cache mount is shared mutable state that no
	// key bounds (I3), and this engine is stricter about it than BuildKit or
	// Earthly, both of which cache such a step with the mount left out of the
	// key. What was wrong was that it happened in silence: the step rebuilt
	// every build and nothing said so, and it took three experiments with an
	// instrumented scheduler to find out (E224-E226).
	//
	// A refusal that costs a minute a build belongs in the build that pays for
	// it (E228).
	Uncacheable   int
	UncacheableAt []string
	// Unobserved counts steps whose observation could not be used, so nothing
	// was stored for Κ₂ to find later. Distinct from a miss: a miss is a step
	// that could not be reused *this* time, while this is one that will not be
	// reusable next time either.
	Unobserved int
	// UnobservedWhy is the first reason one was unusable. A count on its own
	// says the tier is not working and not what to do about it - which is the
	// state this whole line of work kept rediscovering (E209, E215, E217).
	UnobservedWhy string
	// UnobservedWhere is where that step is written. The reason says what went
	// wrong and this says which line to look at, which is the difference
	// between a fact and a thing somebody can act on - and it cost a corpus run
	// and a guess to find out that the answer was WORKDIR (E219).
	UnobservedWhere string
	// Flattened counts steps whose base needed Φ. A build where this is
	// non-zero is one that would have failed outright on today's engine.
	Flattened int
}

// Run schedules and executes the graph, returning the schedule it chose.
//
// Determinism is the property under test at S0: the same graph and the same
// worker inventory must produce a byte-identical schedule, every run, on every
// machine (green paper §4.7.3). The two places that could leak nondeterminism -
// ready-set ordering and placement ties - are both broken by node identity.
func (s *Scheduler) Run(ctx context.Context, g *ir.Graph) (Schedule, error) {
	// Refuse first, so a partial engine never half-builds. A refusal after
	// three steps have run leaves a tree that is neither the old result nor the
	// new one, and a user with no way to tell which parts are real.
	err := s.Capabilities.Check(g)
	if err != nil {
		return nil, err
	}

	nodes := g.Nodes() // already deterministic: post-order, ties by identity

	// inflight tracks steps already run, so a node reached by two paths is
	// executed once. The ticktock prototype used a bounded LRU here and could
	// silently re-run a source operation on overflow; this is per-build state
	// with a natural lifetime, so it is a plain map.
	s.done = make(map[ir.NodeID]Result, len(nodes))
	s.failed = map[ir.NodeID]bool{}
	s.skipped = map[ir.NodeID]bool{}
	s.load = make(map[string]int, len(s.Workers))
	s.sched = nil
	// stacks[n] is the layer stack n's inputs sit on, before n's own result is
	// added. Depth accumulates along a chain, which is what reaches the limit.
	s.stacks = make(map[ir.NodeID][]ir.NodeID, len(nodes))

	// The build record is emitted by default from the first milestone with a
	// cache to explain: every mechanism that diffs, bisects or attributes
	// assumes it exists, and retrofitting a record format after four consumers
	// have grown their own is the expensive path.
	//
	// Allocated only when absent. Replacing a caller's record would leave it
	// holding a pointer to an empty one, which is indistinguishable from a build
	// that did nothing.
	if s.Record == nil {
		s.Record = &Record{}
	}

	// Evaluate concurrently, respecting dependencies.
	//
	// The prototype this replaces had a correct scheduler and a serial build
	// loop, which produced the right answer at the speed of one core. Wall-clock
	// is the whole point of a build tool, so this is not an optimisation.
	//
	// Determinism is preserved by construction, not by luck: the *order* steps
	// complete in reaches nothing. Results are keyed by node identity, the
	// record is sorted by graph position afterwards, and a failure is chosen by
	// graph position rather than by which goroutine lost the race. Green paper
	// (4.10) requires exactly this - any legal schedule yields the same
	// artefacts, and concurrency is a legal schedule.
	limit := s.Parallelism
	if limit <= 0 {
		limit = runtime.NumCPU()
	}

	indexOf := make(map[ir.NodeID]int, len(nodes))
	for i, n := range nodes {
		indexOf[n.ID()] = i
	}

	// remaining counts each node's unfinished inputs; dependents is the reverse
	// edge, so finishing a step can release exactly what it unblocked.
	remaining := make(map[ir.NodeID]int, len(nodes))
	dependents := make(map[ir.NodeID][]*ir.Node, len(nodes))

	for _, n := range nodes {
		seen := map[ir.NodeID]bool{}

		// Sources must finish too: a step cannot copy from a target that has not
		// been built. So must After, which is ordering alone - waited for, never
		// stacked and never keyed.
		deps := append([]*ir.Node{}, n.Inputs...)
		deps = append(deps, n.Sources...)
		deps = append(deps, n.After...)

		for _, in := range deps {
			if seen[in.ID()] {
				continue // a node reached twice is one dependency, not two
			}

			seen[in.ID()] = true
			remaining[n.ID()]++
			dependents[in.ID()] = append(dependents[in.ID()], n)
		}
	}

	// Placement happens *before* anything runs, in the graph's deterministic
	// order.
	//
	// It used to pick the least-loaded worker at the moment a step was reached,
	// which was deterministic only while the build was serial: with steps
	// finishing in whatever order they finish, observed load - and therefore the
	// schedule - varied run to run. Green paper §4.7.3 requires a byte-identical
	// schedule from the same inputs and worker inventory.
	//
	// Simulating the load in topological order keeps both properties: placement
	// is still load-aware, and it is a pure function of the graph.
	placed := make(map[ir.NodeID]Worker, len(nodes))

	for i, n := range nodes {
		w, err := s.place(n, s.load)
		if err != nil {
			// The source location as well as the description: `schedule  (image)`
			// is what this printed for a node whose description was empty, which
			// is every image node.
			return nil, fmt.Errorf("schedule %s (%s): %w", stepIdent(n), n.Op.Kind, err)
		}

		placed[n.ID()] = w
		s.load[w.ID]++
		s.sched = append(s.sched, Assignment{Node: n, Worker: w.ID, Seq: i})
	}

	s.placed = placed

	ready := make([]*ir.Node, 0, len(nodes))

	for _, n := range nodes {
		if remaining[n.ID()] == 0 {
			ready = append(ready, n)
		}
	}

	var (
		wg      sync.WaitGroup
		sem     = make(chan struct{}, limit)
		mu      sync.Mutex
		failure error
		failAt  = len(nodes) // graph position of the failure being reported
		queue   = ready
	)

	// Cancelled on the first failure, so work already started can stop rather
	// than finishing a build that has already lost.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var run func(n *ir.Node)

	run = func(n *ir.Node) {
		defer wg.Done()

		// The cache claims first, then the slot. A step queueing for a cache
		// must not be holding a slot while it does so, and the order is what
		// makes that safe: a slot is only ever held by a step that already has
		// its claims, so no slot is ever waiting for one (E434).
		defer s.claims.take(n.Op.Mounts)()

		sem <- struct{}{}
		defer func() { <-sem }()

		mu.Lock()
		stop := failure != nil
		mu.Unlock()

		if stop {
			return
		}

		err := s.evalNode(ctx, n, indexOf[n.ID()])

		mu.Lock()

		if err != nil {
			// The *earliest* failure in graph order, not the first to arrive: a
			// build that blames a different command depending on which goroutine
			// lost a race is a build nobody can act on - and a cancellation
			// never outranks the failure that caused it, however the order falls.
			failAt, failure = worseFailure(failure, failAt, err, indexOf[n.ID()])

			mu.Unlock()

			// Recorded here rather than only where a *tolerated* failure is,
			// because unwinding asks which steps failed and a hard failure is
			// still a failure. Without this the handlers guarding it were
			// skipped for having nothing to guard.
			s.mu.Lock()
			s.failed[n.ID()] = true
			s.mu.Unlock()

			cancel()

			return
		}

		var next []*ir.Node

		for _, d := range dependents[n.ID()] {
			remaining[d.ID()]--
			if remaining[d.ID()] == 0 {
				next = append(next, d)
			}
		}

		mu.Unlock()

		for _, d := range next {
			wg.Add(1)

			go run(d)
		}
	}

	for _, n := range queue {
		wg.Add(1)

		go run(n)
	}

	wg.Wait()

	if failure != nil {
		// Handlers before giving up. A step guarded by OnFailure exists to run
		// when the step it names fails - a CATCH that reports, a teardown that
		// takes away what the block started - and the build abandoning itself
		// at the failure meant none of them was ever reached. The only way to
		// get one to run was TRY's tolerance, which has no end: everything
		// downstream runs, including whatever follows the block.
		//
		// So a WITH DOCKER block whose body failed left its containers holding
		// their ports for every later build on the machine (E33). This is the
		// narrow version of tolerance: exactly the guarded steps, exactly once,
		// and the build still fails with the error it already had.
		s.unwind(ctx, nodes)

		return nil, failure
	}

	// Sorted by graph position, so two runs of one build produce identical
	// records however the goroutines interleaved. Every tool that diffs builds
	// depends on this.
	sort.Slice(s.Record.Steps, func(i, j int) bool {
		return s.Record.Steps[i].Seq < s.Record.Steps[j].Seq
	})

	// A tolerated failure is still a failure. Reported now that everything which
	// had to run has run - the first one, because it is the one that happened
	// and the others may only be its consequences.
	if len(s.tolerated) > 0 {
		return s.sched, &ToleratedFailureError{StepError: s.tolerated[0]}
	}

	return s.sched, nil
}

// unwind runs the handlers guarding steps that failed.
//
// Sequential, and on a context detached from the build's: the build's was
// cancelled the moment it failed, so anything run on it would fail instantly
// and the teardown would be a no-op that looked like one that ran.
//
// Errors are dropped. A teardown that fails has not made the build worse, and
// reporting it in place of the failure that caused it would replace the
// diagnosis with a footnote.
func (s *Scheduler) unwind(ctx context.Context, nodes []*ir.Node) {
	// Detached rather than fresh, so a caller's values still reach the
	// executor - a sandbox connection is looked up through them.
	ctx = context.WithoutCancel(ctx)

	for _, n := range nodes {
		if n.OnFailure == nil {
			continue
		}

		s.mu.Lock()
		guardFailed := s.failed[n.OnFailure.ID()]
		alreadyRun := false

		if _, done := s.done[n.ID()]; done {
			alreadyRun = true
		}

		s.mu.Unlock()

		if !guardFailed || alreadyRun {
			continue
		}

		_ = s.evalNode(ctx, n, 0)
	}
}

// pushLayer adds a layer to a stack, collapsing a repeat.
//
// Two steps producing identical output produce the same layer - the
// deduplication property working as intended - and the common case is two steps
// that write nothing, which both yield the empty layer. overlayfs refuses a
// repeated lowerdir with ELOOP, so a stack naming one twice cannot be mounted.
//
// Dropping the earlier occurrence is safe precisely because the layers are
// identical: same content, so which copy survives cannot matter. It also keeps
// stacks shorter, which is depth Φ does not have to flatten later.
func pushLayer(stack []ir.NodeID, id ir.NodeID) []ir.NodeID {
	out := make([]ir.NodeID, 0, len(stack)+1)

	for _, s := range stack {
		if s != id {
			out = append(out, s)
		}
	}

	return append(out, id)
}

// StackFor is the layer stack a step's filesystem consists of, after it ran.
//
// Needed because an artifact is selected *after* the build: SAVE ARTIFACT names
// a path in some step's filesystem, and reconstructing that filesystem means
// knowing its stack. Empty for a node that did not run.
func (s *Scheduler) StackFor(n *ir.Node) []ir.NodeID {
	if n == nil {
		return nil
	}

	return s.stacks[n.ID()]
}

// runStep materialises the base, executes, and releases the handle whatever
// happens. Separated from Run so that the release cannot be skipped by an early
// return added later.
// maxStack is the depth Φ collapses at.
func (s *Scheduler) maxStack() int {
	if s.MaxStack > 0 {
		return s.MaxStack
	}

	return MaxStackDepth
}

// usableObservation decides whether a result's observation may become a Κ₂ key.
//
// Three conditions, and the third is not the source's to assert:
//
//	Observed             the source says it watched at all
//	!Incomplete          the source says it did not lose anything
//	not empty for OpExec the scheduler's own check
//
// `Consistent` iterates the reads, the negatives and the listings, so on an
// empty observation all three loops are empty and it returns **true for every
// base in existence**. Κ₂ then claims the result is valid wherever the step
// runs, and `RUN gcc -c main.c` hits against a base with a different compiler -
// I3 violated, the one failure this design exists to prevent.
//
// A step that ran a program read its own executable before it could read
// anything else, so a complete observation of an exec step is never empty. One
// that is empty is a source reporting silence as fact: a tracer attached after
// the exec, or - much more likely - a source wired up before it works, since
// every `Observations()` in this engine returns an empty observation today and
// the way one is switched on is by setting `Observed`.
//
// Stated for OpExec rather than for everything, because a step with no base
// reads nothing from one and refusing it would be the mirror mistake.
func usableObservation(n *ir.Node, base []ir.NodeID, res Result) bool {
	if !res.Observed || res.Observation.Incomplete {
		return false
	}

	if n.Op.Kind != ir.OpExec {
		return true
	}

	return ObservesSomething(n, base, res.Observation)
}

// ObservesSomething reports whether an observation says anything at all about
// the base a step ran over.
//
// **The question is the base, not the opcode.** `Consistent` iterates the
// reads, the negatives and the listings, so on an empty observation all three
// loops are empty and it returns true for every base in existence - and Κ₂ then
// claims the result is valid wherever the step runs (E112, I3).
//
// This was first stated as "an exec step must have seen something", which was
// right about the case in front of it and wrong as a rule: a COPY has a base
// and reads its destination in it (E119), and the next opcode with a base would
// have needed somebody to remember to come back here.
//
// A step with no base genuinely reads nothing from one, and refusing its
// observation would be the mirror mistake - a rule with fewer cases than the
// world (E97).
//
// Exported because the same question is asked on the publish side and the
// lookup side, and a rule implemented twice drifts.
func ObservesSomething(n *ir.Node, base []ir.NodeID, obs Observation) bool {
	_ = n

	if len(base) == 0 {
		return true
	}

	return len(obs.Reads) > 0 || len(obs.Listings) > 0 || len(obs.Negative) > 0
}

// noteStale records the first reason a prediction stopped describing its base.
func (s *Scheduler) noteStale(why string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Stats.StaleWhy == "" {
		s.Stats.StaleWhy = why
	}
}

// noteUnobserved records that a step produced nothing Κ₂ can use, and why.
// noteUncacheable records a step this engine will not cache, and why.
//
// The reason is derived from the operation rather than passed down with it, so a
// new way of becoming uncacheable is named the first time it happens instead of
// being reported as a bare "no cache".
func (s *Scheduler) noteUncacheable(n *ir.Node) {
	const most = 4

	s.mu.Lock()
	defer s.mu.Unlock()

	s.Stats.Uncacheable++

	if len(s.Stats.UncacheableAt) >= most || n.Meta.Source == "" {
		return
	}

	line := n.Meta.Source + ": " + whyUncacheable(n)
	if slices.Contains(s.Stats.UncacheableAt, line) {
		return
	}

	s.Stats.UncacheableAt = append(s.Stats.UncacheableAt, line)
	slices.Sort(s.Stats.UncacheableAt)
}

// whyUncacheable names what makes a step unkeyable, most specific first.
func whyUncacheable(n *ir.Node) string {
	switch {
	case n.Op.Kind == ir.OpHost:
		return "it runs on the host"

	case n.Op.Docker && !n.Op.IsolateDocker && n.Op.DockerCache != "":
		// What the author asked for, so there is nothing to suggest: the cache
		// is storage that outlives the step by definition.
		return "a docker daemon sharing the cache " + n.Op.DockerCache +
			", whose contents no key describes"

	case n.Op.Docker && !n.Op.IsolateDocker:
		// Not isolated, so it may have been handed the daemon of a step this
		// build is running inside, and what that daemon already held is not a
		// function of this step's inputs (I14).
		//
		// The remedy is named because it exists and is one word. The old message
		// was true of every WITH DOCKER block when none could be cached, and
		// became a category the author cannot act on the moment one could
		// (E393).
		return "a docker daemon it may share, whose contents no key describes" +
			" - `WITH DOCKER --isolate` gets one of its own, and is cacheable"

	// An *isolated* block falls through deliberately. It is cacheable as far as
	// the daemon goes, so if it has reached this function the reason is one of
	// the others below - a cache mount, a secret, or the author's own
	// `--no-cache` - and naming the daemon would send them to change a flag that
	// is already right.

	case len(n.Op.Mounts) > 0:
		return "a cache mount, whose contents no key describes"

	// A digest, where the fleet has a key, is exactly a key describing it - so
	// the step reaching here has some other reason and naming the secret would
	// send the author to fix what is already right (E393, as for WITH DOCKER).
	case len(n.Op.SecretEnv) > 0 && len(n.Op.SecretDigest) == 0:
		return "a secret, which no key may describe" +
			" - set `EARTH_HMAC` for the fleet to key it by a digest of its value"

	default:
		return "--no-cache"
	}
}

// noteUnpredicted records where an unpredicted step is written.
//
// Distinct, and bounded: a build with a thousand unpredicted steps has a
// systemic problem, and a thousand locations in a summary line is not how
// anybody would find out what it is.
func (s *Scheduler) noteUnpredicted(n *ir.Node) {
	const most = 4

	s.mu.Lock()
	defer s.mu.Unlock()

	where := n.Meta.Source
	if where == "" || slices.Contains(s.Stats.L2UnpredictedAt, where) {
		return
	}

	if len(s.Stats.L2UnpredictedAt) >= most {
		return
	}

	s.Stats.L2UnpredictedAt = append(s.Stats.L2UnpredictedAt, where)
	slices.Sort(s.Stats.L2UnpredictedAt)
}

func (s *Scheduler) noteUnobserved(n *ir.Node, base []ir.NodeID, res Result) {
	// A step with no base has nothing to observe *of* one, so it is not a step
	// that failed to be observed - it is a step there was nothing to say about.
	// Counting those made every corpus build report several, which is the shape
	// of number that trains people to ignore the line it is on (E218).
	if len(base) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.Stats.Unobserved++

	if s.Stats.UnobservedWhy != "" {
		return
	}

	s.Stats.UnobservedWhere = n.Meta.Source

	switch {
	case len(res.Observation.Why) > 0:
		s.Stats.UnobservedWhy = res.Observation.Why[0]

	case !res.Observed:
		// No source at all, which is a different thing from a source that
		// missed something: nothing was watching.
		s.Stats.UnobservedWhy = "nothing observed this step"

	default:
		// Complete, and saying nothing about the base - so it agrees with every
		// base in existence and must not be keyed (I3).
		s.Stats.UnobservedWhy = "the step looked at nothing in its base"
	}
}

// runStep runs a step, rebuilding an input that could not be obtained.
//
// **A layer that cannot be fetched is not a layer that cannot exist.** A worker
// went behind a firewall, a machine left the fleet, a network went away - and the
// step that produced it is still in the graph. Every other source in this engine
// degrades rather than fails (I6, I11), and the fleet was the one that did not:
// a driver that could not bring a delegated result back failed the build (E278).
//
// Once. An input still unobtainable after the step that makes it has been run
// here is not a transfer problem, and retrying for ever turns a broken build
// into a hanging one.
func (s *Scheduler) runStep(
	ctx context.Context, n *ir.Node, w Worker, base []ir.NodeID, sources [][]ir.NodeID,
) (Result, error) {
	s.remember(n, base, sources)

	res, err := s.runStepOnce(ctx, n, w, base, sources)

	var missing MissingInputError
	if !errors.As(err, &missing) || !s.rebuild(ctx, missing.Layer) {
		return res, err
	}

	return s.runStepOnce(ctx, n, w, base, sources)
}

// remember keeps what a step was run with, so it can be run again.
func (s *Scheduler) remember(n *ir.Node, base []ir.NodeID, sources [][]ir.NodeID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.inputs == nil {
		s.inputs = map[ir.NodeID]ranWith{}
	}

	s.inputs[n.ID()] = ranWith{base: base, sources: sources}
}

// rebuild runs whatever produced this layer, here, and says whether it worked.
//
// On the invoking machine deliberately: the layer was unobtainable from wherever
// it was, so producing it there again would be producing it out of reach again.
func (s *Scheduler) rebuild(ctx context.Context, id ir.NodeID) bool {
	n, ok := s.producerOf(id)
	if !ok {
		return false
	}

	s.mu.Lock()
	with, ok := s.inputs[n.ID()]
	s.mu.Unlock()

	if !ok {
		return false
	}

	res, err := s.runStepOnce(ctx, n, s.invoker(), with.base, with.sources)

	// The same step, so the same layer (I1) - and if it is not, something more
	// interesting than a transfer is wrong and the caller should see the
	// original failure rather than a surprise.
	return err == nil && res.Layer == id
}

// invoker is the local worker, or an empty one if this build has none.
func (s *Scheduler) invoker() Worker {
	for _, w := range s.Workers {
		if w.IsInvoker {
			return w
		}
	}

	return Worker{}
}

func (s *Scheduler) runStepOnce(
	ctx context.Context, n *ir.Node, w Worker, base []ir.NodeID, sources [][]ir.NodeID,
) (Result, error) {
	if s.Materialiser == nil {
		endExec := timing.Phase("exec", n.Meta.Source)
		defer endExec()

		return s.Executor.Run(ctx, n, w, base, sources)
	}

	// Where the scheduler owns the filesystem it materialises here, so that a
	// leaked mount is impossible on the failure path. Where the executor owns it
	// - anything in a VM - this is nil and the executor assembles the same stack
	// on its own side.
	h, err := s.Materialiser.Materialise(ctx, base)
	if err != nil {
		return Result{}, fmt.Errorf("materialise base: %w", err)
	}

	defer func() {
		endRelease := timing.Phase("release", n.Meta.Source)

		rerr := h.Release()

		endRelease()

		if rerr != nil && err == nil {
			err = rerr
		}
	}()

	endExec := timing.Phase("exec", n.Meta.Source)
	defer endExec()

	return s.Executor.Run(ctx, n, w, base, sources)
}

// ranWith is what a step was given, kept so it can be given the same again.
type ranWith struct {
	base    []ir.NodeID
	sources [][]ir.NodeID
}

// stepIdent is a step's positional identity: where it sits in the Earthfile,
// which is what a human means by "the same step" across two builds.
//
// Falls back to node identity when there is no source location, which keeps
// synthetic graphs working at the cost of coarser attribution - a change then
// reports as a graph-shape difference, which is true but less useful.
func stepIdent(n *ir.Node) string {
	if n.Meta.Source != "" {
		return n.Meta.Source
	}

	return n.ID().String()
}

// place chooses a worker for a node.
//
// The hard filter runs first and is not negotiable: a worker failing any
// constraint is ineligible regardless of how attractive it looks. Among the
// eligible, least-loaded wins, and ties are broken by worker ID so the choice
// does not depend on slice order or map iteration.
func (s *Scheduler) place(n *ir.Node, load map[string]int) (Worker, error) {
	eligible := make([]Worker, 0, len(s.Workers))

	native := s.native()

	for _, w := range s.Workers {
		if !eligibleFor(n, w, native) {
			continue
		}

		eligible = append(eligible, w)
	}

	// **Emulation is a second pass, not a looser first one.** Widening
	// eligibility instead would let an idle emulator take work from a busy
	// machine of the right architecture, and that is never the faster choice.
	// Not "usually slower": emulated work runs on the order of a hundred times
	// slower, because every instruction goes through an interpreter. A queue on
	// the native machine would have to be a hundred steps deep before the
	// comparison even became close.
	//
	// That makes the ordering a rule rather than a heuristic, and it is why
	// there is no load comparison between the two passes and should not be one.
	// Only when nothing can run the step natively is a machine that can emulate
	// it considered at all.
	if len(eligible) == 0 {
		for _, w := range s.Workers {
			if !w.canEmulate(n.Platform) {
				continue
			}

			// **The same predicate, with the one question it would fail
			// answered.** Every other constraint still holds - an invoker-only
			// step still needs the invoker - and restating them here is how the
			// placement model and the fleet's guarantee drifted apart before
			// (E426). A copy of the node would have been the obvious way to say
			// "pretend the platform matches", and `ir.Node` carries a lock.
			if eligible0(n, w, native, true) {
				eligible = append(eligible, w)
			}
		}
	}

	if len(eligible) == 0 {
		return Worker{}, noWorkerFor(n, s.Workers)
	}

	sort.Slice(eligible, func(i, j int) bool {
		li, lj := load[eligible[i].ID], load[eligible[j].ID]
		if li != lj {
			return li < lj
		}

		return eligible[i].ID < eligible[j].ID
	})

	return eligible[0], nil
}

// native is the invoking machine's platform.
//
// The invoker is the one worker whose platform is known without being announced,
// which is why it is the reference an unstated platform resolves against.
func (s *Scheduler) native() ir.Platform {
	for _, w := range s.Workers {
		if w.IsInvoker {
			return w.Platform
		}
	}

	return ir.Platform{}
}

// eligibleFor applies green paper §4.7.1's hard constraints.
//
// native is the invoking machine's platform, and it is what an unstated one on
// the node means.
func eligibleFor(n *ir.Node, w Worker, native ir.Platform) bool {
	return eligible0(n, w, native, false)
}

// eligible0 is eligibleFor with the architecture question settled by the caller.
//
// `platformSatisfied` is true only for the emulation pass, where the machine can
// run the step's platform by emulating it - every other constraint is asked
// exactly as it is asked of a native placement, from this one copy of them.
func eligible0(n *ir.Node, w Worker, native ir.Platform, platformSatisfied bool) bool {
	// Everything the fleet will refuse to delegate.
	//
	// One list, in `ir`, read by both: the fleet's refusal is the guarantee and
	// this is the model of it, and they were written separately - so placement
	// knew about the host and nothing else, charging a worker for every step
	// needing a secret, a docker daemon, a terminal or a cache mount, and
	// leaving the invoker uncharged for all of them (E426, E430).
	//
	// The schedule was already deterministic. It was not true, and only the
	// first of those is checked anywhere.
	if only, _ := n.Op.OnInvokerOnly(); only {
		return w.IsInvoker
	}

	// A mounted step runs on the invoker, and placement has to know it.
	//
	// The fleet refuses to delegate one - `fleet/delegate.go` returns
	// ErrNotDelegable for a step with mounts - so it lands on the invoker
	// whatever was decided here. Deciding otherwise charged a worker for work it
	// never did and left the invoker uncharged for work it did, so every later
	// decision was made against a load map that did not describe the build
	// (E426).
	//
	// The schedule was already deterministic; it was not true. Those are
	// different properties and only the first was being kept.
	//
	// Stated here as well as there on purpose: this is the placement's model of
	// where work can go, and the fleet's is the guarantee. Two checks at two
	// boundaries reading the same fact, which is the shape E384 argued for - not
	// E382's redundancy, where both copies sat in one place.
	// An unstated platform means **this machine's**, not anybody's.
	//
	// On one machine the distinction does not arise. On a fleet of mixed
	// architectures it is the difference between a build and a wrong build: a
	// step written without a platform means native, and running it elsewhere
	// produces binaries for a machine nobody asked about, filed under a key
	// that does not record which (E267). The failure is silent in the worst
	// way - the step succeeds and the layer is real.
	if platformSatisfied {
		return true
	}

	return platformFits(n, w, native)
}

// eligibleApartFromPlatform is everything eligibleFor checks except which
// architecture the machine is.
//
// Emulation reads it: a machine that can emulate the step's platform must still
// be the invoker when the step demands one, and must still not be charged with
// a mounted step. Only the architecture is in question, and only the
// architecture is skipped.
// platformFits is the architecture half of eligibility.
func platformFits(n *ir.Node, w Worker, native ir.Platform) bool {
	want := n.Platform
	if want == (ir.Platform{}) {
		want = native
	}

	// Nothing anywhere declares a platform: every in-process fleet, every test,
	// and a single-machine build before anybody has configured one. There is no
	// mismatch to protect against, and refusing would refuse every such build on
	// the way to protecting none.
	if want == (ir.Platform{}) {
		return true
	}

	// A worker that has not said what it is gets nothing. Refusing to guess
	// costs a slower build; guessing costs a wrong one.
	return want == w.Platform
}

// evalNode evaluates one step: lookup, execute if needed, record, publish.
//
// Extracted from Run so that steps can be evaluated concurrently. Every access
// to shared state goes through s.mu; the per-step work outside it - lookups,
// execution, hashing - is where the time goes and is exactly what must overlap.
func (s *Scheduler) evalNode(ctx context.Context, n *ir.Node, idx int) error {
	// Shared state is read under the lock and released before the expensive
	// work. Holding it across a step's execution would serialise the build
	// again, which is the whole thing this is for.
	s.mu.Lock()

	if _, ok := s.done[n.ID()]; ok {
		s.mu.Unlock()

		return nil
	}

	// A CATCH handler over a build that did not fail, or a step standing on one
	// that was skipped. Skipped rather than run against whatever it could reach
	// instead: the second command of a handler stands on the first, and running
	// it against the guarded step's filesystem would execute half a recovery
	// over a build that never went wrong.
	if s.skip(n) {
		s.skipped[n.ID()] = true
		s.done[n.ID()] = Result{}
		s.mu.Unlock()

		return nil
	}

	var stack []ir.NodeID

	// Inputs are what the step stands on. Sources are read from and never
	// stacked: stacking a build context would merge the host's directory layout
	// into the image, and stacking an artifact's target would merge a whole
	// other image in. The distinction is structural now rather than a check on
	// an input's kind, which only worked while a context was the sole source.
	for _, in := range n.Inputs {
		stack = append(stack, s.stacks[in.ID()]...)
	}

	// Inputs the step reads without standing on them: folded into the key
	// because the result depends on them, kept out of the stack because they
	// must not be mounted.
	var (
		// refs identify the sources for the *key*: a source's result layer is
		// its whole content, so that is what the key needs.
		refs []ir.NodeID
		// srcStacks are what a copy reads from. Stacks rather than single
		// layers, because an artifact need not be produced by its target's last
		// step: a build that makes a jar, reads a version out of it, and then
		// saves the jar has that jar two layers down.
		srcStacks [][]ir.NodeID
	)

	for _, src := range n.Sources {
		refs = append(refs, s.done[src.ID()].Layer)

		// Named apart from the step's own stack, which is in scope here and
		// means something else entirely: this one is what a copy reads out of,
		// that one is what the step stands on.
		srcStack := s.stacks[src.ID()]
		if len(srcStack) == 0 {
			srcStack = []ir.NodeID{s.done[src.ID()].Layer}
		}

		srcStacks = append(srcStacks, srcStack)
	}

	s.stacks[n.ID()] = stack

	if s.nodes == nil {
		s.nodes = map[ir.NodeID]*ir.Node{}
	}

	s.nodes[n.ID()] = n
	s.mu.Unlock()

	// 𝑏: exactly the inherited stacks. An input's stack already ends with that
	// input's own layer, so appending it again would put every layer in twice -
	// which overlayfs refuses with ELOOP, and which the simulator accepts
	// happily.
	base := stack

	var flat Flattening

	base, flat = Flatten(base, s.maxStack(), SquashID)

	// A flattened stack names a layer that does not exist yet. Building it is
	// the executor's, because the executor is the only party that knows where
	// layers live - and optional, because a simulator has no filesystem to
	// build one in.
	if flat.Applied() {
		if sq, ok := s.Executor.(Squasher); ok {
			err := sq.Squash(ctx, flat.Into, stack[flat.From:flat.To])
			if err != nil {
				return fmt.Errorf("collapse %d layers into one: %w", flat.To-flat.From, err)
			}
		}
	}

	endKey := timing.Phase("key", n.Meta.Source)
	key := DeriveChainKey(n, base, refs)

	endKey()
	bd, od, ed, pd := componentDigests(n, base)

	rec := StepRecord{
		Ident: stepIdent(n), Node: n.ID(), Class: StepClass(n),
		Base: bd, Op: od, Env: ed, Plat: pd,
		ChainKey: key, Flattened: flat, Meta: n.Meta, Seq: idx,
	}

	if flat.Applied() {
		s.bump(&s.Stats.Flattened)
	}

	// A host step is never cached and never hits a cache.
	//
	// It runs unsandboxed on the invoking machine, so nothing bounds what it
	// observed: A3 does not hold, ε is not a bound, and any key derived from it
	// is a claim about a step that could have read anything. Reading an entry
	// would run nothing and report that the machine had been changed; writing
	// one would offer that claim to every later build (I7).
	//
	// Enforced here rather than left to an executor to declare, because an
	// executor that forgot would produce exactly the wrong answer silently.
	// Uncacheable: a host step, because nothing bounds what it observed (I7); a
	// step the author marked --no-cache, because they have declared it is not a
	// function of its inputs; and a step inside a WITH DOCKER block that did not
	// ask for a daemon of its own, because the one it is handed may be an outer
	// step's and every image already in it is state this key does not describe.
	// `RUN docker images` is the plainest case - it prints exactly that state.
	//
	// **The docker case has narrowed, which is the ending that comment promised.**
	// A block that said `--isolate` gets a daemon of its own whose storage lives
	// in the step's own overlay and is thrown away with the step (E381), so
	// there is no state outliving the build for the key to fail to describe.
	// Every other docker block may be handed a daemon something else has been
	// using, and is still refused.
	//
	// The test is `IsolateDocker` and not `NoCache`, although the interpreter
	// sets both. Reading `NoCache` here would be reading the same decision
	// twice; reading a different field keeps this check independent of the one
	// upstream, which is the whole reason it is enforced here rather than left
	// to a caller to declare.
	//
	// They arrive by different routes and mean the same thing here: there is no
	// honest key for the result, so it is neither looked up nor published.
	host := n.Op.Kind == ir.OpHost || n.Op.NoCache ||
		(n.Op.Docker && !n.Op.IsolateDocker)

	// Not asked at all, rather than asked and ignored.
	//
	// This read `hit && !host` on each tier, so an uncacheable step consulted
	// both and discarded the answer - a store read and a view computation per
	// step, for a result that could not be used. Worse, `tryL2` *counts* why it
	// declined, so every such step reported itself unpredicted on every build
	// for ever, which is a number saying the tier is broken when the answer is
	// that it does not apply (E226).
	if host {
		s.noteUncacheable(n)
	}

	if !host {
		// L1. A hit skips execution entirely; a miss does the work. There is no
		// third outcome (I4).
		// An entry that cannot say what its image declared is not a hit: the
		// stack it would produce is missing an element, and nothing downstream
		// could tell (§3.2a).
		endLookup := timing.Phase("lookup", n.Meta.Source)
		e, hit := Lookup(s.cacheToRead(), s.Blobs, s.Trusted, key)

		endLookup()

		if hit && usableDeclaration(n.Op.Kind, e) {
			rec.Layer, rec.Exit, rec.Bytes, rec.Outcome = e.Layer, e.Exit, e.Bytes, OutcomeL1Hit
			s.finish(n, base, Result{
				Layer: e.Layer, Exit: e.Exit, Bytes: e.Bytes, Declares: e.Declares,
			}, rec)
			s.bump(&s.Stats.Hits)

			return nil
		}

		// L2. Consulted only when L1 missed, which is exactly when the
		// alternative is a full rebuild (green paper 4.3).
		endL2 := timing.Phase("l2", n.Meta.Source)
		e, hit = s.tryL2(ctx, n, base, refs)

		endL2()

		if hit && usableDeclaration(n.Op.Kind, e) {
			rec.Layer, rec.Exit, rec.Bytes, rec.Outcome = e.Layer, e.Exit, e.Bytes, OutcomeL2Hit
			s.finish(n, base, Result{
				Layer: e.Layer, Exit: e.Exit, Bytes: e.Bytes, Declares: e.Declares,
			}, rec)
			s.bump(&s.Stats.L2Hits)

			// **Answered by Κ₂, remembered as Κ₁.** The observed-input tier is
			// the expensive one - it derives a key from what the step read last
			// time and consults a profile to do it - and without this the same
			// step pays it on every build for ever: this repository's own
			// `+earthly` reported "27 by observed inputs" on run after run,
			// never once falling to fewer (E564).
			//
			// Sound because Κ₁ is the narrower claim. It names this exact base,
			// operation, environment and platform, and the hit just established
			// that this result is what those produce; a later build that
			// matches all of them would read the same files and get the same
			// answer, which is what Κ₁ means.
			//
			// The entry is stored as it was found, writer included. It is a
			// record of somebody else's result being reused rather than of this
			// build producing one, and rewriting the writer would launder that.
			if s.Cache != nil {
				s.Cache.Put(key, e)
			}

			return nil
		}
	}

	s.bump(&s.Stats.Misses)

	// Placement was decided before the build started, so nothing here depends on
	// what other steps happen to be doing.
	endStep := timing.Phase("step", n.Meta.Source)
	res, err := s.runStep(ctx, n, s.placed[n.ID()], base, srcStacks)

	endStep()
	if err != nil {
		return fmt.Errorf("run %s: %w", n.ID(), err)
	}

	rec.Layer, rec.Exit, rec.Bytes, rec.Outcome = res.Layer, res.Exit, res.Bytes, OutcomeMiss

	// An observation is usable only if it is closed: everything the step
	// observed is in it. A source that reports its own loss is honest and costs
	// an L2 hit; one that hides it costs correctness.
	if usableObservation(n, base, res) {
		endObs := timing.Phase("observe", n.Meta.Source)

		rec.ObservedKey = DeriveObservedKey(n, refs, res.Observation)
		rec.Observation, rec.Observed = res.Observation, true
		rec.ObsDigest = observationDigest(res.Observation)

		endObs()
	}

	// A step that ran and failed is a result, not an executor error - but it is
	// not a success, and the build stops. The failure is not cached: a cached
	// failure would make the next build fail identically without running
	// anything, so fixing the cause would appear to change nothing.
	if res.Exit != 0 {
		s.record(rec)

		err := &StepError{
			Source:   n.Meta.Source,
			Desc:     n.Meta.Description,
			Exit:     res.Exit,
			Output:   res.Output,
			Streamed: res.Streamed,
		}

		// A tolerated step is TRY: what stands on it must still run, because
		// FINALLY reads the filesystem this step left behind, and that is the
		// only reason TRY exists. The build still fails - remembered here and
		// returned once everything has run, so a red test suite cannot report a
		// green build.
		//
		// The layer is kept for the same reason and cached for none: a failed
		// step is never published, tolerated or not.
		if !n.Op.Tolerate || !res.Captured {
			return err
		}

		s.mu.Lock()
		s.tolerated = append(s.tolerated, err)
		s.failed[n.ID()] = true
		s.mu.Unlock()

		s.finish(n, base, Result{Layer: res.Layer, Exit: res.Exit, Bytes: res.Bytes}, rec)

		return nil
	}

	// A result whose layer was not captured names nothing. The zero NodeID is a
	// well-formed digest, so publishing it would assert that this step produces
	// the empty layer, and every later build sharing its key would hit that
	// assertion (green paper I11).
	if !res.Captured || host {
		rec.Outcome = OutcomeUncaptured
		s.finish(n, base, res, rec)

		return nil
	}

	s.finish(n, base, res, rec)

	if s.Cache != nil {
		// Content travels with the claim, because it is what a later build
		// compares this one against. The executor has computed it since the
		// guest protocol carried two digests, and until now nothing read it -
		// four layers of plumbing to a dead end, and the reason the conflict
		// check was comparing the digest that legitimately changes (E81).
		e := Entry{
			Layer: res.Layer, Content: res.Content,
			Exit: res.Exit, Bytes: res.Bytes, Writer: s.Writer,
			// Declared unconditionally: this result came from running the step,
			// so whether it declares anything is known even when the answer is
			// nothing.
			Declares: res.Declares, Declared: true,
		}

		// Both keys name the same result. Κ₁ is what the next identical build
		// hits; Κ₂ is what a build over a *different* base hits when it touched
		// nothing that differs.
		s.Cache.Put(key, e)

		switch {
		case s.Profiles == nil:
		case usableObservation(n, base, res):
			s.Profiles.Put(StepClass(n), res.Observation)
			s.Cache.Put(DeriveObservedKey(n, refs, res.Observation), e)

		default:
			s.noteUnobserved(n, base, res)
		}
	}

	return nil
}

// finish publishes a step's result and its record together, so no other step can
// observe one without the other.
func (s *Scheduler) finish(n *ir.Node, base []ir.NodeID, res Result, rec StepRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.done[n.ID()] = res

	// A zero identity is "no layer", exactly as it is "no declaration" below:
	// the empty base produces one, and pushing it would make every stack above
	// it name an element the store can never hold.
	stack := base

	switch {
	case len(res.Layers) > 0:
		// A step that produced a stack: every layer joins, oldest first.
		for _, l := range res.Layers {
			if l != (ir.NodeID{}) {
				stack = pushLayer(stack, l)
			}
		}

	case res.Layer != (ir.NodeID{}):
		stack = pushLayer(base, res.Layer)
	}

	// Above the layer it came with, because a declaration applies to what comes
	// after it exactly as a layer does.
	if res.Declares != (ir.NodeID{}) {
		stack = pushLayer(stack, res.Declares)
	}

	s.stacks[n.ID()] = stack
	s.Record.Steps = append(s.Record.Steps, rec)

	// Summed and maxed, under the lock that already guards the rest: a cache hit
	// contributes nothing, which is the point of it (E467).
	s.Stats.CPU += res.CPU

	if res.MaxRSS > s.Stats.MaxRSS {
		s.Stats.MaxRSS = res.MaxRSS
	}
}

// record stores a record without a result, for a step that failed.
func (s *Scheduler) record(rec StepRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Record.Steps = append(s.Record.Steps, rec)
}

// bump increments a counter under the lock. Statistics are shared, and a build
// whose numbers depend on scheduling is a build whose numbers mean nothing.
func (s *Scheduler) bump(counter *int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	*counter++
}

// skip reports whether a step has no reason to run. Called with s.mu held.
//
// Two ways to have none: it guards a step that did not fail, or it stands on
// something that was itself skipped. The second is what makes a handler of more
// than one command work, and it is transitive by construction - each step asks
// about its own inputs, which have already been decided.
func (s *Scheduler) skip(n *ir.Node) bool {
	if n.OnFailure != nil && !s.failed[n.OnFailure.ID()] {
		return true
	}

	for _, in := range n.Inputs {
		if s.skipped[in.ID()] {
			return true
		}
	}

	return false
}

// cacheToRead is the cache this build may read, which is none when the
// invocation said `--no-cache`.
//
// Written as a *read* path rather than as a flag at each site, because there are
// several lookups and a build that skipped some of them would be a build with an
// opinion about which parts of the cache it trusted (E462).
func (s *Scheduler) cacheToRead() ActionCache {
	if s.NoCache {
		return nil
	}

	return s.Cache
}
