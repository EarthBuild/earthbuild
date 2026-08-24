package fleet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Runner turns assignments into replies, using this machine's executor.
//
// The worker's whole job, and the mirror of `Delegate`. Everything careful about
// it is in one direction: **an assignment arrives from somebody else**, so the
// conversion back into an operation refuses what it does not recognise rather
// than choosing a default.
//
// A delegate is an engine (C.3). Every invariant in §5 binds it as it binds the
// parent - which is why this hands the step to the same `core.Executor` a local
// build uses rather than to something simpler.
func Runner(
	e core.Executor, as core.Worker, opts ...RunnerOpt,
) func(context.Context, Assignment) (Reply, error) {
	cfg := runnerCfg{room: make(chan struct{}, DefaultCapacity())}
	for _, o := range opts {
		o(&cfg)
	}

	return func(ctx context.Context, a Assignment) (Reply, error) {
		if a.Version != Version {
			// Refused rather than attempted. A version this engine does not
			// speak is a message whose fields it cannot be sure of, and
			// guessing at one is how a protocol change becomes a wrong build.
			return Reply{
				Version: Version,
				Refused: fmt.Sprintf("this worker speaks version %d and was sent %d",
					Version, a.Version),
			}, nil
		}

		// A step for a machine this is not. The safety net under placement
		// rather than a substitute for it: the driver should not have sent this,
		// and when the two disagree - a stale inventory, a worker replaced - the
		// worker is the party that knows (I10, E267).
		//
		// Building it anyway would succeed and produce binaries for the wrong
		// machine, which is the failure with no symptom until somebody runs them.
		if mine := platformName(as.Platform); a.Platform != "" && mine != "" &&
			a.Platform != mine {
			return Reply{
				Version:  Version,
				Platform: mine,
				Refused: fmt.Sprintf("this worker is %s and the step is for %s",
					mine, a.Platform),
			}, nil
		}

		// **An assignment with no operation is a request to be ready.**
		//
		// The remaining cost of a fleet has a shape: one fetch per worker per
		// build, about 300ms, paid the moment the first step arrives - while
		// every other machine has nothing to do (E341). Making the fetch faster
		// is the wrong lever; it should already have happened.
		//
		// A prime is an assignment stripped of its step. No second message type,
		// no second path through a worker, and a worker too old to know it
		// refuses exactly as it refuses any unknown operation - which costs the
		// build nothing, because a prime is advice about *when* (I5).
		if a.Op.Kind == "" {
			return cfg.prime(ctx, as, a), nil
		}

		op, err := operationOf(a.Op)
		if err != nil {
			// A refusal, not a failure: the driver runs it somewhere that can
			// (I10, E235).
			return Reply{Version: Version, Refused: err.Error()}, nil
		}

		// The inputs, before the step that needs them. A worker on another
		// machine holds only what it has previously been sent (E258).
		//
		// **Off unless configured**, and that is not a silent degrade: a fleet
		// sharing one store - the in-process one, or two engines on a
		// developer's laptop - has nothing to move, and opening a fetch to
		// discover that would cost every such build a round trip. A worker on
		// its own machine is given somewhere to put blobs and somewhere to get
		// them, and then it provisions.
		// Where a fault-in goes, before anything can fault: the step is about to
		// run and the holders are only known here.
		if cfg.sink != nil {
			cfg.sink.Set(cfg.fragmenters(a))
		}

		var moved Transfer

		if cfg.into != nil || cfg.frags != nil {
			moved, err = cfg.provision(ctx, a)
			if err != nil {
				// A refusal: this machine could not get what the step needs, and
				// the driver may have somewhere that can. Not a failure, because
				// nothing about the step is wrong (I11).
				//
				// Whatever *did* arrive before it gave up is still counted: a
				// fetch that got three layers of four spent the bytes for three.
				return refusal(as, moved, err.Error()), nil
			}
		}

		// Room to run, or wait for it - **after** the inputs are here.
		//
		// A queue, not a refusal: turning work away because the machine is busy
		// would send the driver looking elsewhere while this machine is about to
		// be free, and on a fleet where everybody is busy that is a build that
		// fails for being popular (E271).
		//
		// Waiting *here* rather than before the fetch is what overlaps the two
		// costs a delegated step has. A worker with one slot used to take a
		// slot, then go looking for its base - so a machine with something to
		// run and something to fetch for did the fetching only once the running
		// was done, which is the one arrangement where a fast network buys
		// nothing (E275).
		//
		// The bandwidth is spent before the step is certainly going to run. That
		// is the trade: a cancelled build has fetched something it did not use,
		// against every queued step paying its transfer in series.
		queued := time.Now()

		select {
		case cfg.room <- struct{}{}:
			defer func() { <-cfg.room }()

		case <-ctx.Done():
			return refusal(as, moved, "the build was cancelled while this"+
				" worker was full"), nil
		}

		n := &ir.Node{
			Op:       op,
			Platform: platformOf(a.Platform),
			Meta: ir.Meta{
				Description: "delegated " + string(a.Op.Kind),
				// What this step read last time, on its way to whatever
				// assembles the base: the executor is what materialises, and
				// only the node reaches it (E301). Advice, and not in the
				// identity - there is a test in engine/ir that says so.
				ReadsPredicted: a.Hints.ReadsPredicted,
			},
		}

		// Timed here, because the worker is the only party that can. The
		// driver's round trip includes the queue, the transfer and the network;
		// the difference between that and the step itself is what the account
		// calls overhead, and it is meaningless if one side of the subtraction
		// is always zero (E276).
		//
		// The *step*, not the assignment: the wait for a slot and the transfer
		// are counted elsewhere, and folding them in here would hide them inside
		// the one number nobody would then question.
		waited := time.Since(queued)

		ran := time.Now()

		res, err := e.Run(ctx, n, as, a.Base, a.Sources)

		// **A prediction that was wrong is not a step that cannot run.**
		//
		// A hint is advice (I5), and a worker that believed a wrong one has
		// fetched the wrong tenth of a base - so the step asks for a file that
		// is not there and the executor says so with `ErrInputMissing`. Refusing
		// would send it back to the driver, which is correct and means the fleet
		// stops being used the moment a prediction is imperfect: always, in any
		// real build (E327).
		//
		// One retry. The second attempt stands on the whole base, so a third
		// could only repeat it - and the prediction is cleared, because it has
		// just been shown to be wrong about this step.
		for tries := 0; errors.Is(err, core.ErrInputMissing) && tries < faultRounds; tries++ {
			more, again, ferr := cfg.faultIn(ctx, &a, n, err)

			moved.Bytes += more.Bytes
			moved.Took += more.Took

			if ferr != nil {
				break
			}

			res, err = e.Run(ctx, n, as, a.Base, a.Sources)

			// The whole base was fetched, so there is nothing further to get.
			// Without this a worker whose step keeps asking for something no
			// layer contains restarts it `faultRounds` times for nothing.
			if !again {
				break
			}
		}

		took := time.Since(ran)

		if err != nil {
			// The step could not be *started* - a missing binary, a sandbox
			// that would not boot. That is this machine's problem and not the
			// step's, so it is a refusal and the driver may run it elsewhere.
			//
			// **Carrying what was moved**, because it was moved. A step that
			// pulled four hundred megabytes and then failed on a missing binary
			// did not cost nothing, and an account that says so makes a fleet
			// look cheapest exactly when it is being least useful (E270).
			return refusal(as, moved, err.Error()), nil
		}

		reply := replyOf(res)
		reply.DurationMillis = took.Milliseconds()
		reply.Platform = platformName(as.Platform)
		reply.Capacity = cap(cfg.room)
		reply.QueueMillis = waited.Milliseconds()
		reply.FetchedBytes = moved.Bytes
		reply.FetchMillis = moved.Took.Milliseconds()
		// Where the next step needing this layer should look first. A worker
		// that does not say where it is cannot be fetched from, and every
		// later step goes back to the driver (E260).
		reply.HeldAt = cfg.at

		return reply, nil
	}
}

