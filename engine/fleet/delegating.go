package fleet

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Delegating runs a step here or asks a worker to.
//
// A `core.Executor`, because that is the seam the scheduler already routes
// through: placement decides *which* worker a step belongs to and this decides
// what that means. Nothing above it learns that a fleet exists.
type Delegating struct {
	// Local runs a step on this machine, and is what every non-delegable step
	// falls back to.
	Local core.Executor
	// Fleet reaches the workers. A nil one means every step is local, which is
	// what a build with no fleet configured does.
	Fleet Transport
	// Note receives one line, at most once, when a build that expected a fleet
	// stops having one. Nil means nobody is listening.
	//
	// **Once**, because a build with five hundred delegable steps would
	// otherwise print five hundred identical lines, which is how a message
	// stops being read. And only for a fleet that has *gone* - a step that could
	// never be delegated runs locally by design on a fleet in perfect health,
	// and reporting that would cry wolf on every build with a secret in it
	// (E257).
	Note func(string)
	// Sizes says how large a layer is, for a layer this driver did not produce.
	//
	// A seed base, an image pulled from a registry: the driver holds it and no
	// step of this build made it, so nothing here knows its size - and without
	// a size, placement prices shipping it the same as shipping nothing (E317).
	//
	// Optional. Zero from it means "not known", and an assignment with any
	// unknown input states no size at all rather than a partial one.
	Sizes func(ir.NodeID) int64
	// Store is this machine's layers, and Peers turns a holder into somewhere to
	// fetch from. Together they are how a layer a *worker* produced gets back
	// here when something has to run on the invoking machine (E274).
	//
	// Both optional. A fleet sharing one store has nothing to bring back, and a
	// build with no local steps never needs to - but a build with either and
	// neither of these fails at the last step, having done all the work.
	Store Keeper
	Peers func(string) (Source, error)
	// Predict says what a step read last time, so a worker can fetch part of a
	// base instead of all of it (E287).
	//
	// Advisory in the strongest sense: a worker that ignores it produces the
	// same answer having moved more bytes, and a worker that believes a wrong
	// one faults on what it was not told about and fetches that too. Nothing
	// here can change a result, which is what makes it safe to send a guess
	// (I5).
	//
	// Nil means this driver has nothing to say, which is what a first build has.
	Predict func(*ir.Node) []string
	// Room is how many steps this machine runs at once.
	//
	// What stops E320 from moving a queue instead of removing one: keeping a
	// step because the fleet is busy is right only while this machine can
	// actually take it. Eight steps against a fleet with two slots and a driver
	// with two produced two delegated and six queued *here* (E321).
	//
	// Zero means "as many as arrive", which is what a driver with no executor
	// of its own effectively has and what every build did before this.
	Room int
	// Self is where this driver serves blobs, if it does.
	//
	// Named **last** among a step's holders, after every peer: the driver holds
	// the base of every build and is the one address every worker can reach, so
	// it is the fallback that makes a worker need no configuration of its own -
	// and a driver that named itself *first* would be the star topology E260
	// exists to avoid, arrived at from the other end (E277).
	Self string

	lost    sync.Once
	refused sync.Once
	kept    sync.Once
	primed  sync.Once
	// flight is how many steps are with the fleet right now, here how many are
	// running on this machine, and room the largest capacity any worker has
	// admitted to. See fleetFull.
	flight atomic.Int64
	here   atomic.Int64
	// waiting is how many steps are held at the pilot gate. See learn.
	waiting atomic.Int64
	room    atomic.Int64
	// flying is claimed by the first step to be delegated without evidence, and
	// gate/learnt are how the rest wait for what it learns. See learn.
	flying atomic.Bool
	gate   sync.Once
	opened sync.Once
	learnt chan struct{}
	acct   account
	held   holders
	// known is the size of every layer this build produced, which is half of
	// what pricing a transfer needs. The other half is `Sizes`.
	known measured
	// rate is what this fleet has been measured to cost, driver-side. The
	// rendezvous keeps its own for ordering workers; this one answers a
	// different question - whether to involve a worker at all.
	rate Rate
}

// Spend is where this build's wall-clock went (E259).
func (d *Delegating) Spend() Spend { return d.acct.spend() }

// NoteSpend records one delegated step's cost, for tests and for callers that
// drive the account themselves.
//
// Exported because the split between a queue and the wire is arithmetic worth
// asserting directly: it is done by subtraction from two clocks, and a sign
// error there is a report that reads plausibly and says the opposite thing
// (E336).
func (d *Delegating) NoteSpend(r Reply, round time.Duration) {
	d.acct.delegated(round, r)

	// **And the rate**, because what a step cost and what the fleet costs are
	// the same reply read twice. They were updated in two places, so a caller
	// that recorded a step's cost got an account that had seen it and a price
	// that had not (E351).
	d.rate.Observe(r.FetchedBytes, r.FetchMillis, r.DurationMillis)
}

