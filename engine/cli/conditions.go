package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
	"github.com/containerd/platforms"
)

// runner executes a graph and reports whether it succeeded.
//
// Narrower than a scheduler on purpose: everything about turning a condition
// into a branch is then testable without a VM, an image or a network, which is
// the difference between a property that is checked on every change and one
// that is checked when someone remembers to run the slow suite.
type runner func(ctx context.Context, g *ir.Graph) (output string, err error)

// decideByRunning answers a condition by running it on the filesystem the
// recipe has built up to that line.
//
// Green paper §3.4a: where a condition requires evaluation in a sandbox, the
// graph is not fully known in advance. This is where that happens. The prefix
// the condition stands on is executed first - which costs almost nothing on a
// second build, because those steps are keyed and cached, and the build proper
// then hits the same entries.
//
// The exit status is the answer, as it is in a shell. That the *scheduler*
// already reports a non-zero exit as a StepError rather than an executor
// failure is what makes this small: the distinction between "it ran and said
// no" and "it could not be run" is one the engine already draws, and answering
// false to the second would take a branch the Earthfile did not select while
// reporting success.
func decideByRunning(
	ctx context.Context, run runner, cond []string, base *ir.Node, dir, where string,
) (interp.Result, error) {
	if base == nil {
		return interp.Result{}, fmt.Errorf("there is no filesystem to evaluate it against (%s)", where)
	}

	if base.Op.Kind == ir.OpHost {
		return interp.Result{}, fmt.Errorf(
			"deciding it needs the LOCALLY steps before it to have run, and a host step is never"+
				" cached - so running them to decide would run them twice (%s)"+
				"\n  to build this now, use --engine=buildkit", where)
	}

	// Through a shell, because the condition *is* a shell condition: `command
	// -v x`, `[ -f y ]`, `grep -q z file` mean what the shell means by them,
	// down to 127 for a command that is not installed. A second implementation
	// of those rules would be a second language.
	probe := &ir.Node{
		Op: ir.Op{
			Kind: ir.OpExec,
			Args: []string{"/bin/sh", "-c", strings.Join(cond, " ")},
			// The interpreter's working directory at this line, not the base
			// step's: WORKDIR changes the state without producing a step, so
			// the last step's Dir is whatever it happened to be and not where
			// the build now is.
			Dir: dir,
			Env: base.Op.Env,
		},
		Inputs:   []*ir.Node{base},
		Platform: base.Platform,
		Meta: ir.Meta{
			Source:      where,
			Description: "IF " + strings.Join(cond, " "),
		},
	}

	out, err := run(ctx, &ir.Graph{Root: probe})
	if err == nil {
		return interp.Result{Output: out}, nil
	}

	// It ran and failed. A failed step is deliberately not cached, so a
	// condition that changes its mind is not held to its old answer - and its
	// output is carried back, because a command that failed is often the one
	// whose message matters most.
	if stepErr, ok := errors.AsType[*core.StepError](err); ok {
		return interp.Result{Exit: stepErr.Exit, Output: stepErr.Output}, nil
	}

	return interp.Result{}, err
}

