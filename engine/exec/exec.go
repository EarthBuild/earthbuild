// Package exec is stage S4: the port the scheduler calls to actually run a step.
//
// Its whole reason for existing is the sandbox lifetime. Experiment E1b
// measured a VM at roughly 690ms to boot and tear down, against about 65ms to
// exec inside a running one, so a sandbox per *step* would put half a minute of
// pure lifecycle into a fifty-step build for no benefit. The sandbox is a
// property of the run; steps are what happen inside it.
//
// The Sandbox port keeps that decision testable without a VM: the boot count is
// observable, so "one sandbox per run" is asserted rather than intended.
package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ignore"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// Conn is a bidirectional channel to a guest agent. An interface rather than
// net.Conn because the transports differ - a pipe locally, a vsock or a stdio
// pair into a VM - and none of that concerns the caller.
type Conn interface {
	io.ReadWriteCloser
}

// Sandbox is a place steps can run: something that boots, serves the guest
// protocol, and stops.
//
// Note what it cannot express: there is no per-step method. A sandbox is
// started once and reused, and the type is what makes that so rather than a
// convention someone has to remember.
type Sandbox interface {
	Start(ctx context.Context) (Conn, error)
	Stop() error

	// StoreDir is where layers live for this sandbox. FROM is satisfied by
	// placing an image there rather than by running anything, so the executor
	// has to know where "there" is.
	StoreDir() string

	// Confines reports whether a step's writes are held to its own layer
	// (green paper A3). A sandbox that does not confine still runs steps; its
	// results simply never become cache entries, because ε does not bound what
	// the step observed and the resulting key would be a false claim.
	Confines() bool
}

// Executor runs steps inside one sandbox. Implements core.Executor.
type Executor struct {
	// releases holds the teardowns that were moved behind their step's answer.
	// Waited for by Close, so what is deferred is when a mount comes down and
	// never whether it does.
	releases releaser

	// leaked is which layers this build produced hold a secret it was given.
	// Refused at the exit points rather than where it was found - see
	// leakedLayers.
	leaked leakedLayers

	// Platform is the "os/arch" images are pulled for. Defaults to the guest's.
	Platform string

	// Prime assembles a base from the paths a step was predicted to read,
	// instead of stacking the whole of its layers.
	//
	// Nil everywhere but a worker that has peers to fetch fragments from, and
	// nil means the base is the stack of layers it has always been. Set, a step
	// moves the part of its base it reads - measured at 0.2% to 2% of the layer
	// for read sets the shape a real step has (E298, E302).
	//
	// A primer that cannot prime falls back to the ordinary path rather than
	// failing: it is a slower build, and every mechanism this rests on was built
	// to degrade (I11).
	Prime func(ctx context.Context, stack []ir.NodeID, want []string, into string) error
	// Fetch obtains one path of a stack, into a place this engine chose.
	//
	// The other half of Prime: what the prediction missed, faulted in while the
	// step runs (E289, E304). Nil means a step that reads beyond its prediction
	// is failed rather than served, which is why a primer without a fetcher is
	// a configuration nobody should build.
	Fetch func(ctx context.Context, stack []ir.NodeID, into, at string) error

	primedMu sync.Mutex
	primed   map[string]primedBase

	// Scratch is where a primed base is assembled. The system temporary
	// directory when empty, which is wrong on a machine whose /tmp is a
	// different filesystem from the store - the same argument as E263's.
	Scratch string
	// ImageCache is where pulled images are kept, when they should not live
	// with the layers.
	//
	// Empty means beside the layer store, which is the simple case. Set, it lets
	// one machine share images across build caches: an image is identical for
	// every project, and fetching alpine once per project is bandwidth spent on
	// nothing.
	ImageCache string
	// Terminal is the caller's terminal, for `RUN --interactive`.
	//
	// Held here rather than in the graph for the reason Secrets are: the IR says
	// a step is interactive, and this is the only place the terminal itself
	// exists. Nil means no interactive step can run, which is the honest answer
	// for a build with nobody watching - a CI job, a cron entry - as well as for
	// any arrangement that is not one host.
	Terminal *os.File
	// Secrets are the credentials the invocation supplied, by name.
	//
	// Held here rather than in the graph so a value has nowhere to leak: the IR
	// carries a secret's id, and this is the only place the value exists.
	Secrets map[string]string
	// AWSCredentials are the invoking environment's AWS_* variables, for
	// `RUN --aws`.
	//
	// Supplied rather than read from the environment here, for the reason
	// SSHAuthSock is: a caller that is not a CLI has its own idea of what the
	// invocation held, and a package that reaches for `os.Environ` decides for
	// them. Empty means a step asking for credentials is given none, which is
	// the honest answer when the invocation had none to give.
	AWSCredentials map[string]string
	// SSHAuthSock is where the invoking user's ssh agent listens, empty when
	// there is none.
	//
	// Supplied rather than read from the environment here, because this package
	// is the one that runs steps and an executor that reached for ambient state
	// would be a second place the build's inputs come from (E466).
	SSHAuthSock string
	// Context is the local build context directory COPY reads from.
	Context string
	// Progress receives each line a step prints, with the step it came from.
	// Nil discards output, which is what tests want and what a machine-readable
	// front end would do differently.
	//
	// `raw` says the step asked for its output unprefixed - `RUN --raw-output`.
	// Passed rather than looked up because only the sink holds the node and
	// only the display holds the format, and the decision needs both.
	Progress func(step, line string, raw bool)

	// Capture receives each line a step prints, with the node that printed it.
	//
	// Progress is for display and names a step the way a person reads it - a
	// source location. Capture is for a caller that needs one step's output as
	// a *value*, which a label cannot answer: source locations are not unique,
	// and a caller filtering on one would be reading whatever else happened to
	// share a line.
	//
	// The distinction is not theoretical. `LET v=$(cmd)` runs cmd on the
	// filesystem the recipe has built to that point, which runs the steps
	// before it when they are not already cached, and taking the value out of
	// the display stream took their output with it - so v depended on whether
	// the machine had built this before.
	// stderr says the line came from the step's standard error. A build log
	// wants both streams; a `$( )` substitution wants stdout alone, which is
	// what every shell gives it (E725).
	Capture func(n *ir.Node, line string, stderr bool)

	sb Sandbox
	c  *guest.Client

	// start guards the one boot; startErr is what it came to, so every later
	// caller is told the same thing rather than retrying a backend that is down.
	start    sync.Once
	startErr error

	mu      sync.Mutex
	closed  bool
	running bool
	// dockerNote is why a WITH DOCKER step got no client. See DockerNote.
	dockerNote string
}

// New prepares an executor over a sandbox, without starting it.
//
// The sandbox starts on first use - see client() for why, and for what it cost
// when it did not.
func New(sb Sandbox) (*Executor, error) {
	return &Executor{sb: sb}, nil
}

// startedClient is the guest this executor already has, or nil.
//
// Distinct from client(), which *starts* one. A caller that merely prefers the
// guest - because the guest is nearer the store - must not be the reason a
// machine boots: a build whose every step was a cache hit is entitled to finish
// without one (E537).
func (e *Executor) startedClient() *guest.Client {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running || e.closed {
		return nil
	}

	return e.c
}

// client starts the sandbox if it is not running, and returns the guest.
//
// Started on first use rather than at construction, which is worth the whole of
// a VM boot on the most common thing a developer does: build again after
// changing nothing. A no-op rebuild of `FROM alpine + RUN true` - every step an
// L1 hit, nothing executed - cost 790ms against 10ms for the same build with no
// sandbox in its plan, and the difference was a VM booted to run nothing.
//
// The failure to start is deferred with it. That is the honest place for it: a
// build whose every step is cached is entitled to succeed on a machine whose VM
// backend is broken, and a build that must run something gets the diagnosis at
// the step that needed it.
// **No context, deliberately, and contextcheck is told so at each call.** The
// connection is made once and shared by every step afterwards, so the context
// that would be threaded here is whichever caller happened to be first - and
// cancelling that one caller would take the sandbox away from all the others.
// A `sync.Once` over a shared resource cannot borrow one caller's lifetime.
//
// Cancellation still reaches the work: every step's own call carries the
// caller's context, and it is the step that gets cancelled rather than the
// machine it runs on.
func (e *Executor) client() (*guest.Client, error) {
	e.start.Do(func() {
		c, err := e.connect()
		if err != nil {
			e.startErr = err

			return
		}

		e.mu.Lock()
		e.c, e.running = c, true
		e.mu.Unlock()
	})

	return e.c, e.startErr
}