// Run places a step, delegating it when that is both possible and asked for.
//
// **Refusing to delegate is not refusing to build.** A step carrying a secret, a
// cache mount or a `host` op cannot be expressed in an assignment (E230), and
// the answer is to run it here rather than to fail: the work is perfectly
// possible, it is only this machine that can do it. §4.7.1 already keeps
// placement from putting a `host` step on a worker, so reaching that case means
// two things disagreed - and the safe direction is to build.
func (d *Delegating) Run(
	ctx context.Context, n *ir.Node, w core.Worker, base []ir.NodeID, sources [][]ir.NodeID,
) (core.Result, error) {
	if d.Fleet == nil || w.IsInvoker {
		return d.local(ctx, n, w, base, sources)
	}

	a, err := Delegate(n, base, sources)
	if err != nil {
		if errors.Is(err, ErrNotDelegable) {
			return d.local(ctx, n, w, base, sources)
		}

		return core.Result{}, err
	}

	// Where this step's inputs can be got from, nearest first. Advice: a worker
	// that ignores it fetches from the driver and is merely slower (E260).
	a.Hints.ReadsPredicted = d.predicted(n)
	a.Hints.Holders = d.held.of(a)
	if d.Self != "" {
		a.Hints.Holders = append(a.Hints.Holders, d.Self)
	}

	// What delegating this would cost to make possible. The only number
	// placement has that is about *bytes* rather than about queueing, and
	// without it every base is priced the same however large it is (E317).
	a.Hints.Bytes = d.bytesOf(a)

	// Every worker is told what this build stands on, once, before any of them
	// is asked to do anything with it.
	d.primeAll(ctx, a)

	// **Asked twice, and the first time is not redundant.** A fleet that is
	// already fuller than this machine is a reason to run here whatever the
	// transfer would cost, and waiting to hear a price that cannot change the
	// answer is precisely the queue this exists to avoid (E321).
	if why, keep := d.keepHere(a); keep {
		d.noteKept(n, why)

		return d.local(ctx, n, w, base, sources)
	}

	// One step finds out what the fleet costs before a whole wave commits to it.
	d.learn(ctx, a)

	if why, keep := d.keepHere(a); keep {
		d.noteKept(n, why)

		return d.local(ctx, n, w, base, sources)
	}

	began := time.Now()

	d.flight.Add(1)

	r, err := d.Fleet.Assign(ctx, a)

	d.flight.Add(-1)
	if err != nil {
		// A fleet that cannot take the step is not a build that cannot proceed.
		// Both of the errors this can be - nobody available, everybody gone -
		// leave the work possible here, and a build that failed because a
		// worker rebooted would be worse than a slow one (I11).
		if errors.Is(err, ErrNoWorker) || errors.Is(err, ErrWorkerGone) {
			d.learned()
			d.noteLost(n, err)

			return d.local(ctx, n, w, base, sources)
		}

		return core.Result{}, fmt.Errorf("delegate %s: %w", n.Meta.Source, err)
	}

	d.NoteSpend(r, time.Since(began))
	d.held.record(r)
	// **And what it stood on.** A worker that answered had the step's inputs, so
	// it is now a copy of them - and holders were recorded only for layers a
	// worker *produced*, while a base is produced by nobody in this build. Every
	// worker went on fetching every base from the driver, whose uplink is then
	// the fleet's bandwidth (E260, met from a third direction in E325).
	//
	// Advice, like every holder hint: a worker that has only part of a base
	// answers "absent" for the whole and the asker falls through (I6).
	d.held.also(standsOn(a), r.HeldAt)
	d.known.grew(r.Layer, r.Bytes)
	d.learned()
	d.roomy(r.Capacity)

	if r.Refused != "" {
		// The worker is an engine and refused, which is a thing engines do
		// (I10). It is not a build failure and not this worker's fault: run it
		// here, where the construct may well be implemented.
		//
		// **Said out loud**, once. A refusal that is silently absorbed looks
		// exactly like a fleet nobody is using, and the reason is the only thing
		// that tells the two apart - which cost an afternoon of two-machine runs
		// reporting "4 delegated, 4 here" and nothing else (E308).
		d.noteRefused(n, r.Refused)

		return d.local(ctx, n, w, base, sources)
	}

	return resultOf(r), nil
}