// engine lazily owns the sandbox, so that one is built at most once per run and
// only if something actually needs it.
//
// The order forces this. Whether a build needs a sandbox is a property of its
// plan, but making the plan may itself need one to decide a condition - so the
// sandbox cannot be decided up front, and must not be built twice when it is.
type engine struct {
	o Options

	// caseNote is what to say about a case-insensitive store *if this build
	// fails*. Empty where the store distinguishes case, or where the answer is
	// not known (E491).
	caseNote string

	// learned is what earlier builds observed about each condition, loaded on
	// demand and written back once the build is over.
	learned *core.Predictions
	// decided is which way each condition went in *this* build, so what the
	// build needed can be attributed to them afterwards.
	mu      sync.Mutex
	decided map[string]bool
	// images is every image this build's plan named, attributed to those
	// conditions once the build is over.
	images []string

	// image is the sandbox root filesystem this build needs. Empty is the
	// default one; a plan with a WITH DOCKER block asks for the one with a
	// daemon in it, which is a different VM because the VM is named after it.
	image string

	// started records that something has begun building the sandbox - possibly
	// on speculation, in the background. used records that something actually
	// needed it. The distinction is load-bearing: `executorFor` read "a sandbox
	// exists" as "a probe needed one", which held only while a probe was the
	// sole way one came to exist.
	started bool
	used    bool

	// fleetEx is what the driver handed back: a `fleet.Delegating` when workers
	// were joined, and the plain executor otherwise. The *build* schedules over
	// it, which it did not until E500.
	fleetEx core.Executor
	// fleetStop shuts down the driver endpoint, when a fleet was joined. Nil
	// for the ordinary build, which joins none.
	fleetStop func()

	// host is the executor for a build that needs no sandbox. Kept apart from
	// ex so a warm sandbox and a host-only build can both be shut down: one
	// field for both meant whichever was assigned last was the only one closed.
	host *exec.Executor

	once  sync.Once
	ex    *exec.Executor
	sched *core.Scheduler
	err   error

	// ac is this build's action cache. See actionCache.
	acOnce sync.Once
	ac     *cache.Cache
	acErr  error

	// prof is this build's profile store. See profileStore.
	profOnce sync.Once
	prof     *cache.Profiles
	profErr  error
}

// actionCache returns this build's one action cache, opening it on first ask.
//
// One per build, and the singleness is the point rather than thrift. Two Cache
// values over one directory both enforce I9 - os.Link is atomic whoever calls
// it - but the record of the rewrites they refused lives in the *object*, so a
// key that claimed two results while a condition was being probed was refused
// by one cache and reported by neither.
//
// Both callers reach it: the scheduler that answers conditions, and the build
// that follows. A host-only build asks nothing until the end and gets it then.
func (g *engine) actionCache(store string) (*cache.Cache, error) {
	g.acOnce.Do(func() {
		// The action cache lives beside the layers it refers to, so an entry
		// and the result it claims are evicted together. Splitting them would
		// leave claims pointing at layers someone else deleted - which is
		// handled (a missing layer is a miss) but pointlessly.
		g.ac, g.acErr = cache.Open(store)
	})

	return g.ac, g.acErr
}

// profileStore is the L2 tier's other half, beside the action cache and for the
// same reason: a profile names paths in layers, so it is evicted with them.
//
// Once per engine, because several schedulers share one - a condition is
// evaluated by its own scheduler (see sandboxed) and would otherwise open the
// store again.
func (g *engine) profileStore(store string) (*cache.Profiles, error) {
	g.profOnce.Do(func() {
		g.prof, g.profErr = cache.OpenProfiles(store)
	})

	return g.prof, g.profErr
}

// sandboxed returns the shared sandbox executor and a scheduler over it.
func (g *engine) sandboxed() (*exec.Executor, *core.Scheduler, error) {
	g.once.Do(func() {
		sb, err := sandbox(g.image)
		if err != nil {
			g.err = err

			return
		}

		e, err := exec.New(sb)
		if err != nil {
			g.err = err

			return
		}

		e.Platform = g.o.Platform
		e.Context = g.o.Dir
		e.Secrets = g.o.Secrets
		// The invoking user's agent, read here where the invocation's ambient
		// state is already being gathered - the executor takes it as a value
		// rather than reaching for it, so nothing below this line depends on the
		// environment (E466).
		e.SSHAuthSock = os.Getenv("SSH_AUTH_SOCK")

		imageRoot, err := imageCacheDir()
		if err == nil {
			e.ImageCache = imageRoot
		}

		ac, err := g.actionCache(sb.StoreDir())
		if err != nil {
			g.err = err

			return
		}

		// A fleet, if one was asked for. Mostly this hands back `e` itself and
		// costs nothing; when it does not, the *workers it found* have to reach
		// the scheduler too, or placement never puts a step on them and the
		// build looks local while a fleet sits idle.
		x, stop, err := fleet.Driver(context.Background(), e,
			func(s string) { fmt.Fprintln(g.o.Out, s) },
			&fleet.Layers{Root: sb.StoreDir()}, g.profiles(sb.StoreDir()))
		if err != nil {
			g.err = err

			return
		}

		g.fleetStop = stop

		workers := []core.Worker{localWorker(g.o.Platform)}
		if d, ok := x.(*fleet.Delegating); ok {
			workers = append(workers, d.Remote()...)
		}

		g.ex = e
		// Kept for the *build's* scheduler as well, not only for conditions
		// (E500).
		//
		// **Under the lock, because the reader does not join this Once.**
		// `sync.Once` publishes to whoever calls `Do`, and `scheduling` reads
		// `fleetEx` without calling it - so on the prewarm path, which runs this
		// on a goroutine nothing waits for (E537), the build reads a field this
		// is writing. The race detector found it on the first run that used
		// `-race`; nothing here had ever run one (E610).
		g.mu.Lock()
		g.fleetEx = x
		g.mu.Unlock()

		// The same question the build's scheduler asks, asked the same way:
		// a conditions pass that verified against the store while the build
		// verified against the index could answer a condition one way and its
		// own build the other.
		//
		// A failure here is not this pass's to report - the build opens the
		// same store a moment later and says so with somewhere to say it.
		blobs, _ := store.OpenBlobs(sb.StoreDir())

		g.sched = &core.Scheduler{
			Workers:  workers,
			Executor: x,
			Cache:    ac,
			Blobs:    blobs,
			Writer:   writerName,
		}
	})

	return g.ex, g.sched, g.err
}