// operationOf turns a wire operation back into one this engine can run.
//
// The reverse of `kindOf`, and a switch with no default that guesses: a kind
// this engine does not know is refused, because the alternative is running
// something a peer named and this engine interpreted differently.
func operationOf(o Op) (ir.Op, error) {
	var kind ir.OpKind

	switch o.Kind {
	case KindExec:
		kind = ir.OpExec

	case KindFile:
		kind = ir.OpFile

	case KindImage:
		kind = ir.OpImage

	case KindBuild:
		// Delegation of a whole target, which needs an engine of its own on
		// this machine. Refused until there is one, and refused *by name* so
		// the driver's message says what is missing rather than that something
		// went wrong.
		return ir.Op{}, fmt.Errorf("%w: this worker cannot take a whole target yet",
			ErrNotDelegable)

	default:
		return ir.Op{}, fmt.Errorf("%w: %q is not an operation this worker knows",
			ErrNotDelegable, o.Kind)
	}

	op := ir.Op{
		Kind: kind, Args: o.Args, Env: o.Env,
		Dir: o.Dir, User: o.User, NoNetwork: o.NoNetwork,
	}

	// The private caches, rebuilt exactly as the invoker had them. Exactly,
	// because both ends key on this operation: a worker running the command
	// without the mount captures into its result what the invoker discards, and
	// files it under the invoker's key (E433).
	for _, target := range o.Scratch {
		op.Mounts = append(op.Mounts, ir.Mount{Target: target, Ephemeral: true})
	}

	return op, nil
}