// noteRefused says, once, why a worker would not take a step.
//
// Once, for the same reason `noteLost` is: five hundred delegable steps refused
// for one reason would print five hundred identical lines. The step is named
// because the reason is often about *that* step - a secret, a construct - rather
// than about the fleet.
func (d *Delegating) noteRefused(n *ir.Node, why string) {
	if d.Note == nil {
		return
	}

	d.refused.Do(func() {
		d.Note(fmt.Sprintf("fleet: a worker would not take %s (%s)"+
			" - running here", n.Meta.Source, why))
	})
}

// noteLost says, once, that the build has stopped being a fleet build.
//
// The step is named because it is the actionable part: it is where the fleet was
// last expected to be there, and everything after it is running on one machine.
func (d *Delegating) noteLost(n *ir.Node, cause error) {
	if d.Note == nil {
		return
	}

	d.lost.Do(func() {
		d.Note(fmt.Sprintf("fleet: no worker took %s (%v)"+
			" - building the rest here", n.Meta.Source, cause))
	})
}

func (d *Delegating) local(
	ctx context.Context, n *ir.Node, w core.Worker, base []ir.NodeID, sources [][]ir.NodeID,
) (core.Result, error) {
	d.acct.local()

	// Whatever a worker made that this step needs. A delegated step leaves its
	// layer on the worker and hands back a digest, so a step that has to run
	// *here* - a host op, a construct no worker implements, an artifact being
	// written out - would otherwise be asked to build on a base that is on
	// somebody else's disk (E274).
	err := d.bringBack(ctx, base, sources)
	if err != nil {
		return core.Result{}, err
	}

	if d.Local == nil {
		return core.Result{}, fmt.Errorf("%w: and there is no local executor to"+
			" fall back to", ErrNotDelegable)
	}

	d.here.Add(1)

	res, err := d.Local.Run(ctx, n, w, base, sources)

	d.here.Add(-1)

	// Recorded here as well as for a delegated step: a build that runs half its
	// steps locally would otherwise know the size of only half its layers, and
	// an assignment with one unknown input states no size at all.
	d.known.grew(res.Layer, res.Bytes)

	return res, err //nolint:wrapcheck // the executor's own error
}

// resultOf turns a worker's claim into a result this engine can use.
//
// A translation and not an endorsement. The observation arrives from a machine
// this one did not write (A5), and it reaches Κ₂ only through the driver's
// existing rules: an observation naming nothing is refused rather than trusted,
// because it agrees with every base. Nothing here relaxes that - `Observed` is
// set from what the observation *contains*, exactly as it is for a local step,
// so a worker cannot assert its own credibility by setting a flag.
func resultOf(r Reply) core.Result {
	obs := core.Observation{
		Reads:      r.Observation.Reads,
		Negative:   r.Observation.Negative,
		Listings:   r.Observation.Listings,
		Incomplete: r.Observation.Incomplete,
	}

	if obs.Reads == nil {
		obs.Reads = map[string]ir.NodeID{}
	}

	if obs.Listings == nil {
		obs.Listings = map[string]ir.NodeID{}
	}

	return core.Result{
		Layer:   r.Layer,
		Content: r.Content,
		Exit:    r.Exit,
		Bytes:   r.Bytes,
		// Captured, because a worker runs a step confined - that is what makes
		// it a worker rather than a shell. A delegate that could not confine
		// would have refused (I10).
		Captured:    true,
		Observation: obs,
		Observed: len(obs.Reads) > 0 || len(obs.Listings) > 0 ||
			len(obs.Negative) > 0,
	}
}

// bringBack fetches inputs this machine lacks from whoever holds them.
//
// Silent when there is nothing to do, which is nearly always: a build with no
// fleet, a fleet sharing one store, or a step whose inputs this machine made
// itself. The work only happens where the alternative is a step that cannot
// start.
func (d *Delegating) bringBack(
	ctx context.Context, base []ir.NodeID, sources [][]ir.NodeID,
) error {
	if d.Store == nil {
		return nil
	}

	a := Assignment{Base: base, Sources: sources}

	holders := d.held.of(a)
	if len(holders) == 0 {
		// Nothing here came from a worker, so there is nothing to fetch and no
		// reason to open a connection to find that out.
		return nil
	}

	from := make([]Source, 0, len(holders))

	// Over the connection the worker opened, when there is one. It needs
	// nothing to be reachable, which is the normal case rather than the
	// exception (E279) - so it is tried before dialling rather than after.
	back, _ := d.Fleet.(interface {
		SourceFor(string) (Source, bool)
	})

	for _, at := range holders {
		if back != nil {
			if s, ok := back.SourceFor(at); ok {
				from = append(from, s)

				continue
			}
		}

		if d.Peers == nil {
			continue
		}

		s, err := d.Peers(at)
		if err != nil || s == nil {
			// A holder that will not dial is skipped, as it is on a worker: the
			// address is a claim another machine made about itself (I5).
			continue
		}

		from = append(from, s)
	}

	_, err := Provision(ctx, d.Store, a, from...)
	if err == nil {
		return nil
	}

	// Which layer, and said in the one way the scheduler can act on.
	//
	// **Unobtainable is not nonexistent** (E278): the step that produced this
	// layer is still in the graph, and the scheduler is the only party that
	// knows which step that is. A plain error here failed the build; this asks
	// for the layer to be made again, here.
	for _, id := range append(append([]ir.NodeID{}, base...), flatten(sources)...) {
		if at := d.held.where(id); at != "" && !d.Store.Has(id) {
			return core.MissingInput{Layer: id, Where: at}
		}
	}

	return fmt.Errorf("bring a delegated result back to this machine: %w", err)
}