// profiles is the read-set store, or nothing if it will not open.
//
// A driver with no profiles predicts nothing and sends whole layers, which is
// slower and not wrong - so a store that will not open is a reason to say
// nothing rather than to fail a build (E287).
func (g *engine) profiles(store string) core.Profiles {
	p, err := g.profileStore(store)
	if err != nil || p == nil {
		return nil
	}

	return p
}

// commands is the runner handed to the interpreter.
//
// Every answer is recorded against its site, so the next build knows which way
// this condition has been going. Recorded here rather than in the interpreter
// because it is a property of *running* the condition: a plan-only caller has
// no runner, evaluates nothing, and has nothing to learn from.
func (g *engine) commands(ctx context.Context) interp.Commands {
	return func(cmd []string, base *ir.Node, dir, where string) (interp.Result, error) {
		res, err := decideByRunning(ctx, g.runGraph, cmd, base, dir, where)
		if err == nil {
			taken := res.Exit == 0

			recordBranch(g.learned, cmd, where, taken)

			g.mu.Lock()

			if g.decided == nil {
				g.decided = map[string]bool{}
			}

			g.decided[siteOf(cmd, where)] = taken
			g.mu.Unlock()
		}

		return res, err
	}
}

// runGraph runs a probe and collects what the probe itself printed.
//
// Only the root: running a probe means running the steps it stands on, and
// those are ordinary build steps that print ordinary output. Taking the value
// from the display stream took that output too, so `LET v=$(echo wanted)` after
// a step that printed `noise` produced both lines - and produced only one of
// them once the earlier step was cached and printed nothing. A value that
// changes with the state of the cache is not a value.
//
// Matched on node identity rather than the source location the display stream
// carries, because a location names a line and a line is not a step.
func (g *engine) runGraph(ctx context.Context, graph *ir.Graph) (string, error) {
	// Whatever the sandbox was started for, something now needs it.
	g.mu.Lock()
	g.started, g.used = true, true
	g.mu.Unlock()

	e, s, err := g.sandboxed()
	if err != nil {
		return "", err
	}

	var out strings.Builder

	// The build keeps its own progress: the steps under a probe are the build's
	// steps, and a user watching a slow one should see it whether or not a
	// condition happens to be waiting on it.
	root := graph.Root.ID()

	prev := e.Capture
	e.Capture = func(n *ir.Node, line string) {
		if n.ID() == root {
			out.WriteString(line + "\n")
		}
	}

	defer func() { e.Capture = prev }()

	_, err = s.Run(ctx, graph)
	if err != nil {
		return out.String(), err
	}

	return out.String(), nil
}

// close releases the sandbox if one was built.
func (g *engine) close() {
	if g.host != nil {
		// Already on the way out: a failure to close is not something a
		// caller can act on, and reporting it would displace the reason the
		// build is closing.
		_ = g.host.Close()
	}

	// Joined through sandboxed() rather than read from the field: a warm-up
	// fills it on another goroutine, and a shutdown that raced the boot would
	// either miss the sandbox it meant to close or read a half-written pointer.
	g.mu.Lock()
	started := g.started
	g.mu.Unlock()

	if !started {
		return
	}

	g.closeSandbox()
}