// replyOf is what this worker says about a step it ran.
//
// A non-zero exit is a **result** and travels as one: the step ran and said no,
// and the driver should fail the build with its output rather than try the step
// somewhere else (E232). Only a step that could not run at all is a refusal.
func replyOf(res core.Result) Reply {
	return Reply{
		Version: Version,
		Layer:   res.Layer, Content: res.Content,
		Exit: res.Exit, Bytes: res.Bytes,
		Observation: Observation{
			Reads:      res.Observation.Reads,
			Negative:   res.Observation.Negative,
			Listings:   res.Observation.Listings,
			Incomplete: res.Observation.Incomplete,
		},
	}
}

// platformName writes a platform, and writes an unknown one as nothing.
//
// `ir.Platform{}.String()` is "/", which is a name rather than an absence - and
// a worker announcing "/" claims to be a machine, so placement would refuse it
// for every step instead of falling through to the "nobody knows" case. The
// empty string is what "I do not know what I am" has to look like on the wire.
func platformName(p ir.Platform) string {
	if p == (ir.Platform{}) {
		return ""
	}

	return p.String()
}

// platformOf reads a platform as `Platform.String` writes one.
//
// There is no parser in `ir` because nothing needed one until a platform crossed
// a wire: inside one process a platform is a struct and never a string. Written
// here rather than added there, because the direction only exists for the wire -
// and an empty or unreadable value is the zero platform, which means "this
// machine's", exactly as an absent one does everywhere else.
func platformOf(s string) ir.Platform {
	if s == "" {
		return ir.Platform{}
	}

	parts := strings.SplitN(s, "/", 3)

	out := ir.Platform{OS: parts[0]}
	if len(parts) > 1 {
		out.Arch = parts[1]
	}

	if len(parts) > 2 {
		out.Variant = parts[2]
	}

	return out
}

// RunnerOpt configures a worker.
type RunnerOpt func(*runnerCfg)