// MaxPredicted is the most paths a read-set hint carries.
//
// A fragment costs its manifest - about a hundred bytes an entry - so a
// prediction naming most of a base asks for nearly the whole thing *and* pays
// for the proof. Past some size the honest answer is "fetch the layer", and
// sending no hint is how this protocol says that.
//
// A judgement, and one line to change. What matters is that there is a cap: a
// read set is a step's own business, and a step that reads a hundred thousand
// files is not hypothetical (E287).
const MaxPredicted = 4096

// predicted is what this step read last time, if anybody knows and it is worth
// saying.
func (d *Delegating) predicted(n *ir.Node) []string {
	if d.Predict == nil {
		return nil
	}

	got := d.Predict(n)
	if len(got) == 0 || len(got) > MaxPredicted {
		// Nothing, rather than an empty list dressed up as knowledge: a worker
		// told "read nothing" would fetch a fragment of nothing and fault on
		// every file. Absence means "I do not know", and the whole layer is what
		// not knowing costs.
		return nil
	}

	return got
}

// flatten is every id in a stack of stacks, once each in order.
func flatten(stacks [][]ir.NodeID) []ir.NodeID {
	var out []ir.NodeID
	for _, s := range stacks {
		out = append(out, s...)
	}

	return out
}

// enumerable is a transport that can say who joined it.
//
// An interface rather than a method on Transport, because not every transport
// has an answer: InProcess has a fixed set decided by its caller, and asking it
// who is out there is a question about a mesh it does not have.
type enumerable interface {
	Inventory() []core.Worker
}

// Remote is the workers the scheduler may place steps on, this machine
// excluded.
//
// Placement (§4.7.1) chooses among the workers it was given, so a fleet that is
// reachable but unlisted never receives a step and the build quietly stays
// local. The local worker is left out because the caller already holds it;
// including it would put one machine in the list twice, which §4.7.3 sees as two
// candidates sharing an identity.
//
// Nobody, when the transport cannot enumerate: inventing a worker would have the
// scheduler place a step on something that may not exist.
func (d *Delegating) Remote() []core.Worker {
	e, ok := d.Fleet.(enumerable)
	if !ok {
		return nil
	}

	return e.Inventory()
}

// holders is who holds which layer, as the workers have said.
//
// The driver is the only party that knows both halves - which machine produced a
// layer, and which step needs it next - so it is the only party that can turn a
// fleet from a star into a mesh. Without this every worker fetches every input
// from the driver, whose uplink then *is* the fleet's bandwidth: adding machines
// adds queueing rather than throughput, which is the shape a distributed build
// takes when it comes out slower than one machine (E260).
type holders struct {
	mu sync.Mutex
	at map[ir.NodeID]string
}

// also notes that a worker now holds the inputs it was sent.
//
// Separate from `record` because the two know different things: `record` is told
// where a layer was made, this is inferred from a step having run. A worker with
// only part of a base is still worth naming - the part is what the next step
// with the same prediction wants, and a whole-layer request it cannot answer
// falls through to the driver.
func (h *holders) also(ids []ir.NodeID, at string) {
	if at == "" || len(ids) == 0 {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.at == nil {
		h.at = map[ir.NodeID]string{}
	}

	for _, id := range ids {
		if _, seen := h.at[id]; !seen {
			// **First holder wins.** The alternative overwrites the machine that
			// produced a layer with whichever machine most recently read it,
			// which is a worse hint: the producer certainly has all of it.
			h.at[id] = at
		}
	}
}

// record notes where a produced layer can now be fetched.
//
// One address per layer, the most recent. A layer may be held in several places
// once it has been fetched around, but the driver only reliably knows about the
// machine that made it - and a list built from guesses would send workers dialling
// peers that garbage-collected it.
func (h *holders) record(r Reply) {
	// A worker that gave no address has nothing to serve: an in-process fleet
	// has no address at all, and one sharing a store has nothing to move. An
	// empty string recorded here is a dial to nowhere on every later step.
	if r.HeldAt == "" || r.Layer == (ir.NodeID{}) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.at == nil {
		h.at = map[ir.NodeID]string{}
	}

	h.at[r.Layer] = r.HeldAt
}

// where is the one machine known to hold this layer, or nothing.
func (h *holders) where(id ir.NodeID) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.at[id]
}