// closeSandbox shuts down the sandbox executor, if one was built.
//
// Separate from close() because switching images has to do exactly this and
// nothing else: the host executor and the rest of the engine outlive the
// machine a probe happened to start.
func (g *engine) closeSandbox() {
	e, _, err := g.sandboxed()
	if err == nil && e != nil {
		_ = e.Close()
	}

	// The driver endpoint outlives the executor deliberately - a worker mid-step
	// is still talking - so it is taken down after, and only if one was joined.
	if g.fleetStop != nil {
		g.fleetStop()
	}
}

// executorFor returns the executor the build should use.
//
// A sandbox built to decide a condition is the same sandbox the build needs,
// and reusing it is not only thrift: a second one would have its own layer
// store, so the steps already run to answer the condition would all be cache
// misses in the build that follows.
func (g *engine) executorFor(plan *interp.Plan) (*exec.Executor, error) {
	// Before anything is chosen or started. The executor refuses this too and
	// that refusal is the guarantee (E391); this one exists so an author is not
	// told about a flag after a machine has booted for them (E394).
	err := checkIsolationSupported(plan.Graph)
	if err != nil {
		return nil, err
	}

	// A sandbox nothing has used yet does not make this build need one.
	// Reading its mere existence as a reason would let a background warm-up
	// decide which executor a host-only build runs on, and a hint that changes
	// a result is not a hint (I5). A sandbox a *probe* used is another matter -
	// reusing it is not thrift, because a second one has its own layer store
	// and every step already run to answer the condition would be a cache miss
	// in the build that follows.
	if needsSandbox(plan) || g.wasUsed() {
		// Switched even when a probe has already started the plain VM.
		//
		// It used to be `&& !g.wasUsed()`, on the reasoning that changing
		// machines would discard a layer store this build had written to. It
		// does not: both sandboxes take `sb.Store = storeDir()`, one host
		// directory shared into whichever VM is running, so the layers were
		// never in the machine to lose. What the guard actually did was run a
		// WITH DOCKER block in a VM with no docker in it - and wait ninety
		// seconds for a binary that was never going to arrive, in any target
		// that had a condition the interpreter could not decide.
		//
		// The cost of switching is a plain VM that was booted and is no longer
		// wanted. That is a boot, not a result.
		if needsDocker(plan) {
			switchErr := g.switchTo(sandboxImage(true))
			if switchErr != nil {
				return nil, switchErr
			}
		}

		e, _, sandboxedErr := g.sandboxed()

		return e, sandboxedErr
	}

	e, err := exec.NewHostOnly()
	if err != nil {
		return nil, err
	}

	e.Context = g.o.Dir
	g.host = e

	return e, nil
}

// wasUsed reports whether anything has actually needed the sandbox.
func (g *engine) wasUsed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.used
}

// remotes checks other repositories out under the build cache.
func (g *engine) remotes(ctx context.Context) interp.Remotes {
	return func(repo, rev string) (string, error) {
		dir, err := storeDir()
		if err != nil {
			return "", err
		}

		return gitRemotes(ctx, dir, httpsURL)(repo, rev)
	}
}

// localWorker describes the machine running the build.
//
// The platform matters and defaulting it to none does not mean "any": the
// scheduler's affinity rule refuses a node whose platform a worker does not
// declare, so a worker declaring nothing can run nothing that names a platform.
// `FROM --platform=linux/arm64 alpine` planned correctly and then failed with
// "no eligible worker" on a machine that runs exactly that.
//
// A node asking for a platform this machine cannot run still fails, which is
// the point: that is a scheduling failure that says so, rather than a silent
// build of the wrong architecture.
func localWorker(platform string) core.Worker {
	if platform == "" {
		platform = exec.DefaultPlatform()
	}

	w := core.Worker{ID: "local", IsInvoker: true}

	p, err := platforms.Parse(platform)
	if err == nil {
		w.Platform = ir.Platform{OS: p.OS, Arch: p.Architecture, Variant: p.Variant}
	}

	return w
}