// connect starts the sandbox and greets the guest, recovering once from a VM
// that is not there.
//
// A sandbox is now reused between builds, and the listing that decides to reuse
// one can be stale: a VM that is gone, or up but wedged, answers `container ls`
// and not a handshake. Taking it away and booting a fresh one is the recovery,
// and it belongs here because the handshake is the first thing that can tell
// the difference.
//
// Once, not in a loop. A backend that is genuinely broken has to say so rather
// than reboot until someone notices.
func (e *Executor) connect() (*guest.Client, error) {
	// Background deliberately, not the caller's context. The boot happens once,
	// behind a sync.Once, and serves every step of the build - so honouring the
	// context of whichever caller happened to arrive first would let a probe
	// with a short deadline take the sandbox away from everything after it.
	//
	// The consequence is that a boot cannot be cancelled, which is a real cost
	// and the smaller one: a wedged boot wastes a minute, and a sandbox
	// cancelled out from under a running build wastes the build.
	endStart := phase("sandbox:start", "")
	conn, err := e.sb.Start(context.Background())

	endStart()
	if err != nil {
		return nil, fmt.Errorf("start sandbox: %w", err)
	}

	endDial := phase("sandbox:dial", "")
	c, err := guest.Dial(conn)

	endDial()
	if err == nil {
		withTerminals(e.sb, c)

		return c, nil
	}

	r, ok := e.sb.(interface{ Remove() error })
	if !ok {
		_ = e.sb.Stop()

		return nil, fmt.Errorf("connect to the guest inside the sandbox: %w", err)
	}

	_ = e.sb.Stop()
	_ = r.Remove()

	conn, err2 := e.sb.Start(context.Background())
	if err2 != nil {
		return nil, fmt.Errorf("start sandbox: %w\n  after clearing one that did not answer: %w", err2, err)
	}

	c, err2 = guest.Dial(conn)
	if err2 == nil {
		withTerminals(e.sb, c)
	}

	if err2 != nil {
		_ = e.sb.Stop()

		return nil, fmt.Errorf("connect to the guest inside the sandbox: %w"+
			"\n  a previous one was cleared and rebooted first: %w", err2, err)
	}

	return c, nil
}

// Ping starts the sandbox and checks the guest answers. Nothing in a build
// calls it; it exists so the lazy start is observable to a test without
// materialising a layer stack.
func (e *Executor) Ping(ctx context.Context) error {
	c, err := e.client()
	if err != nil {
		return err
	}

	_, err = c.Materialise(ctx, nil)

	return err
}

// baseImageOf names the image a step stands on, for a diagnosis. Empty when the
// chain does not reach one, which a message can say better than a blank can.
func baseImageOf(n *ir.Node) string {
	for _, in := range n.Inputs {
		if in.Op.Kind == ir.OpImage && len(in.Op.Args) > 0 {
			return in.Op.Args[0]
		}

		if name := baseImageOf(in); name != "" {
			return name
		}
	}

	return "the base image"
}

// Where the docker client and its socket live in a sandbox image that has a
// daemon. Fixed paths, because they are a property of the image the engine
// chooses rather than of anything an Earthfile can say.
const (
	dockerClientPath = "/usr/local/bin/docker"
	dockerSocketPath = "/var/run/docker.sock"
	// The subcommands that are separate binaries: `docker compose` and
	// `docker buildx` are plugins, and a step given only the client finds
	// `docker` and then reports `compose` as an unknown command - with the
	// whole of docker's help after it, which reads as though the Earthfile were
	// wrong.
	//
	// Read only by dockermounts_darwin.go, so a linter run on Linux reports it
	// as unused and deleting it breaks the macOS build. `unused` findings are
	// per-platform, which is E106's lesson arriving from the linter's side: a
	// file behind a build tag is not compiled, not counted, and here not seen.
	// Read only by dockermounts_darwin.go; see above.
	dockerPluginDir = "/usr/local/libexec/docker/cli-plugins" //nolint:unused
)