// of is where this assignment's inputs are held, without repeats.
//
// Deduplicated because a base layer is commonly also a source, and a worker
// handed the same address twice dials it twice. Ordered by first appearance in
// the assignment, which makes the hint a function of the assignment rather than
// of a map's iteration order - a fleet's advice should not vary run to run.
func (h *holders) of(a Assignment) []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.at) == 0 {
		return nil
	}

	seen := map[string]bool{}

	var out []string

	add := func(id ir.NodeID) {
		at, ok := h.at[id]
		if !ok || seen[at] {
			return
		}

		seen[at] = true
		out = append(out, at)
	}

	for _, id := range a.Base {
		add(id)
	}

	for _, stack := range a.Sources {
		for _, id := range stack {
			add(id)
		}
	}

	return out
}

// bytesOf is how much this assignment would cost to ship, if that is knowable.
//
// **All of its inputs or none of them.** A sum over the ones this driver
// happens to know is a smaller number that looks like a whole one, and placement
// reads it as the price of the step - so a base priced at a tenth of its size
// is worse than one priced at the constant. Under-pricing is how a fleet talks
// itself into shipping something it should not have.
func (d *Delegating) bytesOf(a Assignment) int64 {
	var out int64

	for _, id := range standsOn(a) {
		n := d.sizeOf(id)
		if n <= 0 {
			return 0
		}

		out += n
	}

	return out
}

// sizeOf is what this driver knows about one layer's size.
//
// What it produced itself first - that is a measurement it took - and then
// whatever it was told about the layers it did not.
func (d *Delegating) sizeOf(id ir.NodeID) int64 {
	if n, ok := d.known.of(id); ok {
		return n
	}

	if d.Sizes == nil {
		return 0
	}

	return d.Sizes(id)
}

// measured is how big the layers this build produced turned out to be.
//
// Its own lock, as `holders` has: the two are consulted on the same path and a
// shared one would serialise placement behind bookkeeping.
type measured struct {
	mu sync.Mutex
	at map[ir.NodeID]int64
}

func (m *measured) of(id ir.NodeID) (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.at[id]

	return n, ok
}

// grew records what a step produced. A zero is not recorded: it is what a
// result that never said carries, and filing it would answer "known to be
// empty" to a question nobody had answered.
func (m *measured) grew(id ir.NodeID, n int64) {
	if n <= 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.at == nil {
		m.at = map[ir.NodeID]int64{}
	}

	m.at[id] = n
}

// keepHere decides where a step runs, and it is one comparison.
//
// **Three thresholds became this.** Each of E318, E320 and E321 arrived after a
// measurement showed the previous one moving a cost rather than removing it -
// ship what is cheap, then do not queue behind a full fleet, then do not keep
// what this machine has no room for. They are the same question asked from
// different sides, and `cheaperHere` asks it once: which side finishes this step
// sooner, counting the transfer.
//
// Only when running here is **free of transfer**. If the driver would have to
// bring the base back from whichever worker made it, both choices move the bytes
// and keeping the step buys a busy driver and nothing else.
func (d *Delegating) keepHere(a Assignment) (string, bool) {
	if d.Fleet == nil || d.Local == nil || d.Store == nil {
		return "", false
	}

	slots := d.slots()
	if slots <= 0 {
		// No fleet at all. Not a cost comparison - the ordinary path reports
		// there is nobody to delegate to and runs it here (I11).
		return "", false
	}

	// **A price only when there is one**, in the same half-steps `cheaperHere`
	// compares in.
	//
	// `Slots` answers with the constant when a size is unstated or the fleet
	// unmeasured, which is right for *ordering* workers - a machine that must
	// fetch an unknown thing is not thereby the cheapest. It is wrong here: "we
	// do not know what shipping costs" must not become "it costs enough to keep
	// the step", or a driver that has measured nothing keeps everything and the
	// fleet is switched off by ignorance (E317's intent, E343's regression).
	//
	// Not halved either. The doubling is what lets the price mean half a step,
	// and dividing it away made every transfer under a step free - which is how
	// a chain of eight shipped all eight for no parallelism at all.
	ship := 0
	if moves := d.movesFor(a); moves > 0 && d.rate.Measured() && !d.fleetHolds(a) {
		ship = d.rate.Slots(moves)
	}

	// **What this machine would have to fetch is a cost, not a veto.**
	//
	// Keeping used to require already holding everything the step reads, so a
	// driver sat out every level-shaped build: from the second level those
	// layers live on whichever worker made them (E346). The argument for the
	// veto - both choices move the same bytes - holds only while the fleet has
	// room, and stops holding exactly when a driver would be most useful.
	bring := 0
	if !d.holdsAll(a) {
		if !d.canBringBack() {
			return "", false
		}

		bring = ship
		if bring == 0 {
			bring = transferCost
		}
	}

	if !cheaperHereFetching(d.here.Load(), int64(d.Room), d.flight.Load(),
		slots, ship, bring) {
		return "", false
	}

	return fmt.Sprintf("keeping a step here: this machine finishes it in %d"+
		" wave(s) and the fleet would need %d plus %d half-step(s) of transfer",
		waves(d.here.Load(), int64(d.Room)), waves(d.flight.Load(), slots),
		ship), true
}