type runnerCfg struct {
	// room bounds how many steps run at once. See WithCapacity.
	room chan struct{}
	into Keeper
	from []Source
	at   string
	dial func(string) (Source, error)
	// known are the peers already dialled, by address. See dialed.
	knownMu sync.Mutex
	known   map[string]Source
	// frags is where parts of layers are kept, and fragFrom where they can be
	// got from when no dialled holder can serve them. See WithFragments.
	frags    *Fragments
	fragFrom []Fragmenter
	// sink is where this worker's executor faults in from, refreshed with the
	// holders of every assignment. See WithPeerSink.
	sink *Peers
	// fetching serialises transfers on this worker. See provision.
	fetching sync.Mutex
}

// provision brings in what this step needs, one transfer at a time.
//
// **A worker has one uplink.** Steps run concurrently, and without this each of
// them looks, sees the base is absent, and fetches it - so a machine pulls the
// same hundreds of megabytes down the same pipe several times over. Measured on
// an eight-way fan-out over three workers: five copies of a base where two would
// do (E266). Fetching twice at once does not halve the time, it halves the
// share.
//
// The cheap check happens **outside** the lock. A step that needs nothing is the
// common case once a fleet is warm, and making it queue behind a transfer it has
// no use for would trade one waste for another. The check is repeated inside
// `Provision`, which is what makes the second caller find what the first brought
// rather than fetch it again.
func (c *runnerCfg) provision(ctx context.Context, a Assignment) (Transfer, error) {
	// **Part of a base, when the driver said what the step reads.**
	//
	// Here rather than in an executor, because this is where a worker's sources
	// are: the holders the driver named, corrected, dialled, with the driver
	// last (C.4). The probe used to do it with an endpoint chosen before the
	// assignment arrived and reached the wrong protocol, so every lazy run
	// between machines silently fetched whole layers instead (E314, E323).
	//
	// A fragment from a peer is then the same mechanism as a layer from a peer,
	// and a worker that has one and not the other is not a case anybody has to
	// think about.
	if c.frags != nil && len(a.Hints.ReadsPredicted) > 0 {
		// **The cheap check outside the lock**, as the whole-layer path below
		// has always had. Without it every step after the first on a worker
		// waits for a transfer it has no use for: measured at half a second per
		// delegated step, fixed rather than proportional to the work, which is
		// the signature of a queue (E335).
		if !lackingParts(c.frags, a) {
			return Transfer{}, nil
		}

		return c.uplink(ctx, func() (Transfer, error) {
			return ProvisionFragments(ctx, c.frags, a, c.fragmenters(a)...)
		})
	}

	return c.provisionWhole(ctx, a)
}

// provisionWhole brings in every input entire, ignoring any prediction.
//
// What a worker does when it was never given one, and what it falls back to when
// the one it was given turned out to be wrong (E327).
func (c *runnerCfg) provisionWhole(ctx context.Context, a Assignment) (Transfer, error) {
	if c.into == nil || len(lacking(c.into, a)) == 0 {
		return Transfer{}, nil
	}

	return c.uplink(ctx, func() (Transfer, error) {
		return Provision(ctx, c.into, a, c.sources(a)...)
	})
}

// uplink runs one transfer at a time and counts the waiting as part of it.
//
// **A queue for the uplink is transfer time, and it was nothing at all.** Both
// provisioning paths started their clocks *after* this lock, so a step waiting
// behind another machine's fetch reported only its own - and the driver, which
// computes the wire by subtracting what a worker reports from the round trip,
// attributed every second of that queue to the network. At four workers that was
// 506ms a step of "wire" that was not the wire (E336).
//
// The distinction matters because the two have different fixes: an expensive
// protocol is not an expensive uplink, and the account is what decides which one
// a fleet has.
func (c *runnerCfg) uplink(ctx context.Context, fetch func() (Transfer, error)) (Transfer, error) {
	began := time.Now()

	c.fetching.Lock()
	defer c.fetching.Unlock()

	err := ctx.Err()
	if err != nil {
		return Transfer{Took: time.Since(began)}, fmt.Errorf("wait for this worker's uplink: %w", err)
	}

	moved, err := fetch()

	// The whole wait, not the transfer within it.
	moved.Took = time.Since(began)

	return moved, err
}