// Run executes one step against a materialised base, and captures what it
// produced.
//
// A result is marked captured only when it was *both* captured and confined.
// The two are separate failures with the same remedy: a digest that was not
// computed names nothing, and a digest computed from an unconfined step names
// something no other build should trust. Either way the scheduler declines to
// cache it, and says so rather than silently producing a build that looks
// cached and is not.
func (e *Executor) Run(
	ctx context.Context, n *ir.Node, _ core.Worker, base []ir.NodeID, sources [][]ir.NodeID,
) (core.Result, error) {
	switch n.Op.Kind {
	case ir.OpImage:
		// FROM is materialised, not run: the image is placed in the layer store
		// under this node's identity, and the "result" is that layer.
		return e.materialiseImage(ctx, n)

	case ir.OpScratch:
		// The empty base. Nothing is fetched, nothing is written, and the
		// result is a step with no layer at all - which is what every step
		// stacked on it then starts from (E468).
		//
		// Captured, because it is complete: a build that copies onto `scratch`
		// and saves the result must be cacheable like any other, and the layer
		// it produced is the empty one.
		return core.Result{Captured: true}, nil

	case ir.OpLocal:
		// **The context lives on the host, and so did the store.** That second
		// clause was true when this was written and `EARTH_STORE_IN_VM`
		// falsified it: staged here, the layer lands where the guest cannot
		// read it, and the failure surfaces much later as `COPY x: nothing in
		// that target has it` - a missing artifact naming a target nobody
		// wrote.
		//
		// So it is handed across instead, which is what stageContextInGuest
		// does; the refusal below is what remains for a sandbox that cannot
		// carry it.
		if guest.StoreInVM() {
			return e.stageContextInGuest(ctx, n)
		}

		return e.stageContext(n)

	case ir.OpFile:
		return e.copyStep(ctx, n, base, sources)

	case ir.OpPackImage:
		// Written on this machine, into the store both sides share: the layers,
		// the platform and the reference are all here, and the step that loads
		// it is inside the sandbox.
		return e.packImage(ctx, n, base)

	case ir.OpHost:
		// LOCALLY: on this machine, in the project directory, with no sandbox.
		return e.hostStep(ctx, n)

	case ir.OpExec:
		// The case below.

	default:
		// Everything else is another stage's work and must not be silently
		// treated as a no-op, which would build something the Earthfile does not
		// describe.
		return core.Result{}, fmt.Errorf("exec backend cannot evaluate %s (%s)", n.Op.Kind, n.Meta.Source)
	}

	endClient := phase("exec:client", n.Meta.Source)
	c, err := e.client()

	endClient()
	if err != nil {
		return core.Result{}, err
	}

	endPrep := phase("exec:prep", n.Meta.Source)

	h, done, err := e.base(ctx, c, n, base)
	if err != nil {
		return core.Result{}, err
	}

	defer done()

	// **From the handle, which is where a declaration arrives** (green paper
	// §3.2a). It lives in the stack, and the party that materialised the stack
	// is the one that read it - so it comes back with the handle rather than
	// being looked up a second time from outside.
	//
	// This used to read the `.decl` files beside the base's layers from the
	// host. That was right on a machine that materialised the image itself and
	// wrong on one that was sent the layers, because the sidecar does not
	// travel; and it is wrong on every machine once the store is a disk the
	// host cannot open (E553, E554).
	baseCfg := declarationOf(h)

	// What the base image declared, under ε, with this step's own ENV over the
	// top - see stepEnv. It comes from an input and is therefore already in this
	// step's key, so it is not the ambient state I3 forbids.
	env := stepEnv(baseCfg.Env, n.Op.Env)

	// The id travels, not a path. The guest resolves it against its own store,
	// because the host and the guest see that store at different paths - a VM
	// on macOS - and a host path would have the guest create a directory in its
	// own filesystem that vanishes with it.
	mounts := make([]guest.Mount, 0, len(n.Op.Mounts)+2)

	// A WITH DOCKER step is given the client and a socket to reach the daemon
	// running in the sandbox. Both are mounts rather than layers, for the reason
	// a cache is one: they belong to the machine and outlive the step, and
	// anything written into the step's own root is captured into its image.
	//
	// The daemon itself is the sandbox image's business - the plan chooses an
	// image with one in it - so nothing here starts or stops anything.
	var daemon *guest.Daemon

	if n.Op.Docker {
		// Which daemon a step reaches is a property of the backend *and* of the
		// block: a VM's is disposable and this machine's is not (E117), and the
		// block says whether it wants one of its own (E381).
		plan, dockerErr := dockerFor(n.Op.IsolateDocker, n.Op.DockerCache, n.Op.DockerScope)
		if dockerErr != nil {
			return core.Result{}, dockerErr
		}

		// Why no client was provided, if none was: the socket alone works only
		// for an image carrying its own, and a step whose image has none says
		// `docker: not found` about a mount that is fine (E146). Only that -
		// this channel is a warning about the client and nothing else (E392).
		e.noteDocker(plan.Note)

		mounts = append(mounts, plan.Mounts...)

		if plan.Own {
			daemon = &guest.Daemon{Root: daemonRoot, Socket: daemonSocket}
		}
	}

	for _, m := range n.Op.Mounts {
		gm := guest.Mount{
			ID: m.ID, Target: m.Target, ReadOnly: m.ReadOnly,
			Persist: m.Persist, Sandbox: m.Sandbox,
			// The sharing mode, which decides whether the guest queues steps on
			// this directory and whether it is a directory anybody else can see
			// (E432).
			Exclusive: m.Exclusive, Ephemeral: m.Ephemeral, Tmpfs: m.Tmpfs, Mode: m.Mode,
		}

		// A bound view names the object it shows by identity (§3.3d). Zero for
		// a cache mount and a secret, which show nothing this build made.
		if m.From != (ir.NodeID{}) {
			viewErr := fillView(&gm, m, n, sources)
			if viewErr != nil {
				return core.Result{}, viewErr
			}
		}

		// The value is looked up here and nowhere earlier: it is not in the
		// node, not in the key, and not in any plan anyone can print.
		if m.Secret {
			gm.Credential = true

			// The same secret under two spellings - see ir.SecretName.
			id := ir.SecretName(m.ID)

			v, ok := e.Secrets[id]
			if !ok {
				return core.Result{}, fmt.Errorf(
					"%s needs the secret %q, which this invocation did not supply",
					n.Meta.Source, id)
			}

			gm.Secret = v
		}

		mounts = append(mounts, gm)
	}

	// The invoking user's ssh agent, where the step asked for one.
	//
	// Resolved here rather than at planning, because the socket's path is a
	// property of this invocation and would poison every key it reached: the
	// operation says an agent is wanted and this finds it (E466).
	if n.Op.SSH {
		agentMounts, agentEnv, sshErr := sshAgent(e.SSHAuthSock)
		if sshErr != nil {
			return core.Result{}, fmt.Errorf("%s: %w", n.Meta.Source, sshErr)
		}

		mounts = append(mounts, agentMounts...)

		for k, v := range agentEnv {
			env = append(env, k+"="+v)
		}
	}

	// Secret values are added to the environment here and nowhere earlier: the
	// node records which secrets the step asked for, and this is the only place
	// a value exists.
	// The names, so the guest can tell a credential from any other variable when
	// it checks what the step produced. Names only: the values travel in `env`
	// once and have no reason to travel twice.
	var secretNames []string

	for _, spec := range n.Op.SecretEnv {
		name, source, ok := strings.Cut(spec, "=")
		if !ok {
			source = name
		}

		// As above, and the empty source supplies nothing rather than failing:
		// `--build-arg SECRET_ID=""` empties it on purpose.
		source = ir.SecretName(source)
		if source == "" {
			continue
		}

		v, given := e.Secrets[source]
		if !given {
			return core.Result{}, fmt.Errorf(
				"%s needs the secret %q, which this invocation did not supply",
				n.Meta.Source, source)
		}

		env = append(env, name+"="+v)
		secretNames = append(secretNames, name)
	}

	// **`RUN --aws`: the credentials travel like a secret because they are
	// one.** Registering the names means the scanner redacts their values from
	// the log and fails the build if one reaches a layer - which is the whole
	// reason to forward them through this path rather than as plain
	// environment. See awsEnv for why only some of the AWS variables are
	// named.
	if n.Op.AWS {
		awsVars, awsSecret := awsEnv(e.AWSCredentials)

		env = append(env, awsVars...)
		secretNames = append(secretNames, awsSecret...)
	}

	// Before anything runs: a step built for a platform this sandbox cannot
	// execute fails with `exec format error`, which names neither the platform
	// nor the line.
	err = CheckRunnable(DefaultPlatform(), e.platformFor(n), n.Meta.Source)
	if err != nil {
		return core.Result{}, err
	}

	// `RUN --entrypoint` runs the image's own entrypoint with these arguments.
	// The entrypoint is read here rather than planned, because only the fetched
	// image knows it - and it is in the step's key already, through the image
	// the step stands on.
	argv := n.Op.Args
	if n.Op.Entrypoint {
		if len(baseCfg.Entrypoint) == 0 {
			return core.Result{}, fmt.Errorf(
				"%s: --entrypoint, but %s declares no entrypoint to run"+
					"\n  write the command out, or use an image that declares one",
				n.Meta.Source, baseImageOf(n))
		}

		argv = append(append([]string{}, baseCfg.Entrypoint...), argv...)
	}

	write, flush := e.sinkFor(n)

	endPrep()

	endRun := phase("run", n.Meta.Source)

	step, err := c.RunStep(ctx, h, guest.Step{
		Dir: n.Op.Dir, User: n.Op.User, Argv: argv, Env: env, Mounts: mounts,
		SecretEnv: secretNames,
		NoNet:     n.Op.NoNetwork, Daemon: daemon, Hosts: n.Op.Hosts,
		// Observed, so the step can be reused against a base it did not run on.
		//
		// The only source a RUN has, and it costs: measured at **8x on a path
		// operation**, 8.4µs against 1.0µs, which is one round trip through this
		// engine per open or stat (E213). Affordable because path calls are a
		// small share of a real step's time - a compile making a hundred
		// thousand of them pays under a second - and not free, so this is the
		// line to change when somebody measures a build where it is not.
		//
		// Not for an interactive step. A person at a prompt is not producing a
		// layer anybody will reuse, and every keystroke's worth of shell
		// completion would trap.
		Trace: tracing() && !n.Op.Interactive,
		// Only for a step that asked. Handing a terminal to every step would put
		// a prompt's descriptor in front of a hundred non-interactive ones and
		// make each of them the sole holder of it (E192).
		Terminal: terminalFor(n, e.Terminal),
	}, write)

	endRun()

	endFlush := phase("exec:flush", n.Meta.Source)

	// The step is over, so anything the buffer still holds is the last line of
	// its output and belongs to it (E449). Before the error is handled, because
	// the output of a step that failed is the part worth reading.
	flush()

	if err != nil {
		return core.Result{}, ExplainExec(
			fmt.Errorf("run %s: %w", n.Meta.Source, err), DefaultPlatform(), n.Meta.Source)
	}

	endFlush()

	endCapture := phase("capture", n.Meta.Source)
	id, content, bytes, leaked, err := c.Capture(ctx, h)
	e.noteLeaked(id, leaked)
	endCapture()

	endAfter := phase("exec:after", n.Meta.Source)
	defer endAfter()

	if err != nil {
		return core.Result{}, fmt.Errorf("capture the result of %s: %w", n.Meta.Source, err)
	}

	// What the step looked at, which the guest recorded while it ran. Asked
	// after the work and before the handle is released, because that is the only
	// moment both are true - the same reason and the same place as the copy path.
	//
	// **Missing here until now**, which is why a traced RUN produced a complete
	// observation that nothing ever stored: `usableObservation` gates on
	// `Observed`, so a result that never sets it is one no prediction is ever
	// written for, and Κ₂ has nothing to look up (E217).
	obs, observed := observedFrom(h)

	return core.Result{
		Layer:   id,
		Content: content,
		Bytes:   bytes,
		Exit:    step.Exit,
		Output:  step.Output,
		// What the step spent, for a build asked to say so (E467).
		CPU:         step.CPU,
		MaxRSS:      step.MaxRSS,
		Observation: obs,
		Observed:    observed,
		Captured:    e.sb.Confines(),
		// Anything watching has already seen these lines, so an error about
		// this step points at them rather than printing them again (E73).
		Streamed: e.Progress != nil,
	}, nil
}

// platformFor is the platform a node is built for.
//
// The node's own platform wins, because `BUILD --platform=linux/arm64 +target`
// means that target's steps run there. Without this the interpreter records a
// platform, the key changes, two builds are planned - and both pull the same
// image: a right plan and a wrong result.
func (e *Executor) platformFor(n *ir.Node) string {
	if n.Platform.OS != "" && n.Platform.Arch != "" {
		p := n.Platform.OS + "/" + n.Platform.Arch
		if n.Platform.Variant != "" {
			p += "/" + n.Platform.Variant
		}

		return p
	}

	if e.Platform != "" {
		return e.Platform
	}

	return DefaultPlatform()
}

// DefaultPlatform is what images are pulled for when nothing says otherwise.
//
// The *sandbox's* platform, not the host's. Both backends run Linux - a VM on
// macOS, this kernel on Linux - so defaulting to runtime.GOOS asks a registry
// for a darwin image, which no base image provides.
func DefaultPlatform() string { return "linux/" + runtime.GOARCH }