// noteKept says, once, that this build is declining to use its fleet.
//
// Once for the same reason `noteLost` is: a build with five hundred such steps
// would print five hundred identical lines. And it is worth saying at all
// because a fleet that is up, reachable and never asked looks exactly like a
// fleet that is not there - which is the failure class this project keeps
// meeting from the other side.
func (d *Delegating) noteKept(n *ir.Node, why string) {
	// **No accounting here.** `local` counts the step, and counting it twice
	// would make a build that declined to delegate look like one that ran
	// twice as many steps - an account that quietly does not add up is the one
	// thing this project has fixed most often (E270).
	if d.Note == nil {
		return
	}

	d.kept.Do(func() { d.Note(why + " (" + n.Meta.Source + ")") })
}

// PilotWait bounds how long a step waits for the fleet's first measurement.
//
// A build must not stall on a fleet that never answers, and the step that is
// waiting can always be run here - so the deadline is generous rather than
// tight: passing it means delegating on no evidence, which is what every build
// did before E319.
const PilotWait = 30 * time.Second

// learn holds back an expensive step until one has been out and come back.
//
// **The cold start, which is every build's first wave.** `notWorthShipping`
// needs a measured fleet, and a build that launches six steps at once decides
// all six before any reply exists. Over a real LAN that was 31.2 MiB moved for
// 183ms of compute with every step delegated, and the rule written to prevent
// precisely that never ran (E319).
//
// So the first such step goes out alone - it is the one that finds out what a
// transfer costs - and the others wait for what it learns. Three things keep
// this from being a bottleneck:
//
//   - only steps whose inputs are worth pricing wait at all. A step with
//     nothing to ship has nothing to gain, and a build of them is unaffected;
//   - the wait ends at the first observation, not the first *reply*: a fleet of
//     twenty workers is held for one round trip, once, ever;
//   - it is bounded. A fleet that never answers delegates on no evidence, which
//     is what every build did before this.
//
// The pilot is not privileged and is not retried: if it is refused or the fleet
// is gone, the ordinary paths handle it and the gate opens anyway, because a
// build must never wait on a step that is not coming back.
func (d *Delegating) learn(ctx context.Context, a Assignment) {
	if d.Local == nil || d.Store == nil || a.Hints.Bytes <= 0 || d.rate.Measured() {
		return
	}

	// The first caller here goes out and teaches; everybody else waits.
	if d.flying.CompareAndSwap(false, true) {
		return
	}

	// **And no more wait than could act on the answer.** The gate exists so a
	// step can be *kept*; a step this machine has no room for is going to the
	// fleet whatever the price, and holding it is delay with no possible
	// benefit. Measured: eight steps, seven of them waiting ~600ms for an answer
	// that could only change what happened to two (E321).
	if room := int64(max(d.Room, 1)); d.waiting.Add(1) > room {
		d.waiting.Add(-1)

		return
	}

	defer d.waiting.Add(-1)

	t := time.NewTimer(PilotWait)
	defer t.Stop()

	select {
	case <-d.taught():
	case <-t.C:
	case <-ctx.Done():
	}
}

// taught is closed once this driver has measured its fleet.
func (d *Delegating) taught() chan struct{} {
	d.gate.Do(func() { d.learnt = make(chan struct{}) })

	return d.learnt
}