// fragmenters is which of this worker's sources can send part of a layer.
//
// The same list, in the same order, filtered - not a second configuration. A
// peer that speaks the blob protocol can do both, and one that cannot is simply
// not asked: `ProvisionFragments` falls through to the next, and a base nobody
// can fragment is a refusal the driver can act on (I11).
func (c *runnerCfg) fragmenters(a Assignment) []Fragmenter {
	out := make([]Fragmenter, 0, len(c.fragFrom)+1)

	for _, src := range c.sources(a) {
		if f, ok := src.(Fragmenter); ok {
			out = append(out, f)
		}
	}

	return append(out, c.fragFrom...)
}

// sources is where to look for this assignment's inputs, nearest first.
//
// Holders from the hint before the configured sources, which is C.4's order and
// the difference between a mesh and a star: the machine that produced a layer is
// the closest copy of it, and asking the driver first means the driver's uplink
// is the whole fleet's bandwidth.
//
// A holder that cannot be dialled is skipped rather than fatal. The address came
// from another machine's claim about itself and may be stale, wrong or simply
// unreachable from here - none of which is a reason to fail a step whose bytes
// the driver still has.
func (c *runnerCfg) sources(a Assignment) []Source {
	if c.dial == nil || len(a.Hints.Holders) == 0 {
		return c.from
	}

	out := make([]Source, 0, len(a.Hints.Holders)+len(c.from))

	for _, at := range a.Hints.Holders {
		s, err := c.dialed(at)
		if err != nil || s == nil {
			// **A holder that will not dial becomes a source that says so.**
			//
			// Skipping silently is right for the build - somebody else may have
			// it - and wrong for anybody trying to find out why nothing was
			// fetched: a worker with one holder that would not parse ends up
			// with no sources at all, and reports "no source had it", which is
			// true and useless (E309).
			//
			// Carried as a source rather than logged, because `Provision`
			// already keeps the last source's reason and this way it reaches
			// the refusal the driver prints.
			if err == nil {
				err = errors.New("nothing to dial")
			}

			out = append(out, &unreachable{at: at, why: err})

			continue
		}

		out = append(out, s)
	}

	return append(out, c.from...)
}

// WithBlobs gives a worker somewhere to keep inputs and somewhere to get them.
//
// Without it a worker runs steps out of whatever its executor's store already
// holds, which is right for a fleet that shares one and wrong for a fleet that
// does not - so the two are told apart by configuration rather than guessed at.
func WithBlobs(into Keeper, from ...Source) RunnerOpt {
	return func(c *runnerCfg) {
		c.into = into
		c.from = from
	}
}

// WithFragments makes a worker fetch only what a step is predicted to read.
//
// **Off unless configured**, and the whole feature turns on one hint: a driver
// that says nothing about what a step reads gets a worker that fetches whole
// layers, which is what it did before and what a first build has to do anyway.
//
// `from` is a fallback. The sources a worker already has - the holders the
// driver named - are asked first, and any of them that can send a fragment
// does; this is for a fleet whose peers cannot be dialled at all.
func WithFragments(into *Fragments, from ...Fragmenter) RunnerOpt {
	return func(c *runnerCfg) {
		c.frags = into
		c.fragFrom = from
	}
}

// WithPeerSink tells a worker's executor where to fault a path in from.
//
// The holders are per assignment and only this function sees them; an executor
// must not know what a fleet is. The sink is the value both hold - the caller
// gives it to `Runner` and to whatever fills paths in, and every assignment
// refreshes it (E329).
func WithPeerSink(p *Peers) RunnerOpt {
	return func(c *runnerCfg) { c.sink = p }
}