// materialiseImage ensures a base image is present in the layer store.
//
// Pulled once and then reused: the layer directory is named by the node's
// identity, which is derived from the reference, so a second target using the
// same base finds it already there. A pull that has already happened is the
// cheapest kind.
func (e *Executor) materialiseImage(ctx context.Context, n *ir.Node) (core.Result, error) {
	if len(n.Op.Args) == 0 {
		return core.Result{}, fmt.Errorf("FROM has no image reference (%s)", n.Meta.Source)
	}

	// Through the shared cache, so the second target to name this image links
	// it rather than pulling it again.
	platform := e.platformFor(n)

	// The configuration the pull found, kept so it can be written beside the
	// layer once the fetch has succeeded. Empty when the image came from the
	// shared cache and was not fetched at all.
	imageRoot := e.ImageCache
	if imageRoot == "" {
		imageRoot = e.sb.StoreDir()
	}

	pull := func(ctx context.Context, ref, into string) (ocispec.ImageConfig, error) {
		return image.Pull(ctx, ref, into, image.Options{
			Platform: platform,
			// Beside the images, because where a registry issues tokens is the
			// same answer for every project on this machine (E535).
			Challenges: imageRoot,
			Mirrors:    image.MirrorsFromEnv(),
		})
	}

	root := e.sb.StoreDir()
	shared := filepath.Join(imageRoot, "imagecache", ImageCacheKey(n.Op.Args[0], platform))

	// **One layer per layer**, rather than one layer for the whole image.
	//
	// Behind a flag while it earns its place: the merged form is what every
	// cache key in existence was derived from, so turning this on changes them.
	// See E646 and E648 for what it buys and what it costs.
	if LayersApart() {
		// **The guest unpacks where it can grant what the archive says**, and
		// the host does where it cannot ask. Both keep the layers apart; they
		// differ only in which side writes them. See EnvUnpackInGuest.
		// **A store on the guest's device implies the guest unpacks**, because
		// the host cannot write a block device it does not have.
		if UnpacksInGuest() {
			return e.materialiseImageInGuest(ctx, n, platform, imageRoot, root, shared)
		}

		return e.materialiseImageApart(ctx, n, platform, imageRoot, root, shared)
	}

	// Already materialised, and named by what is in it rather than by which
	// node asked for it. The recorded name is what makes this cheap: without it
	// every build would re-capture the tree to learn a digest it had already
	// computed.
	st := store.DirStore(root)

	if id, ok := imageLayerNamed(shared); ok && st.Populated(id) {
		// The declaration too, and by the same route: it is derived from the
		// configuration beside the layer, so an image this machine has already
		// materialised produces the same identity without fetching anything.
		return core.Result{
			Layer: id, Captured: e.sb.Confines(), Declares: st.Declaration(id),
		}, nil
	}

	// Staged under a name nothing derives meaning from, because the name this
	// layer will keep is the digest of what lands here (§3.2) and that is not
	// known until it has landed.
	staging, err := st.Staging(".image-")
	if err != nil {
		return core.Result{}, fmt.Errorf("stage %s: %w", n.Op.Args[0], err)
	}

	// fetchImageFrom refuses to write into a directory that exists, since an
	// existing layer is a finished one it must not disturb (E141). The staging
	// name is ours and empty, so it is removed and handed over as a name.
	_ = os.Remove(staging)

	endFetch := phase("image:fetch", n.Op.Args[0])
	err = fetchImageFrom(ctx, imageRoot, n.Op.Args[0], platform, staging, pull)
	endFetch()

	if err != nil {
		return core.Result{}, fmt.Errorf("FROM %s (%s): %w", n.Op.Args[0], n.Meta.Source, err)
	}

	endPlace := phase("image:place", n.Op.Args[0])
	id, err := st.Place(staging)
	endPlace()

	if err == nil {
		st.NoteUnmarked(id)
	}

	if err != nil {
		return core.Result{}, fmt.Errorf("FROM %s (%s): %w", n.Op.Args[0], n.Meta.Source, err)
	}

	// The configuration follows the layer to its final name. What an image
	// declares is not part of what it ships, so it travels beside the tree
	// rather than in it.
	_ = st.AdoptConfig(id, staging+store.ConfigSuffix)
	_ = os.Remove(staging + store.ConfigSuffix)

	rememberImageLayer(shared, id)

	return core.Result{
		Layer: id, Captured: e.sb.Confines(), Declares: st.Declaration(id),
	}, nil
}

// layerSuffix names the file recording which layer an image materialised as.
//
// Beside the shared image-cache entry rather than in the layer store: it answers
// "what does this reference unpack to", which is a property of the image and not
// of any one build's store.
const layerSuffix = ".layer"

// imageLayerNamed is the layer an image was last materialised as, if this
// machine has done it before.
//
// A cache of a pure function - the digest of the tree the image unpacks to - so
// a wrong or stale answer costs a re-capture and cannot produce a wrong layer:
// the caller checks the named layer is present, and what it names was computed
// by capturing the tree.
func imageLayerNamed(shared string) (ir.NodeID, bool) {
	// `shared` is a store path this engine computed, and the note beside it is
	// one it wrote (gosec G304). Nothing here comes from a build.
	b, err := os.ReadFile(shared + layerSuffix) //nolint:gosec // a path this engine derived
	if err != nil {
		return ir.NodeID{}, false
	}

	id, err := ir.ParseNodeID(strings.TrimSpace(string(b)))
	if err != nil {
		return ir.NodeID{}, false
	}

	return id, true
}

// rememberImageLayer records what an image unpacked to. Best-effort: losing it
// costs one capture.
func rememberImageLayer(shared string, id ir.NodeID) {
	_ = os.WriteFile(shared+layerSuffix, []byte(id.String()), 0o600)
}

// stageContext places a build context path in the layer store.
//
// Identity comes from the node, whose Content digest already covers the bytes
// (green paper §3.3a via the interpreter), so a context that has not changed is
// staged once and found thereafter.
func (e *Executor) stageContext(n *ir.Node) (core.Result, error) {
	if e.Context == "" {
		return core.Result{}, fmt.Errorf("no build context configured, but %s needs one", n.Meta.Source)
	}

	if store.DirStore(e.sb.StoreDir()).Has(n.ID()) {
		return core.Result{Layer: n.ID(), Captured: e.sb.Confines()}, nil
	}

	st := store.DirStore(e.sb.StoreDir())

	// **Built beside its name and renamed in.** This used to copy straight into
	// the final directory, so a copy that failed half way left a tree that `Has`
	// reports as present and a later build stands on. The rule everywhere else
	// here is that a transfer leaving nothing beats one leaving half.
	dir, err := st.Staging(".context-")
	if err != nil {
		return core.Result{}, fmt.Errorf("stage the build context: %w", err)
	}

	committed := false

	defer func() {
		if !committed {
			_ = os.RemoveAll(dir)
		}
	}()

	// Timed on this path too, so the two can be compared: this one copies and
	// commits, while the guest path copies, tars, and has the guest unpack -
	// which is the whole of why a `COPY` costs 0.31ms a file here and 0.73ms
	// there (E829a).
	endCopy := phase("context:copy", n.Meta.Source)
	err = e.copyContextInto(n, dir)
	endCopy()

	if err != nil {
		return core.Result{}, err
	}

	err = st.PutNamed(n.ID(), dir)
	if err != nil {
		return core.Result{}, fmt.Errorf("file the build context: %w", err)
	}

	committed = true

	return core.Result{Layer: n.ID(), Captured: e.sb.Confines()}, nil
}

// copyStep implements COPY: materialise the base, copy from the context layer,
// capture what changed.
func (e *Executor) copyStep(
	ctx context.Context, n *ir.Node, base []ir.NodeID, sources [][]ir.NodeID,
) (core.Result, error) {
	if len(n.Op.Args) < 2 {
		return core.Result{}, fmt.Errorf("COPY needs a source and a destination (%s)", n.Meta.Source)
	}

	// The source is what the step reads from - a staged build context, or
	// another target's output - and is never part of the base. Looking for it
	// among the inputs was correct only while a context was the sole kind of
	// source; an artifact source is an ordinary node and was invisible there.
	if len(sources) == 0 {
		return core.Result{}, fmt.Errorf("COPY at %s has no source", n.Meta.Source)
	}

	from := sources[0]

	c, err := e.client()
	if err != nil {
		return core.Result{}, err
	}

	h, err := c.Materialise(ctx, base)
	if err != nil {
		return core.Result{}, fmt.Errorf("materialise the base for %s: %w", n.Meta.Source, err)
	}

	defer func() { _ = h.Release() }()

	err = c.Copy(ctx, h, from, n.Op.Args[0], n.Op.Args[1],
		guest.CopyOpts{
			AsDir: n.Op.DirCopy, NoFollow: n.Op.NoFollow, KeepOwn: n.Op.KeepOwn,
			Chown: n.Op.Chown, IfExists: n.Op.IfExists, Chmod: n.Op.Chmod,
			LandsAs: n.Op.As,
		})
	if err != nil {
		return core.Result{}, fmt.Errorf("%s: %w", n.Meta.Source, err)
	}

	id, content, bytes, leaked, err := c.Capture(ctx, h)
	e.noteLeaked(id, leaked)
	if err != nil {
		return core.Result{}, fmt.Errorf("capture the result of %s: %w", n.Meta.Source, err)
	}

	// What the copy looked at in its base, which the guest recorded while doing
	// the work (E119). Asked after the copy and before the handle is released,
	// because that is the only moment both are true.
	obs, observed := observedFrom(h)

	return core.Result{
		Layer: id, Content: content, Bytes: bytes, Captured: e.sb.Confines(),
		Observation: obs, Observed: observed,
	}, nil
}

// observedFrom decides whether a handle's record amounts to an observation of
// the step, and returns it either way.
//
// The guest reports *what it saw*; whether that is an observation of the step is
// a question about the whole step, which is why this is on the executor's side.
//
// **A lossy record is still reported.** `Observed` and `Incomplete` answer
// different questions - "did anyone watch" and "did they see everything" -
// and collapsing them here would throw away the distinction the scheduler needs
// to refuse for the right reason and to say so in the build's record.
func observedFrom(h core.Handle) (core.Observation, bool) {
	obs := h.Observations()

	return obs, len(obs.Reads) > 0 || len(obs.Listings) > 0 || len(obs.Negative) > 0
}