// learned opens the gate, whatever the outcome of the step that was holding it.
//
// Called on **every** exit from a delegated step, including refusals and a fleet
// that has gone: a gate that only opened on success would hold a build for the
// full deadline every time its first step was refused, which is a common and
// entirely healthy thing for a step to be (I10).
func (d *Delegating) learned() {
	// **`sync.Once`, not a check and then a close.** The first version looked to
	// see whether the gate was already shut and then shut it, which two
	// goroutines can both pass - and a build of twelve steps on two workers
	// panicked with `close of closed channel` within seconds of running (E323).
	//
	// *Failure class: TOCTOU on a check-then-act.* The check reads as a guard
	// and is not one.
	d.opened.Do(func() { close(d.taught()) })
}

// fleetFull keeps a step here rather than queueing it behind a busy fleet.
//
// **The whole-build view the per-step rules cannot have.** E317 prices a fetch
// and E318 declines one, but both judge a step alone: six cheap steps each
// answer "yes, worth shipping", go to a worker with room for one, and five of
// them queue while the machine that asked sits idle holding every input. A queue
// is invisible to any comparison made one step at a time.
//
// The driver is the only party that knows both numbers - how many steps are with
// the fleet, and how much room the fleet admitted to - so it is the only party
// that can notice.
//
// Same shape as its neighbours: it fires only when running here costs no
// transfer, so it can never trade a queue for a fetch. And it is deliberately
// *not* a scheduler - it does not model this machine's own capacity, because a
// step kept here still runs through the ordinary executor, which has whatever
// limits it has.
func (d *Delegating) fleetFull(_ Assignment) (string, bool) {
	if d.Fleet == nil || d.Local == nil || d.Store == nil {
		return "", false
	}

	// Room for one per worker until a worker says otherwise. The cautious
	// direction: a fleet assumed larger than it is takes work it will queue,
	// which is the failure this exists to fix (E272 makes the same argument
	// about capacity from the other end).
	slots := int64(max(d.Fleet.Workers(), 0)) * max(d.room.Load(), 1)
	if slots <= 0 || d.flight.Load() < slots {
		return "", false
	}

	// **And this machine has somewhere to put it.** When both are full the step
	// goes to the fleet: a worker that queues starts the moment it can, while a
	// driver that queues delays everything else it is doing, including every
	// decision like this one (E321).
	if d.Room > 0 && d.here.Load() >= int64(d.Room) {
		return "", false
	}

	return fmt.Sprintf("keeping a step here: all %d slot(s) this fleet admits"+
		" to are busy and this machine already has its inputs", slots), true
}

// roomy records the largest capacity any worker has admitted to.
//
// The largest rather than the latest: capacity is a property of a machine and a
// fleet of one large and several small ones would otherwise be sized by whoever
// answered last. Wrong in the direction of delegating too much, which is the
// direction that queues.
func (d *Delegating) roomy(n int) {
	for {
		was := d.room.Load()
		if int64(n) <= was || d.room.CompareAndSwap(was, int64(n)) {
			return
		}
	}
}

// holdsAll is whether this machine could run the step without fetching.
//
// The condition both rules that keep a step here depend on, written once. Each
// of them can only ever be right when running here is *free of transfer*: if the
// driver would have to bring the base back from whichever worker made it, both
// choices move the bytes and keeping the step buys a busy driver and nothing
// else.
//
// A missing executor or store is the same answer as a missing input - there is
// nowhere to keep the step - so they are checked in the same place rather than
// duplicated at each call.
func (d *Delegating) holdsAll(a Assignment) bool {
	if d.Local == nil || d.Store == nil {
		return false
	}

	for _, id := range standsOn(a) {
		if !d.Store.Has(id) {
			return false
		}
	}

	return true
}

// slots is how many steps this fleet can run at once, as far as anyone knows.
//
// **A worker that has not spoken yet is assumed to be a machine like this one.**
// Capacity arrives on a reply, so a build that launches its first wave at once
// sizes the fleet before anybody has answered - and sizing it at one slot per
// worker kept seven steps of eight and finished no faster than a single machine,
// where the same build split four and four finished in two thirds of the time
// (E322).
//
// Not arbitrary: the machine doing the asking is the only other machine this
// process has ever seen, and a fleet is normally made of peers. The first reply
// corrects it in either direction, and `roomy` keeps the largest any worker has
// admitted to.
func (d *Delegating) slots() int64 {
	return int64(max(d.Fleet.Workers(), 0)) *
		max(d.room.Load(), int64(max(d.Room, 1)))
}