// WithPeers lets a worker fetch from other workers, and be fetched from.
//
// `at` is what this worker announces as its own address; `dial` turns a holder
// hint into a source. Both are optional and independent: a worker that can dial
// but announces nothing still relieves the driver of serving it, and one that
// announces without dialling still lets others relieve the driver of serving
// them.
func WithPeers(at string, dial func(string) (Source, error)) RunnerOpt {
	return func(c *runnerCfg) {
		c.at = at
		c.dial = dial
	}
}

// refusal is a reply that declines the step, and says what it already cost.
//
// One constructor rather than four literals, because the field that kept being
// left out is the one nobody notices the absence of: a refusal that under-reports
// a transfer is indistinguishable from a step that never needed one, and the
// difference only shows up as an account that quietly does not add up (E270).
func refusal(as core.Worker, moved Transfer, why string) Reply {
	return Reply{
		Version:      Version,
		Platform:     platformName(as.Platform),
		FetchedBytes: moved.Bytes,
		FetchMillis:  moved.Took.Milliseconds(),
		Refused:      why,
	}
}

// DefaultCapacity is how many steps a worker runs at once when nobody says.
//
// The machine's cores, because the driver cannot know and a fixed guess is wrong
// on every machine but one. **A number rather than "unlimited"**: unlimited is
// what a worker had before this, which made one machine infinitely parallel and
// no number of machines able to beat it (E271) - and told the driver it was
// never busy, which is the input its placement decides on (E266).
func DefaultCapacity() int {
	if n := runtime.NumCPU(); n > 0 {
		return n
	}

	return 1
}

// WithCapacity sets how many steps this worker runs at once.
//
// Zero or less means the default. A capacity below one would be a worker that
// never starts anything, which is a configuration mistake rather than a choice
// worth honouring.
func WithCapacity(n int) RunnerOpt {
	return func(c *runnerCfg) {
		if n < 1 {
			n = DefaultCapacity()
		}

		c.room = make(chan struct{}, n)
	}
}

// AtDriver rewrites a hint whose host is unspecified to the driver's own.
//
// The driver has the same problem a worker has - it does not know its own
// externally visible address - and it cannot be corrected the way a worker's is,
// because nobody observed *its* connection. But every worker was **told** where
// the driver is, and a hint with an unspecified host can only have come from the
// machine that composed the hint (E277).
//
// So the substitution is exact rather than a guess: a worker's address is
// corrected by the driver, which saw it, and the driver's is corrected by the
// worker, which was told it. Neither party guesses about itself.
func AtDriver(driver string) func(string) string {
	return func(at string) string {
		id, host, ok := strings.Cut(at, "@")
		if !ok || id == "" || host == "" {
			return at
		}

		ap, err := netip.ParseAddrPort(host)
		if err != nil || !ap.Addr().IsUnspecified() {
			return at
		}

		to, err := netip.ParseAddrPort(driver)
		if err != nil {
			return at
		}

		return id + "@" + netip.AddrPortFrom(to.Addr().Unmap(), ap.Port()).String()
	}
}

// unreachable is a holder that could not be dialled, kept so its reason travels.
type unreachable struct {
	at  string
	why error
}

func (u *unreachable) Name() string { return u.at }

func (u *unreachable) Fetch(
	context.Context, []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	return nil, u.why
}

// faultRounds bounds how many times a worker will fetch what a step asked for
// and try again.
//
// **Cheap faults are cheap and pathological ones are not free.** Each round
// costs one file and one restart of the step; a prediction that is wrong about
// most of a base would otherwise restart it hundreds of times, and the answer to
// that is the whole layer - which the last round does.
//
// Small, because a prediction wrong more than a handful of times is a prediction
// worth abandoning rather than repairing.
const faultRounds = 4