// Degraded reports why steps ran without the resource limits they were given.
//
// Passed through from the guest, which is the only party that knows: the host
// asks for a memory ceiling and the guest is where a cgroup either exists or
// does not. Empty when nothing was degraded, so a caller can print it
// unconditionally (E123).
func (e *Executor) Degraded() string {
	// **Never starts one.** `client()` boots the sandbox on first use, and a
	// build whose every step was cached or local is entitled never to boot one
	// - which is the property `TestALocalOnlyBuildNeedsNoSandbox` exists to
	// hold, and which asking this question used to break by asking it. A query
	// with a side effect is not a query.
	e.mu.Lock()
	c := e.c
	e.mu.Unlock()

	if c == nil {
		return ""
	}

	return c.Degraded()
}

// SharedNet reports why steps shared one network namespace rather than each
// getting its own.
//
// Passed through from the guest for the reason Degraded is: the host asks for
// isolation and the guest is where `ip` either exists or does not. Empty when
// nothing shared, so a caller can print it unconditionally.
//
// Never starts a sandbox, on the rule Degraded states above: a query with a side
// effect is not a query, and a build whose every step was cached is entitled
// never to boot one.
func (e *Executor) SharedNet() string {
	e.mu.Lock()
	c := e.c
	e.mu.Unlock()

	if c == nil {
		return ""
	}

	return c.SharedNet()
}

// Unmounted reports why a step's filesystem was not fully built.
//
// Passed through from the guest for the reason Degraded is: mounting /sys is
// something only the guest can attempt, and only the guest knows whether it
// worked. Empty when every mount was made, so a caller can print it
// unconditionally.
func (e *Executor) Unmounted() string {
	// Never starts a sandbox, on the rule above: a query with a side effect is
	// not a query.
	e.mu.Lock()
	c := e.c
	e.mu.Unlock()

	if c == nil {
		return ""
	}

	return c.Unmounted()
}

// EnvTrace turns off watching what a step reads.
//
// **A lever and a measurement.** Observation is how a step earns an L2 hit - a
// result reused over a base it was not computed on - and it is paid for on every
// intercepted syscall. The price has been measured twice and read wrongly both
// times: twenty-five-fold on a step that only reads (E588), and nothing at all
// on a test suite where the hypervisor was the whole story (E589, E598). With
// the hypervisor gone, the engine's own overhead on `+unit-test` is 2.3s of
// 335s and the rest is the step itself running - so what remains to explain is
// inside the sandbox, and this is the switch that says whether it is this.
//
// On unless switched off, because a build that cannot earn an L2 hit is slower
// in the way that matters more.
// EnvTrace names the setting that turns the syscall tracer off.
//
// `0` disables it and anything else leaves it on, which is the direction that
// matters: a build that forgets to set it observes its steps, and observing is
// what the second cache tier is derived from. The cost is measured rather than
// assumed - eighty per cent on a target that caches nothing (E601) - and the
// tier it buys is measured too (E621), so the trade is an operator's to make
// with two numbers.
const EnvTrace = "EARTH_TRACE"

// tracing reports whether steps are watched.
func tracing() bool { return os.Getenv(EnvTrace) != "0" }

// GuestNote says the sandbox agent is older than this engine, or nothing.
//
// Asked of the executor rather than computed at the call site, because the
// executor is what knows where the guest came from - `$EARTH_GUESTD`, or beside
// this binary - and a note naming a different file than the one that ran would
// be worse than none (E499).
func (e *Executor) GuestNote() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}

	guest, _, err := findGuestCommand()
	if err != nil {
		return ""
	}

	return staleGuestNote(self, guest)
}

// DockerNote reports why a WITH DOCKER step was given no docker client, or
// empty if it was given one.
//
// Kept once and asked of the executor rather than announced, so the caller
// decides when a build-level note belongs in its output - the same shape as
// Degraded and as the case-insensitive store warning.
func (e *Executor) DockerNote() string {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.dockerNote
}