// siteOf names a condition by where it is written and what it says.
//
// Not by the filesystem it is asked about: the probe that answers it stands on
// everything built before it, so that identity changes with almost any commit.
// Keyed on the site, a condition that has gone the same way for a month is
// still known to after an unrelated edit.
func siteOf(cond []string, where string) string {
	return where + " " + strings.Join(cond, " ")
}

// recordBranch remembers which way a condition went.
//
// Recording is all this does. The branch a build takes is whatever evaluating
// the condition yielded (green paper I5) - the history decides what is worth
// speculating on, never what is true, and keeping those two apart is what stops
// a stale statistic from becoming a wrong build.
func recordBranch(p *core.Predictions, cond []string, where string, taken bool) {
	if p == nil {
		return
	}

	p.Observe(siteOf(cond, where), taken)
}

// historyFile is where a machine keeps what it has learned about conditions.
const historyFile = "predictions.json"

// loadPredictions reads what earlier builds observed.
//
// A machine with no history is the first build on it, and a file this version
// cannot parse is the same case: a prediction is a hint, so losing the history
// costs speed and nothing else. Refusing to build because a statistics file is
// malformed would make a hint load-bearing, which is the one thing I5 forbids.
func loadPredictions(dir string) (*core.Predictions, error) {
	p := core.NewPredictions()

	b, err := os.ReadFile(filepath.Join(dir, historyFile)) //nolint:gosec // the engine's own store
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return p, nil
		}

		return nil, fmt.Errorf("read the prediction history: %w", err)
	}

	var stored history
	err = json.Unmarshal(b, &stored)
	if err != nil {
		// A history that cannot be parsed is a cache that cannot be used, not
		// a build that cannot proceed: predictions only decide *when* work
		// starts, never what it produces (I5), so the honest answer is to
		// start from nothing.
		return p, nil //nolint:nilerr // a corrupt cache is not a build failure
	}

	p.Restore(stored.Taken)
	p.RestoreNeeds(stored.Needs)
	p.RestoreIdle(stored.Idle)

	return p, nil
}

// history is what is kept between builds: which way each condition went, and
// what each branch went on to need.
type history struct {
	Taken map[string][2]int   `json:"taken"`
	Needs map[string][]string `json:"needs"`
	// Idle is how many consecutive builds each masked entry went unwanted
	// (green paper A.3). Absent from a store written before it existed, which
	// decodes as no counts - so nothing is dropped early rather than
	// everything at once.
	Idle map[string]map[string]int `json:"idle,omitempty"`
}

// savePredictions writes what this build learned.
//
// Go's JSON encoder sorts map keys, so two machines that learned the same thing
// hold identical bytes.
func savePredictions(dir string, p *core.Predictions) error {
	b, err := json.Marshal(history{
		Taken: p.Snapshot(), Needs: p.NeedsSnapshot(), Idle: p.IdleSnapshot(),
	})
	if err != nil {
		return fmt.Errorf("encode the prediction history: %w", err)
	}

	err = os.MkdirAll(dir, 0o750)
	if err != nil {
		return fmt.Errorf("prepare %s: %w", dir, err)
	}

	err = os.WriteFile(filepath.Join(dir, historyFile), b, 0o600)
	if err != nil {
		return fmt.Errorf("write the prediction history: %w", err)
	}

	return nil
}

// gitClone fetches a repository named by GIT CLONE, under the build cache.
func (g *engine) gitClone(ctx context.Context) interp.GitClone {
	return func(url, ref string) (string, error) {
		dir, err := storeDir()
		if err != nil {
			return "", err
		}

		return gitCloner(ctx, dir)(url, ref)
	}
}

// needsDocker reports whether any step in a plan asks for a docker daemon.
//
// Every node, not only the spine: a WITH DOCKER block inside a target reached
// through an artifact is off to one side of the graph, and a sandbox chosen
// from the spine alone would have no daemon in it when that target ran.
func needsDocker(plan *interp.Plan) bool {
	for _, n := range plan.Graph.Nodes() {
		if n.Op.Docker {
			return true
		}
	}

	return false
}