// faultIn gets what a step asked for and was not given.
//
// The named file if the executor knew which one and this worker fetches parts of
// layers; the whole base otherwise, and on the last round regardless. Each round
// adds the faulted path to the prediction, so the retry is told about it and
// primes with it - a retry told nothing would fault on the same file for ever.
func (c *runnerCfg) faultIn(
	ctx context.Context, a *Assignment, n *ir.Node, why error,
) (moved Transfer, again bool, err error) {
	var miss core.MissingInput

	if c.frags != nil && errors.As(why, &miss) && miss.Path != "" {
		a.Hints.ReadsPredicted = append(a.Hints.ReadsPredicted, miss.Path)
		n.Meta.ReadsPredicted = a.Hints.ReadsPredicted

		got, provisionErr := c.provision(ctx, *a)
		if provisionErr == nil {
			return got, true, nil
		}
		// Could not get the file on its own. The whole layer is the answer to
		// that, and the caller's next round does not get another chance at the
		// cheap path.
		c.frags = nil
	}

	if c.into == nil {
		return Transfer{}, false, errNoWhereToPut
	}

	// Everything, and no prediction: this attempt stands on the whole base, so
	// telling it what to prime would only make it prime part of what it has.
	n.Meta.ReadsPredicted = nil

	got, err := c.provisionWhole(ctx, *a)

	return got, false, err
}

// errNoWhereToPut marks a worker with no store to fetch into.
var errNoWhereToPut = errors.New("this worker has nowhere to put a layer")

// lackingParts is whether any of this step's inputs is missing the paths it was
// predicted to read.
//
// The same question `ProvisionFragments` asks, asked again outside the transfer
// lock so a warm worker does not queue. Repeated rather than shared, because the
// point of asking twice is that the answer may change between the two: the first
// caller fetches and the second finds what it brought.
func lackingParts(into *Fragments, a Assignment) bool {
	for _, id := range standsOn(a) {
		if !into.Has(id, a.Hints.ReadsPredicted) {
			return true
		}
	}

	return false
}

// dialed is a peer, dialled once and kept.
//
// **A connection costs 25ms on loopback before it has moved anything**, and
// there is no network there to blame - so between machines, with a real round
// trip and path discovery, it is most of what a small fetch costs (E336, E337).
//
// Nothing was reused because nothing could be: a fresh source was built for
// every holder of every assignment, so a connection cache inside one had nobody
// to be a cache for.
//
// Failures are not cached. A holder that would not dial this time may dial next
// time - a worker rebooting, a network settling - and remembering a refusal
// would make the first minute of a fleet's life permanent.
func (c *runnerCfg) dialed(at string) (Source, error) {
	c.knownMu.Lock()
	defer c.knownMu.Unlock()

	if s, ok := c.known[at]; ok {
		return s, nil
	}

	s, err := c.dial(at)
	if err != nil || s == nil {
		return s, err
	}

	if c.known == nil {
		c.known = map[string]Source{}
	}

	c.known[at] = s

	return s, nil
}

// prime brings in what a step will need, without running one.
//
// Reported like any other reply, because it is where a fleet spends its first
// second and an account that could not see it would call the cost of being
// ready the cost of the step that found it missing.
func (c *runnerCfg) prime(ctx context.Context, as core.Worker, a Assignment) Reply {
	if c.into == nil && c.frags == nil {
		// Nowhere to put anything. Not a refusal: a worker that shares the
		// driver's store is already as ready as it can be.
		return Reply{Version: Version, Platform: platformName(as.Platform)}
	}

	moved, err := c.provision(ctx, a)
	if err != nil {
		// A prime that could not run is not a build that cannot proceed: the
		// step it was for will fetch when it arrives, more slowly (I11).
		return refusal(as, moved, err.Error())
	}

	return Reply{
		Version:      Version,
		Platform:     platformName(as.Platform),
		Capacity:     cap(c.room),
		HeldAt:       c.at,
		FetchedBytes: moved.Bytes,
		FetchMillis:  moved.Took.Milliseconds(),
	}
}