func (e *Executor) noteDocker(note string) {
	if note == "" {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.dockerNote == "" {
		e.dockerNote = note
	}
}

// sinkFor routes a step's live output, prefixed with where the step came from.
//
// The prefix is not decoration. Steps run concurrently, so their output
// interleaves; unattributed lines are worse than none, because a user reads one
// step's error under another step's heading and debugs the wrong command.
func (e *Executor) sinkFor(n *ir.Node) (write func(string, bool), done func()) {
	if e.Progress == nil && e.Capture == nil {
		return nil, func() {}
	}

	where := n.Meta.Source
	if where == "" {
		where = n.Op.Kind.String()
	}

	// Indexed by stream: 0 is stdout, 1 is standard error.
	var pending [2]string

	emit := func(line string, isErr bool) {
		if e.Progress != nil {
			e.Progress(where, line, n.Meta.RawOutput)
		}

		if e.Capture != nil {
			e.Capture(n, line, isErr)
		}
	}

	write = func(chunk string, isErr bool) {
		// Buffered to line boundaries: a write that splits mid-line would
		// otherwise produce a prefix in the middle of a sentence.
		//
		// **A tail per stream, not one between them.** A single tail joined
		// half a line of stdout to the next line of standard error, which is a
		// line neither of them printed.
		at := 0
		if isErr {
			at = 1
		}

		pending[at] += chunk

		for {
			i := strings.IndexByte(pending[at], '\n')
			if i < 0 {
				return
			}

			emit(pending[at][:i], isErr)

			pending[at] = pending[at][i+1:]
		}
	}

	// What is left when the step ends.
	//
	// Buffering to line boundaries dropped it: a command whose last line has no
	// newline printed nothing at all, and `printf hello` is such a command. It
	// costs more than a missing line on screen - `ARG V=$(cat ./content)` takes
	// its value from this stream, so the argument arrived empty and the failure
	// named an assertion three lines later (E449).
	//
	// Nothing is emitted when the output ended cleanly: flushing an empty
	// remainder would print a blank line after every step.
	done = func() {
		for at, tail := range pending {
			if tail == "" {
				continue
			}

			emit(tail, at == 1)

			pending[at] = ""
		}
	}

	return write, done
}

// hostStep runs a step on the invoking machine.
//
// No sandbox, no layer, no capture. That is what LOCALLY means, and the three
// go together: nothing confines it, so nothing bounds what it observed, so there
// is nothing that could honestly be cached (green paper I7). The scheduler
// enforces the same rule; saying it here as well means a component reports the
// truth it knows rather than relying on another to notice.
func (e *Executor) hostStep(ctx context.Context, n *ir.Node) (core.Result, error) {
	if len(n.Op.Args) == 0 {
		return core.Result{}, fmt.Errorf("RUN needs a command (%s)", n.Meta.Source)
	}

	cmd := osexec.CommandContext(ctx, n.Op.Args[0], n.Op.Args[1:]...) //nolint:gosec // the argv is the step

	// The project directory, because a LOCALLY target's commands are written
	// relative to the Earthfile that contains them. WORKDIR moves within it.
	cmd.Dir = e.Context
	if n.Op.Dir != "" && n.Op.Dir != "/" {
		cmd.Dir = filepath.Join(e.Context, filepath.Clean("/"+n.Op.Dir))
	}

	// A host step inherits the machine's environment, with ε layered on top.
	//
	// This is the opposite of a sandboxed step, and the difference is not a
	// relaxation. ε is restricted there because it must *bound what the step
	// observed* for the key to be sound (I3). A host step has no sound key by
	// construction - it is unsandboxed, so nothing bounds it, so it is never
	// cached (I7) - and the restriction therefore buys no correctness while
	// costing the feature entirely: with an empty environment there is no PATH,
	// and a LOCALLY target cannot run `tr`, `mkdir`, or anything else that is
	// not a shell builtin.
	cmd.Env = os.Environ()
	for k, v := range n.Op.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	write, flush := e.sinkFor(n)

	out, err := runHost(cmd, write)

	flush()

	if len(out) > maxHostOutput {
		out = out[:maxHostOutput]
	}

	if exitErr, ok := errors.AsType[*osexec.ExitError](err); ok {
		// It ran and failed. That is a result.
		return core.Result{Exit: exitErr.ExitCode(), Output: string(out)}, nil
	}

	if err != nil {
		return core.Result{}, fmt.Errorf("run %s on this machine: %w", n.Meta.Source, err)
	}

	// Captured is deliberately false: see above.
	return core.Result{Exit: 0, Output: string(out)}, nil
}

// maxHostOutput bounds what a host step's output can cost, as for a sandboxed
// one.
const maxHostOutput = 64 << 10

// runHost executes a command, streaming its output if anyone is listening.
func runHost(cmd *osexec.Cmd, sink func(string, bool)) ([]byte, error) {
	if sink == nil {
		return cmd.CombinedOutput() //nolint:wrapcheck // the caller classifies this
	}

	var (
		mu  sync.Mutex
		buf []byte
	)

	// Both streams into one buffer and apart to the sink, for the reason the
	// guest's `run` gives: a log wants them interleaved, a `$( )` substitution
	// wants stdout alone (E725).
	stream := func(isErr bool) hostWriter {
		return func(b []byte) (int, error) {
			mu.Lock()
			buf = append(buf, b...)
			mu.Unlock()

			sink(string(b), isErr)

			return len(b), nil
		}
	}

	cmd.Stdout, cmd.Stderr = stream(false), stream(true)

	err := cmd.Run()

	mu.Lock()
	defer mu.Unlock()

	return buf, err //nolint:wrapcheck // the caller classifies this
}

type hostWriter func([]byte) (int, error)

func (f hostWriter) Write(b []byte) (int, error) { return f(b) }

// NewHostOnly returns an executor with no sandbox, for a build whose every step
// runs on this machine.
//
// Any sandboxed step refuses rather than improvising one: an executor that
// quietly started a sandbox here would make "needs no sandbox" a guess the
// caller made and this type silently corrected.
func NewHostOnly() (*Executor, error) { return &Executor{}, nil }

// Sandbox is where this executor's layers live, if it has one.
func (e *Executor) Sandbox() Sandbox {
	if e.sb == nil {
		return hostOnlyStore{}
	}

	return e.sb
}

// hostOnlyStore stands in for a sandbox that does not exist. Its store is a
// directory beside the cache, because a host-only build still records what it
// did even though it caches nothing.
type hostOnlyStore struct{}

func (hostOnlyStore) Start(context.Context) (Conn, error) {
	return nil, errors.New("this build has no sandbox: every step runs on this machine")
}

func (hostOnlyStore) Stop() error      { return nil }
func (hostOnlyStore) StoreDir() string { return os.TempDir() }
func (hostOnlyStore) Confines() bool   { return false }

// Close stops the sandbox. Idempotent, because a deferred Close and an explicit
// one on an error path both run, and the second must not mask the first error.
func (e *Executor) Close() error {
	// Before the sandbox is let go: a release still running refers to a mount
	// inside it, and a build must never exit leaving one up.
	e.releases.wait()

	e.mu.Lock()
	defer e.mu.Unlock()

	// Nothing to stop if nothing started. Not merely wasteful: the backend is a
	// CLI that reports an unknown container as an error, so shutting down a
	// sandbox that never began turns a clean build into one that ends by
	// printing a failure.
	if e.closed || e.sb == nil || !e.running {
		return nil
	}

	e.closed = true

	err := e.sb.Stop()
	if err != nil {
		return fmt.Errorf("stop sandbox: %w", err)
	}

	return nil
}

// ClosedConn is a connection to a machine that is not there.
//
// The listing can say a VM is running when it is gone or wedged, and this is
// what that looks like from here: reads and writes fail immediately, so the
// handshake fails rather than hanging. Exported because the recovery from it is
// worth testing and cannot be provoked with a real VM on demand.
func ClosedConn() Conn {
	host, other := net.Pipe()
	_ = host.Close()
	_ = other.Close()

	return &pipeConn{Conn: host, other: other}
}

// LoopbackConn serves a guest in this process over an in-memory pipe, against
// the host filesystem.
//
// Not a mock: it is the real Server and the real wire format, only without a
// machine boundary, so the protocol is exercised identically to a real sandbox.
// Callers are responsible for nothing; the temporary root is left to the OS.
// **No context, deliberately.** It serves a guest for as long as the test that
// made it wants one, and the goroutine it starts is stopped by closing the
// connection rather than by cancelling somebody's request. A context threaded
// here would be whichever caller happened to construct it, which is not the
// lifetime being managed.
func LoopbackConn() Conn {
	// A failure here means no directory to remove either, so the fallback is
	// recorded as *not ours*: removing os.TempDir() on Close would take the
	// machine's whole scratch space with it.
	root, err := os.MkdirTemp("", "earthbuild-loopback-")
	if err != nil {
		return loopbackIn(os.TempDir(), "")
	}

	return loopbackIn(root, root)
}

// loopbackIn serves a guest rooted at `root`, owning `own` if it is not empty.
func loopbackIn(root, own string) Conn {
	host, guestSide := net.Pipe()

	srv := &guest.Server{Mat: &hostMat{root: root}, Unconfined: true}
	go func() { _ = srv.Serve(context.Background(), guestSide) }()

	// The directory is the connection's, so closing the connection takes it
	// away. It was not, and every call left one behind: 2890 of them on the
	// build box, whose root filesystem filled up and stopped the run gate
	// (E473).
	return &pipeConn{Conn: host, other: guestSide, root: own}
}

type pipeConn struct {
	net.Conn

	other net.Conn
	// root is the scratch directory this connection's guest lives in, empty
	// when the connection did not make one. Removed by Close.
	root string
}

// Scratch names the directory this connection owns, empty when it owns none.
//
// Exported for the same reason LoopbackConn is: the rule worth testing is that
// the directory goes away with the connection, and a test that looks for it by
// globbing `/tmp` is reading every other test's litter as well as its own.
func (p *pipeConn) Scratch() string { return p.root }

func (p *pipeConn) Close() error {
	err := p.Conn.Close()

	otherErr := p.other.Close()
	if err == nil && !errors.Is(otherErr, net.ErrClosed) {
		err = otherErr
	}

	// After the pipes, so nothing is still writing into it - and reported
	// rather than swallowed: a scratch directory that cannot be removed is how
	// a disk fills up quietly, which is the failure this whole thing came from.
	if p.root != "" {
		if rmErr := os.RemoveAll(p.root); err == nil {
			err = rmErr
		}

		p.root = ""
	}

	return err
}

// CheckRunnable reports whether this sandbox can execute a step built for a
// platform.
//
// `fork/exec /bin/sh: exec format error` is what running an amd64 binary on an
// arm64 machine looks like, and it names neither the platform nor the image nor
// the line. The sandbox knows what it can run and the step knows what it wants,
// so the two are compared before anything is executed.
//
// Only *executing* is refused. Cross-building is legitimate - a target that
// copies files for another architecture works perfectly well - so this belongs
// where a command is about to run rather than where an image is fetched.
//
// The variant is not the architecture: arm64/v8 runs arm64 code.
func CheckRunnable(sandbox, want, where string) error {
	return checkRunnableWith(sandbox, want, where, EmulatedPlatforms())
}

// checkRunnableWith is CheckRunnable against a stated set of emulated
// platforms, so the decision can be tested without a kernel register.
//
// **The sandbox is the second gate and used not to know.** `core.Worker`
// carries `Emulates`, filled from the same register, and the scheduler already
// places a foreign-platform step on a machine that names it. This refused the
// step anyway, on a plain comparison - so a build with qemu registered got past
// placement and failed here, told that "nothing emulates one on the other" by
// the one part of the engine that had not looked (E932).
func checkRunnableWith(sandbox, want, where string, emulates []ir.Platform) error {
	if sandbox == "" || want == "" {
		return nil
	}

	arch := func(p string) string {
		os, rest, ok := strings.Cut(p, "/")
		if !ok {
			return p
		}

		a, _, _ := strings.Cut(rest, "/")

		return os + "/" + a
	}

	if arch(sandbox) == arch(want) {
		return nil
	}

	// Emulation makes it runnable, which is the whole point of registering an
	// interpreter. Compared on OS and architecture rather than the whole string,
	// for the reason stated above: a variant is not an architecture.
	for _, e := range emulates {
		if arch(want) == e.OS+"/"+e.Arch {
			return nil
		}
	}

	return fmt.Errorf(
		"%s is for %s and this sandbox runs %s, so it cannot be executed here"+
			"\n  nothing emulates one on the other, so building for %s only moves the failure"+
			"\n  use an image that provides %s, or run this build on a %s machine",
		where, want, sandbox, want, sandbox, want)
}

// ExplainExec turns the kernel's `exec format error` into a sentence about
// architectures.
//
// It is what running a binary built for another machine looks like, and it
// names neither the binary's platform nor this one's. The commonest route to it
// is an image cached before this engine checked architectures: the step asks for
// the sandbox's own platform, so nothing compares them, and the first command
// fails with six words.
//
// Explained where it surfaces, because every route ends here - including the
// ones nobody has thought of. Anything else is passed through untouched: a
// command that exits 1 is not a platform problem, and dressing it as one sends
// the reader away from the cause.
func ExplainExec(err error, sandbox, where string) error {
	if err == nil || !strings.Contains(err.Error(), "exec format error") {
		return err
	}

	return fmt.Errorf("%w"+
		"\n  that is what a binary for another architecture looks like to the kernel"+
		"\n  this sandbox runs %s, so the image %s stands on has to provide it"+
		"\n  if this image was fetched before, clear the image cache and build again",
		err, sandbox, where)
}

// withTerminals gives a client the sandbox's descriptor channel, where it has
// one.
//
// An optional interface rather than a method on Sandbox, like `Remove` above:
// only a backend that can pass descriptors has one, and a backend that cannot -
// anything not on this machine - should not have to say so with a nil.
//
// A client without a channel refuses an interactive step by name, which is the
// answer for every arrangement that is not one host (E189).
func withTerminals(sb Sandbox, c *guest.Client) {
	t, ok := sb.(interface{ Terminals() *net.UnixConn })
	if !ok {
		return
	}

	c.Terminals = t.Terminals()
}

// terminalFor is the terminal a step is entitled to: its own, or none.
func terminalFor(n *ir.Node, tty *os.File) *os.File {
	if !n.Op.Interactive {
		return nil
	}

	return tty
}

// wouldPrime says whether a step's base is worth assembling lazily.
//
// Three things have to be true, and each absence means the same thing - assemble
// the layers. A step nobody has seen before has no prediction; a step with no
// base has nothing to prime from; an engine with no peers has no primer.
//
// Separate from `base` so the decision can be tested without a guest, which is
// the only part of it worth testing on its own.
func (e *Executor) wouldPrime(n *ir.Node, stack []ir.NodeID) bool {
	return e.Prime != nil && len(n.Meta.ReadsPredicted) > 0 && len(stack) > 0
}

// base assembles what a step reads, lazily when it can.
//
// **The whole of the difference between moving a layer and moving what was
// read.** With a primer and a prediction, a directory is primed with the paths
// the step is expected to open and handed to the guest as a prepared base
// (E300); anything unpredicted faults in while the step runs (E289). Without
// either, the base is the stack of layers it has always been.
//
// The fall back is to the ordinary path and not to a failure: a primer that
// cannot prime is a slower build, and every mechanism this rests on was built
// to degrade rather than refuse (I11, E302).
func (e *Executor) base(
	ctx context.Context, c *guest.Client, n *ir.Node, stack []ir.NodeID,
) (core.Handle, func(), error) {
	want := n.Meta.ReadsPredicted

	if e.wouldPrime(n, stack) {
		into, err := os.MkdirTemp(e.Scratch, "primed-")
		if err == nil {
			err = e.Prime(ctx, stack, want, into)
			if err == nil {
				h, herr := c.MaterialisePrepared(ctx, into)
				if herr == nil {
					// Under the name the guest will use when it faults a path
					// in (E303, E304). A handle with no name cannot be
					// answered for, so the base is thrown away rather than
					// left to fail every fault-in it receives.
					named, ok := h.(interface{ HandleID() string })
					if ok {
						e.remember(named.HandleID(), primedBase{stack: stack, into: into})

						return h, func() {
							e.forget(named.HandleID())
							_ = h.Release()
							_ = os.RemoveAll(into)
						}, nil
					}

					_ = h.Release()
				}
			}

			_ = os.RemoveAll(into)
		}
	}

	endMaterialise := phase("materialise", n.Meta.Source)
	h, err := c.Materialise(ctx, stack)
	endMaterialise()

	if err != nil {
		return nil, nil, fmt.Errorf("materialise the base for %s: %w", n.Meta.Source, err)
	}

	// Recorded even though nothing was primed, because the guest may still fault
	// against it: the tracer stops on any path that is not there, and a step
	// resolving a command walks PATH through several that never will be. A base
	// assembled whole answers those with an honest absence, where an unknown
	// handle has to be refused (see FillFor).
	// **Timed, because it was the largest thing the phase log did not show.**
	// A step's `exec` was 27.4ms against a `run` of 6.6ms, and the twenty-one
	// milliseconds between them were attributed to nothing. Releasing a handle
	// is an unmount and an `os.RemoveAll` - 15.8ms and 3.5ms - and it happens
	// once per step, on the way out, where no phase was watching (E817).
	if named, ok := h.(interface{ HandleID() string }); ok {
		e.remember(named.HandleID(), primedBase{stack: stack, complete: true})

		return h, func() {
			e.releases.release(releaseWidth(), func() {
				defer phase("release", n.Meta.Source)()

				e.forget(named.HandleID())
				_ = h.Release()
			})
		}, nil
	}

	return h, func() {
		e.releases.release(releaseWidth(), func() {
			defer phase("release", n.Meta.Source)()

			_ = h.Release()
		})
	}, nil
}

// Prewarm starts the sandbox's machine without waiting for anything to need it.
//
// The boot is otherwise deferred to first use, which is worth a whole VM on a
// build that turns out to run nothing - see client(). Deferring it also puts it
// squarely on the critical path of every build that *does* run something, where
// it cannot overlap the planning that precedes it.
//
// So it is started here instead, on the caller's goroutine, and the caller is
// expected to be one that does not wait: a build that needed no machine must not
// pay for one, and a machine booted on speculation is the machine the next build
// finds already running (E537).
//
// Backends that have nothing to warm say nothing.
func (e *Executor) Prewarm(ctx context.Context) {
	if p, ok := e.sb.(interface{ Prewarm(context.Context) }); ok {
		p.Prewarm(ctx)
	}

	// **And greet the guest, which is the other half of being ready.** Booting
	// the machine off the critical path left the handshake on it: a
	// change-one-file rebuild paid `sandbox:start` 0.044s and `sandbox:dial`
	// 0.062s in front of its first step, behind 0.174s of registry round trip
	// that needed neither.
	//
	// `client()` is a `sync.Once` over both halves, so this is the same
	// initialisation the first step would have run and not a second one - and a
	// failure is remembered there, to be reported by the step that needed it
	// rather than swallowed here.
	//
	// The error is discarded on purpose, for Prewarm's reason: this is an
	// optimisation, and one that cannot work must leave a build that is slower
	// rather than one that stops.
	_, _ = e.client()
}

// WarmImages performs the registry handshake for what this build will pull.
//
// **The third thing that need not wait for the machine.** Prewarm took the boot
// off the critical path and then the handshake with the guest; what is left in
// front of the first step is the pull, and the first 0.46s of a pull is
// `registry:token` - a round trip to a token service, on the host, needing
// nothing the VM provides. A cold build boots for 1.48s and then spends that
// 0.46s; done beside the boot it costs nothing at all (E907).
//
// Paid even by a pinned reference, which is why this is not answered by pinning
// the digest: pinning removes the resolution, not the pull.
//
// Nothing waits for these, and each is silent about failure - see image.Warm.
// A reference the build turns out not to pull has cost one exchange against a
// cache the pull would have filled anyway.
func (e *Executor) WarmImages(ctx context.Context, refs []string, platform string) {
	if len(refs) == 0 {
		return
	}

	// **A build can have no sandbox at all**, which is what a local-only build
	// is, and asking one for its store directory panics. The challenge cache is
	// the only thing the directory is for, and `image.Warm` treats an empty one
	// as "do not remember" - so a build without a machine still warms, it just
	// does not write down where the token came from.
	imageRoot := e.ImageCache
	if imageRoot == "" && e.sb != nil {
		imageRoot = e.sb.StoreDir()
	}

	for _, ref := range refs {
		// An image already unpacked here is not pulled again, so its handshake
		// is a request nobody reads.
		//
		// **Not a latency fix, and it was nearly reported as one.** Four
		// alternating pairs put the warm path 8ms slower with this warming
		// regardless; a third arm and five samples put all three within each
		// other's spread. The wall-clock effect is below the noise floor,
		// because the warm is asynchronous and nothing waits for it.
		//
		// It stays because the request is real even when the wait is not:
		// Docker Hub rates-limits by request, and a build that pulls nothing
		// should ask it for nothing.
		if alreadyLocal(imageRoot, ref, platform) {
			continue
		}

		go image.Warm(ctx, ref, image.Options{
			Platform:   platform,
			Challenges: imageRoot,
			Mirrors:    image.MirrorsFromEnv(),
		})
	}
}

// alreadyLocal reports whether this image has been unpacked into this store
// before, which is when the build will not pull it.
//
// Reads the same marker `materialiseImageApart` writes, so the two agree by
// construction rather than by having been written to match.
//
// **Wrong in the safe direction, both ways.** A marker left behind by layers
// that have since gone makes this skip a warm the pull then pays for itself,
// which is exactly the behaviour before E907. A missing marker makes it warm an
// image that turns out to be present, which costs one exchange against a cache.
// Neither can make a build incorrect, which is why a stat is enough and no
// agreement with the store is sought.
func alreadyLocal(imageRoot, ref, platform string) bool {
	if imageRoot == "" {
		return false
	}

	marker := filepath.Join(imageRoot, "imagecache", ImageCacheKey(ref, platform)+stackSuffix)

	_, err := os.Stat(marker)

	return err == nil
}

// Connected reports whether the guest has been greeted.
//
// Exported so that "the handshake happened beside the plan rather than in front
// of the first step" is observable rather than asserted in a comment - the same
// argument `Boots` and `Resumes` make about the VM.
func (e *Executor) Connected() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.running
}