// sandboxImage is the root filesystem the VM runs.
//
// A build needing docker gets an image with a daemon in it; one that does not
// keeps the small image, because a daemon nobody asked for is a boot to pay for
// and a process to leave running. The two are different VMs and get there by
// the naming that already exists - a sandbox is named after its image - so the
// scheme built for reuse separates them for free.
func sandboxImage(docker bool) string {
	if docker {
		return dockerSandboxImage
	}

	return plainSandboxImage
}

// The sandbox images. Pinned by tag rather than digest for now; the digest
// belongs here before this is anything but a development engine, because an
// image that moves under a build is exactly the non-determinism this engine
// exists to remove.
const (
	plainSandboxImage  = "alpine:3.20"
	dockerSandboxImage = "docker:27-dind"
)

// warm starts the sandbox beside the interpretation rather than in front of it.
//
// sandboxed() is a sync.Once, so the first caller that genuinely needs the
// sandbox joins this initialisation rather than racing it or starting a second.
func (g *engine) warm(ctx context.Context) {
	g.mu.Lock()
	g.started = true
	g.mu.Unlock()

	go func() {
		e, _, err := g.sandboxed()
		if err != nil || e == nil {
			return
		}

		// **And the machine, not only the bookkeeping.** Building the executor
		// opens caches and joins a fleet; it does not boot anything, because the
		// boot is deferred to first use. So the 850ms this exists to overlap was
		// still being paid in front of the first step (E537).
		//
		// Nothing waits for this. A build that needs no machine finishes and
		// exits while the boot is still in flight, and what it leaves behind is
		// a running VM - which is what the next build wants, and what the idle
		// timeout takes away if there is no next build.
		e.Prewarm(ctx)
	}()
}

// switchTo makes the build's sandbox the one running image.
//
// A no-op when that is already the image, which is the common case: nothing was
// built yet, or a probe happened to need the same machine.
//
// Otherwise the sandbox that exists is shut down and another is built. The
// layer store is not affected - it is a host directory shared into the VM, and
// both images take the same one - so what is discarded is a boot and the work
// of the probe that needed it, never a result.
func (g *engine) switchTo(image string) error {
	g.mu.Lock()

	if g.image == image {
		g.mu.Unlock()

		return nil
	}

	started := g.started
	g.mu.Unlock()

	// Joined through sandboxed() rather than read from the field, for the
	// reason close() does it: a warm-up fills it on another goroutine, and
	// replacing it while that boot is in flight would leave a VM nobody owns.
	if started {
		_, _, _ = g.sandboxed()
		g.closeSandbox()
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Only the Once is re-armed. The fields it fills are left alone on purpose:
	// they are written by whichever goroutine runs it, so clearing them here
	// would be the very reach around the Once that
	// TestNothingReadsTheSandboxFieldOutsideTheOnce exists to forbid - and it
	// would buy nothing, because the next run assigns all of them.
	g.image = image
	g.once = sync.Once{}
	g.started = false
	g.used = false

	return nil
}

// removable is a sandbox that can be taken away as well as disconnected from.
//
// Optional rather than part of the Sandbox port: a backend with no persistent
// machine behind it - the host executor, a namespace - has nothing to remove,
// and requiring the method would make every one of them implement a no-op.
type removable interface {
	Remove() error
}

// RemoveSandbox takes away the persistent sandbox for this machine.
//
// The VM outlives a build on purpose, because booting one costs 620-700ms and
// the next build wants the same machine. Something still has to be able to take
// it away, or a developer accumulates one and attributes the memory to anything
// but the build tool.
// RemoveSandbox takes away every persistent sandbox for this machine.
//
// Every one, not this build's: a project with a WITH DOCKER block runs a second
// VM with a daemon in it, and a person asking for the sandbox to be removed
// means the machines the build tool left running, not whichever of them the
// current directory happens to imply.
func RemoveSandbox() error {
	var firstErr error

	for _, image := range []string{plainSandboxImage, dockerSandboxImage} {
		sb, err := sandbox(image)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}

			continue
		}

		r, ok := sb.(removable)
		if !ok {
			// Nothing persistent behind this backend, so nothing to remove and
			// no reason to treat saying so as a failure.
			return nil
		}

		// Every one is attempted even if an earlier fails: leaving a VM running
		// because a different one could not be removed is the outcome this
		// exists to prevent.
		err = r.Remove()
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}