// movesFor is how many bytes delegating this step would actually move.
//
// **Not the size of its inputs.** With a prediction a worker fetches part of a
// base, and the difference is two orders of magnitude: at four workers a 16 MB
// base moved 1.1 MiB in total while every decision was made against 16 MB a
// step, so the driver kept work it should have shipped (E326).
//
// Measured rather than modelled - `Typical` is what delegated steps have
// actually moved - and only where a prediction exists to make it plausible. A
// fleet that has fetched nothing reports zero, which means "no answer" and falls
// back to the stated size rather than to free.
func (d *Delegating) movesFor(a Assignment) int64 {
	if len(a.Hints.ReadsPredicted) == 0 {
		return a.Hints.Bytes
	}

	if n := d.rate.Typical(); n > 0 {
		return n
	}

	return a.Hints.Bytes
}

// Primer is a transport that can tell every worker what to be ready for.
//
// Optional, and asked for by assertion rather than added to `Transport`: a
// transport that cannot broadcast is not a transport that cannot work, and every
// double in every test would otherwise have to grow a method it does not use.
type Primer interface {
	PrimeAll(ctx context.Context, a Assignment)
}

// primeAll tells every worker what this build stands on, once.
//
// **Three machines idle while one fetches** (E341). The cost of a fleet's first
// second is one transfer per worker, paid when that worker's first step arrives;
// priming only the machine being assigned would move that cost rather than
// remove it, and the fleet would warm up one machine at a time.
//
// Once per build rather than once per step: the base is the same for every step
// that stands on it, and a driver that primed repeatedly would spend its own
// uplink telling machines what they already have.
//
// The assignment is sent with its **step removed**, which is what makes it a
// prime: the same base, the same prediction, nothing to run (E342).
func (d *Delegating) primeAll(ctx context.Context, a Assignment) {
	p, ok := d.Fleet.(Primer)
	if !ok {
		return
	}

	d.primed.Do(func() {
		ready := a
		ready.Op = Op{}
		ready.Platform = ""

		p.PrimeAll(ctx, ready)
	})
}

// rooted is a store that lives somewhere on disk.
type rooted interface{ RootDir() string }

// rateAt is where what this fleet costs is kept, beside the layers it moves.
//
// Beside the store rather than in a config directory: the rate is a property of
// *this* machine talking to *this* fleet's layers, and a machine with two stores
// has two answers.
func rateAt(store Keeper) (string, bool) {
	r, ok := store.(rooted)
	if !ok {
		return "", false
	}

	return filepath.Join(r.RootDir(), "fleet-rate.json"), true
}

// Remember loads what an earlier build measured about this fleet.
//
// **Every real build is round one** without it (E350): an unmeasured fleet
// prices a transfer at zero, delegates everything and keeps nothing, which
// measured 1.447s against 1.084s for the same work once it knew.
func (d *Delegating) Remember(store Keeper) {
	d.Store = store

	if at, ok := rateAt(store); ok {
		_ = d.rate.Load(at)
	}
}

// Keep records what this build measured, for the next one.
func (d *Delegating) Keep() error {
	at, ok := rateAt(d.Store)
	if !ok {
		return nil
	}

	return d.rate.Save(at)
}

// MeasuredForTest reports whether this driver knows what its fleet costs.
func (d *Delegating) MeasuredForTest() bool { return d.rate.Measured() }

// flightForTest pretends this many steps are with the fleet.
//
// The condition that matters most - a fleet deeper than this machine - is the
// one hardest to arrange honestly: it needs several steps in flight at once and
// a transport that holds them there. Named as a seam rather than reached by
// choreography, because a test that produces it by timing is a test that
// sometimes does not.
func (d *Delegating) flightForTest(n int64) { d.flight.Store(n) }

// fleetHolds is whether some machine other than this one already has this
// step's inputs.
//
// **A transfer that has happened costs nothing to repeat.** `keepHere` priced
// every delegation as though the base had to cross, and once a worker has
// fetched it, sending that worker another step on the same base moves no bytes
// at all - so an expensive base became a reason to keep every step of a fan-out
// on one machine, having paid for it exactly once (E344).
//
// Read from the holder table, which only ever records **other** machines: an
// entry arrives from a reply's `heldAt` or from a worker having run a step, and
// this driver does neither. A comparison against `Self` here was unreachable and
// is left out rather than kept as reassurance - mutation could delete it with no
// test noticing, and a branch no input reaches is a claim about the code that is
// not true (E325's argument, met again).
//
// Wrong in the safe direction if it is stale: a worker that has evicted the
// layer fetches it again, which is slower and correct (I6).
func (d *Delegating) fleetHolds(a Assignment) bool {
	for _, at := range d.held.of(a) {
		if at != "" {
			return true
		}
	}

	return false
}

// canBringBack is whether this machine has any way to obtain a layer it lacks.
//
// A driver with no peer dialler cannot fetch a worker's layer, so keeping a step
// whose inputs are elsewhere would be keeping one it cannot run (E274).
func (d *Delegating) canBringBack() bool { return d.Peers != nil }