// fillView tells the guest what a bound view shows, and at what size.
//
// The scheduler already hands this step the *stacks* of its sources, so the
// object is not looked up again here - it is matched by identity to the source
// it names. Matching rather than indexing because a step may bind several
// views and copy from other targets besides, and a position would be a second
// place for the two lists to agree.
//
// One layer goes as a layer and is bound directly; more than one has to be
// assembled first. They are the same idea at two sizes, and the small one is
// worth keeping: a view of the local context is the common case and needs no
// overlay, which also means it works where overlay cannot stack.
func fillView(gm *guest.Mount, m ir.Mount, n *ir.Node, sources [][]ir.NodeID) error {
	for i, src := range n.Sources {
		if src.ID() != m.From {
			continue
		}

		if i >= len(sources) {
			break
		}

		stack := sources[i]
		gm.Sub = m.Sub

		if len(stack) == 1 {
			gm.Layer = stack[0].String()

			return nil
		}

		gm.Stack = make([]string, 0, len(stack))
		for _, id := range stack {
			gm.Stack = append(gm.Stack, id.String())
		}

		return nil
	}

	// A view naming an object that is not one of this step's sources is a
	// planning fault, not an execution one: nothing would have built it and
	// nothing would have keyed it. Refused here rather than mounted empty,
	// because an empty mount is what a step reads as "the file is missing".
	return fmt.Errorf("%s binds %s at %s, which is not one of its sources",
		n.Meta.Source, m.From, m.Target)
}

// StoreHas reports which of these layers the store holds.
//
// **Asked of the guest, because the store may not be the host's.** With
// `EARTH_STORE_IN_VM` the layers live on a block device inside the VM, and a
// host that stats its own root reads an empty answer - which `Lookup` turns
// into a miss and a rebuild of everything already there.
//
// Exported so the tier that needs the answer can get it without reaching for a
// client of its own: the connection is this executor's, made once and shared.
func (e *Executor) StoreHas(ctx context.Context, ids []ir.NodeID) ([]ir.NodeID, error) {
	c, err := e.client()
	if err != nil {
		return nil, err
	}

	return c.StoreHas(ctx, ids)
}

// ViewDigests reports what a base holds at each of the given paths.
//
// Asked of the guest for `StoreHas`'s reason: with `EARTH_STORE_IN_VM` the base
// is on a block device inside the VM, and a host that reads it finds nothing -
// which the observed-input tier reports as every prediction being stale.
func (e *Executor) ViewDigests(
	ctx context.Context, stack []ir.NodeID, paths []string,
) (files, listings map[string]ir.NodeID, err error) {
	c, err := e.client()
	if err != nil {
		return nil, nil, err
	}

	return c.ViewDigests(ctx, stack, paths)
}

// localContextRefusal says why a local build context cannot be staged, or nil.
//
// **What is left after the handing-across exists.** The store and the context
// have to end on the same side: with the store on the host they already are,
// and with it on the guest's device the context is packed and handed over. A
// sandbox that cannot say where the guest sees a host path can do neither, and
// this is what it says instead of failing later as a missing artifact (E690).
func localContextRefusal() error {
	if !guest.StoreInVM() {
		return nil
	}

	return fmt.Errorf("COPY from the build context needs the layer store on the"+
		" host, or a sandbox that can hand the context to the guest, and %s put"+
		" the store on the guest's device"+
		"\n  the context is read here, and the guest cannot read a store it does"+
		" not hold"+
		"\n  unset %s for this build, or copy from a target instead of from the"+
		" context", guest.EnvStoreInVM, guest.EnvStoreInVM)
}

// copyContextInto stages what a build-context node names, into dir.
//
// Shared by the two placements: on the host the staged tree is renamed into the
// store, and with the store on the guest's device it is packed and handed
// across, because a rename does not cross a filesystem (E690). What is copied,
// and what is left out, must not depend on which.
func (e *Executor) copyContextInto(n *ir.Node, dir string) error {
	// The directory this entry was read from, which for a target in another
	// Earthfile is that Earthfile's own - not the invocation's.
	root := n.Meta.ContextRoot
	if root == "" {
		root = e.Context
	}

	src := filepath.Join(root, filepath.Clean("/"+n.Op.Args[0]))

	fi, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("build context %s (%s): %w", n.Op.Args[0], n.Meta.Source, err)
	}

	// Staged under the path it has in the context, so the guest can name it the
	// way the Earthfile does.
	dst := filepath.Join(dir, filepath.Clean("/"+n.Op.Args[0]))

	err = os.MkdirAll(filepath.Dir(dst), 0o755) //nolint:gosec // a directory a build writes into, as a shell would make it
	if err != nil {
		return fmt.Errorf("prepare the context layer: %w", err)
	}

	if fi.IsDir() {
		// **The same exclusions the digest was taken with.** The interpreter
		// applies the ignore file when it computes this context's identity, and
		// this used to copy everything - so `.earthlyignore` decided the cache
		// key and not what the container got (E623).
		err = copyDirExcluding(src, dst, ignore.For(root, src))
	} else {
		err = copyOut(src, dst)
	}

	if err != nil {
		return fmt.Errorf("stage the build context: %w", err)
	}

	return nil
}

// contextMedia is what a packed build context is: a tar and nothing else.
//
// Uncompressed deliberately. The bytes go from this machine to a guest sharing
// its page cache, over a mount, and compressing them would spend CPU on both
// sides to save a copy that is not the cost.
const contextMedia = "application/vnd.oci.image.layer.v1.tar"

// stageContextInGuest files a build context in a store this machine cannot
// write to.
//
// **The host stages and the guest places.** Publishing a layer renames a staged
// tree into position, and a rename does not cross a filesystem - so with the
// store on the guest's device a tree staged here can never become a layer there
// (E690). What crosses instead is a tar, which the guest unpacks into its own
// staging and publishes under the name the plan already chose.
//
// Named rather than digested, which is the whole reason this cannot reuse the
// image path: a context's identity was fixed when the interpreter digested the
// host directory and is already in the cache key of every step that copies from
// it. See Request.As.
func (e *Executor) stageContextInGuest(ctx context.Context, n *ir.Node) (core.Result, error) {
	if e.Context == "" {
		return core.Result{}, fmt.Errorf("no build context configured, but %s needs one", n.Meta.Source)
	}

	c, err := e.client()
	if err != nil {
		return core.Result{}, err
	}

	// **Asked, not stated.** The store is on a device this machine cannot read,
	// so whether the context is already filed is the guest's answer to give.
	held, herr := c.StoreHas(ctx, []ir.NodeID{n.ID()})
	if herr == nil && len(held) == 1 {
		return core.Result{Layer: n.ID(), Captured: e.sb.Confines()}, nil
	}

	seer, ok := e.sb.(interface{ GuestPath(string) (string, bool) })
	if !ok {
		return core.Result{}, fmt.Errorf("%w (%s)", localContextRefusal(), n.Meta.Source)
	}

	// Beside the image blobs, which is the directory already established as one
	// the host writes and the guest reads.
	blobs := filepath.Join(e.sb.StoreDir(), "blobs")

	err = os.MkdirAll(blobs, 0o750)
	if err != nil {
		return core.Result{}, fmt.Errorf("prepare somewhere to hand the context over: %w", err)
	}

	staged, err := os.MkdirTemp(blobs, ".context-")
	if err != nil {
		return core.Result{}, fmt.Errorf("stage the build context: %w", err)
	}

	defer func() { _ = os.RemoveAll(staged) }()

	// **Timed, because the step around it was the whole story.** A `COPY` of
	// 2000 files is 1.5s and had no sub-phase at all, so the profile said "this
	// step is slow" and nothing else - which is how `release` hid 71% of a step
	// until it was timed (E819). These two are the pass over the tree and the
	// pass back over it, and knowing the split is what decides whether packing
	// straight from the context is worth the change it takes (E829).
	tarball := filepath.Join(blobs, "context-"+n.ID().String()+".tar")

	// **One pass over the tree, or two.** Staging copies the context into a
	// directory and then reads all of it back to pack it; packing it where it
	// lies does that work once. 350ms of copying in front of 154ms of packing,
	// against 152ms to pack alone, over 2000 files (E829c).
	if directContextPack() {
		endPack := phase("context:pack", n.Meta.Source)
		err = e.packContextDirect(n, tarball)
		endPack()

		if err != nil {
			return core.Result{}, err
		}
	} else {
		endCopy := phase("context:copy", n.Meta.Source)
		err = e.copyContextInto(n, staged)
		endCopy()

		if err != nil {
			return core.Result{}, err
		}

		endPack := phase("context:pack", n.Meta.Source)
		err = packInto(staged, tarball)
		endPack()

		if err != nil {
			return core.Result{}, err
		}
	}

	// Kept only until the guest has read it: this is a copy of the context and
	// the layer it becomes is the thing worth keeping.
	defer func() { _ = os.Remove(tarball) }()

	at, visible := seer.GuestPath(tarball)
	if !visible {
		return core.Result{}, fmt.Errorf("the guest cannot see %s, so the build"+
			" context cannot be handed to it (%s)", tarball, n.Meta.Source)
	}

	id, err := c.UnpackLayerAs(ctx, at, contextMedia, n.ID())
	if err != nil {
		return core.Result{}, fmt.Errorf("file the build context (%s): %w", n.Meta.Source, err)
	}

	return core.Result{Layer: id, Captured: e.sb.Confines()}, nil
}

// packInto writes a directory to a tar file.
func packInto(dir, at string) error {
	f, err := os.Create(at) //nolint:gosec // a path this engine derived
	if err != nil {
		return fmt.Errorf("make room for the packed context: %w", err)
	}

	defer f.Close()

	_, _, err = image.Pack(dir, f)
	if err != nil {
		return fmt.Errorf("pack the build context: %w", err)
	}

	return f.Close()
}
