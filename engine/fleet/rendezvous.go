package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// BindDriver binds an endpoint whose identity **is** the session's key.
//
// C.1 derives 𝑘 from the session and a secret, and this is what that is for: a
// worker who knows the secret derives the same key, and therefore knows the
// driver's endpoint id without being told it. Nobody who does not know the
// secret can derive it, join the mesh, or serve results.
//
// The session is what makes it specific and the secret is what makes it
// unguessable, which is why they are separate arguments (E233).
//
// **The session must be unique per fleet, not per run.** Prior art on the same
// mechanism records four CI matrix jobs sharing one session identifier: the
// driver identity is derived from it, so four fleets advertised the same driver
// and the mesh connected them to each other. `workers joined: 3/2` on one and
// `0/2` on another. A matrix axis belongs in the session term.
func BindDriver(ctx context.Context, s Session, secret []byte, opts ...iroh.Option) (*iroh.Endpoint, error) {
	k, err := DeriveDriverKey(s, secret)
	if err != nil {
		return nil, err
	}

	sk, err := key.SecretKeyFromEd25519(k)
	if err != nil {
		return nil, fmt.Errorf("the derived key is not an endpoint key: %w", err)
	}

	// Reachable from another machine, not only from this one: see [Reachable].
	// Before the caller's own options, so a test binding to a fixed address
	// still gets one.
	found := Discovery(sk)

	e, err := iroh.Bind(ctx, append(append([]iroh.Option{
		iroh.WithSecretKey(sk), iroh.WithALPNs(ALPNControl),
	}, found.Options()...), opts...)...)
	if err != nil {
		return nil, fmt.Errorf("bind the driver: %w", err)
	}

	found.Announce(ctx, e)

	return e, nil
}

// DriverID is the endpoint identifier a worker should look for.
//
// Derived rather than exchanged, which is the point: there is nothing to
// configure and nothing to leak, because knowing the secret *is* knowing where
// to go.
func DriverID(s Session, secret []byte) (key.EndpointID, error) {
	k, err := DeriveDriverKey(s, secret)
	if err != nil {
		return key.EndpointID{}, err
	}

	sk, err := key.SecretKeyFromEd25519(k)
	if err != nil {
		return key.EndpointID{}, fmt.Errorf("the derived key is not an endpoint key: %w", err)
	}

	return sk.Public().EndpointID(), nil
}

// defaultReach is how long a worker gets to answer when nobody said.
//
// A live worker answers a control message in milliseconds - it is one stream on
// a connection that is already open. Ten seconds is therefore not a budget for
// slowness but a bound on a machine that will never answer at all, and the only
// cost of being wrong is that a briefly wedged worker is dropped and rejoins.
const defaultReach = 10 * time.Second

// Rendezvous is the driver's side of a fleet workers dial into.
//
// **Workers connect to the driver, not the other way round**, and that is the
// arrangement that works in the world: a worker is behind whatever NAT its
// operator has, while a driver is the one machine somebody can reach - or, in
// CI, the one that starts first and publishes an address the others are given.
//
// QUIC is bidirectional, so a connection a worker opened carries assignments in
// the other direction. Nothing has to be reachable except the driver.
type Rendezvous struct {
	// Allow is who may join (C.1). Deriving the driver's identity is necessary
	// and **not sufficient**: a secret can leak, and an allowlist can be
	// narrowed without rotating one.
	//
	// Checked at accept rather than before dialling, which is the better place
	// for it - the identity is the one QUIC verified during the handshake
	// rather than one this engine was told, so a peer cannot claim to be
	// somebody on the list.
	Allow *Allowlist

	// Reach bounds how long one worker gets to answer before it is treated as
	// gone. Zero means defaultReach.
	//
	// Necessary because the transport's own patience is the wrong number here:
	// QUIC waits out an idle timeout of tens of seconds before admitting a peer
	// has vanished, and a driver that inherited that would pay it **per step**
	// for as long as the corpse stayed in the fleet (E256). A worker that is
	// alive answers in milliseconds; one that needs half a minute is not a
	// worker this build should be waiting for.
	Reach time.Duration

	// rate is what this fleet has been measured to cost, and is what prices a
	// fetch against a step. Its own lock, so placement's arithmetic does not
	// queue behind the connection table.
	rate Rate

	mu       sync.Mutex
	inflight map[string]int
	conns    []joined
	next     int
	// seq names the next worker to join. **A counter, not a position**: names
	// are what the scheduler places against and the cache attributes to, so one
	// that shifted when a worker left would hand a departed machine's name to
	// whoever stood there next (E256).
	seq int
}

// joined is one worker and the name it keeps for as long as it is here.
type joined struct {
	conn *iroh.Conn
	id   string
	// at is where this worker serves the layers it has produced, as it last
	// announced itself. Empty until it has answered something.
	at string
	// capacity is how many steps it says it can run at once. Zero until it has
	// answered, and treated as one - the cautious direction, since a machine
	// assumed infinite would be given the whole build (E272).
	capacity int
	// from is where this worker's connection appeared to come from. The driver
	// is the only party that observes it, and it is what corrects an address a
	// worker bound to everything cannot know (E277).
	from net.Addr
	// platform is what it says it is, as `ir.Platform.String` writes it.
	// Empty until it has answered something, and an unknown platform is
	// ineligible for every step (E267) - so a fleet is unused until its workers
	// have spoken, rather than used wrongly.
	platform string
}

// Accept registers workers as they arrive, until the context ends.
func (r *Rendezvous) Accept(ctx context.Context, e *iroh.Endpoint, onError func(error)) error {
	if onError == nil {
		onError = func(error) {}
	}

	for {
		conn, err := e.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			return fmt.Errorf("accept a worker: %w", err)
		}

		if r.Allow != nil && !r.Allow.Allows(publicKeyOf(conn.RemoteID())) {
			onError(fmt.Errorf("%w: %v is not on the allowlist",
				ErrNoWorker, conn.RemoteID()))
			_ = conn.CloseWithError(0, "not on the allowlist")

			continue
		}

		id := r.add(conn)

		// Asked as soon as it arrives, not after it has run something.
		//
		// In a goroutine because a worker that never answers must not stop the
		// next one joining, and best-effort because a worker that says nothing
		// is a worker placement gives nothing - which is today's behaviour and
		// the safe one (E504).
		go r.askWhatItIs(ctx, id, conn, onError)
	}
}

func (r *Rendezvous) add(c *iroh.Conn) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.seq++

	w := joined{conn: c, id: "fleet-" + strconv.Itoa(r.seq-1)}
	if c != nil {
		w.from = c.RemoteAddr()
	}

	r.conns = append(r.conns, w)

	return w.id
}

// askWhatItIs asks a worker what it runs, and records the answer.
//
// A worker used to declare its platform by echoing the one an assignment asked
// for, so placement - which refuses a worker that has not declared one - could
// never give it a first step (E503). This asks on arrival instead.
//
// Best-effort in both directions: a worker that does not answer stays in the
// inventory with nothing declared, which is a worker placement gives nothing,
// and that is the safe outcome rather than a guess.
func (r *Rendezvous) askWhatItIs(
	ctx context.Context, id string, c *iroh.Conn, onError func(error),
) {
	if c == nil {
		return
	}

	s, err := c.OpenStreamSync(ctx)
	if err != nil {
		onError(fmt.Errorf("ask %s what it is: %w", id, err))

		return
	}

	defer func() { _ = s.Close() }()

	_, err = s.Write([]byte{kindHello})
	if err != nil {
		onError(fmt.Errorf("ask %s what it is: %w", id, err))

		return
	}

	body, err := ReadMessage(s)
	if err != nil {
		onError(fmt.Errorf("%s did not say what it is: %w", id, err))

		return
	}

	var said Reply

	err = json.Unmarshal(body, &said)
	if err != nil {
		onError(fmt.Errorf("%s said something that is not a reply: %w", id, err))

		return
	}

	r.note(id, said.HeldAt, said.Platform, said.Capacity)
}

// Workers is how many have joined.
func (r *Rendezvous) Workers() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.conns)
}

// Assign gives a step to a worker that has joined.
//
// Each is tried at most once, on a snapshot, for the reason E234 had to learn:
// a worker that keeps failing must not be handed the same step for ever, and
// the bound belongs in this call rather than in a comment.
//
// **Whoever could not be reached leaves** (C.5). Trying each worker once already
// keeps one dead machine from failing one step; left in the fleet it fails every
// later step as well, each paying a connection timeout before reaching a machine
// that is alive - so reassignment that never removes anything is a retry with a
// growing bill (E256).
func (r *Rendezvous) Assign(ctx context.Context, a Assignment) (Reply, error) {
	order := r.snapshot()
	if len(order) == 0 {
		return Reply{}, ErrNoWorker
	}

	var (
		last error
		gone []string
	)

	for _, w := range preferFetching(order, a.Hints.Holders, r.load(), r.priceOf(a)) {
		r.began(w.id)

		reply, err := r.ask(ctx, w.conn, a)

		r.ended(w.id)

		if err == nil {
			// What this cost, so the next placement is priced by what this
			// fleet does rather than by a constant (E317). Before the reply is
			// returned, because a driver that learns only from steps it
			// remembers to feed back learns from none of them.
			r.observed(reply)

			r.drop(ctx, gone)

			// Corrected **here**, at the one point every reply passes through,
			// rather than wherever an address is later used. The first version
			// corrected it in `note` - so the rendezvous knew the right address
			// and `Delegating`, which keeps its own holder table from the raw
			// reply, did not (E279). One correction, one place, and everything
			// downstream sees the same string.
			reply.HeldAt = correctHost(reply.HeldAt, w.from)

			r.note(w.id, reply.HeldAt, reply.Platform, reply.Capacity)

			return reply, nil
		}

		last = err
		gone = append(gone, w.id)
	}

	r.drop(ctx, gone)

	return Reply{}, fmt.Errorf("%w after %d attempt(s): %w", ErrWorkerGone, len(order), last)
}

// ask is one attempt at one worker, bounded by Reach.
//
// The bound is per worker rather than over the whole call: a fleet of four with
// one corpse in it should cost one Reach, not have the live machines share what
// is left of a single budget.
func (r *Rendezvous) ask(ctx context.Context, c *iroh.Conn, a Assignment) (Reply, error) {
	reach := r.Reach
	if reach <= 0 {
		reach = defaultReach
	}

	bounded, cancel := context.WithTimeout(ctx, reach)
	defer cancel()

	return askOver(bounded, c, a)
}

// drop removes workers that could not be reached.
//
// Nothing is dropped once the context is done: a cancelled build fails every
// assignment in flight, and reading that as "every machine has gone" would empty
// a fleet that is entirely healthy - the one failure that is certainly not the
// worker's.
//
// The context checked is the *build's*, not the per-worker one Reach makes: a
// deadline that expired because a machine did not answer is the whole evidence
// that it has gone, and treating that as "the caller went away" would leave
// every corpse in the fleet.
func (r *Rendezvous) drop(ctx context.Context, ids []string) {
	if len(ids) == 0 || ctx.Err() != nil {
		return
	}

	leaving := make(map[string]bool, len(ids))
	for _, id := range ids {
		leaving[id] = true
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	kept := r.conns[:0]

	for _, w := range r.conns {
		if leaving[w.id] {
			continue
		}

		kept = append(kept, w)
	}

	r.conns = kept

	// The rotation offset indexed into the old slice. Left alone it would skip
	// past however many left, which on a fleet of two means one machine takes
	// every step.
	r.next = 0
}

func (r *Rendezvous) snapshot() []joined {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]joined, 0, len(r.conns))

	for i := range r.conns {
		out = append(out, r.conns[(r.next+i)%len(r.conns)])
	}

	if len(r.conns) > 0 {
		r.next++
	}

	return out
}

// The two conversations a worker's connection carries.
//
// **Blobs travel the way assignments do**, which is the whole point: a worker
// dials out and a firewall lets that through, and nothing ever dials back. A
// driver that pulled a result by dialling the worker could not reach it, which
// is what a real two-machine run found on a machine that merely had a firewall
// on (E279).
const (
	kindAssign = byte('a')
	kindBlobs  = byte('b')
	// kindHello asks a worker what it is: the platform it runs, the room it
	// has, and where it serves layers.
	//
	// A worker used to declare its platform by *echoing* the one an assignment
	// asked for, so it could not be given a first step until it had run a first
	// step - and the echo proved nothing about what it could run anyway (E503,
	// E504).
	//
	// The driver asks and the worker answers, which is the direction the
	// protocol already has for an assignment and for a blob request. One more
	// kind, and no new direction.
	kindHello = byte('h')
)

// serveBlobsOver answers a blob request on a stream a worker's connection
// already carries.
func serveBlobsOver(s io.ReadWriter, held Held, onError func(error)) {
	ids, want, proof, err := readRequest(s)
	if err != nil {
		onError(fmt.Errorf("read a blob request: %w", err))

		return
	}

	for _, id := range ids {
		err := serveOneBlob(s, held, id, want, proof)
		if err != nil {
			onError(fmt.Errorf("serve %v: %w", id, err))

			return
		}
	}
}

// JoinOpt configures a worker's side of the fleet.
type JoinOpt func(*joinCfg)

type joinCfg struct {
	held Held
	// self is what this worker answers a hello with: its platform, its room and
	// where it serves layers. Empty means a worker that says nothing, which
	// placement gives nothing (E504).
	self Reply
}

// Runs is what this worker can run, and how much of it at once.
//
// Announced at join rather than learned from a reply. A worker that had run
// nothing had declared no platform, and placement refuses a worker that has not
// declared one - so a fresh worker could never be given a first step (E503).
func Runs(platform string, capacity int, heldAt string) JoinOpt {
	return func(c *joinCfg) {
		c.self = Reply{
			Version:  Version,
			Platform: platform,
			Capacity: capacity,
			HeldAt:   heldAt,
		}
	}
}

// Serving lets the driver fetch this worker's layers over the connection the
// worker opened.
//
// Without it a worker is reachable only by being dialled, which a firewall or a
// NAT prevents - and that is the normal case rather than the exception.
func Serving(held Held) JoinOpt {
	return func(c *joinCfg) { c.held = held }
}

// askOver sends one assignment over a connection a worker opened.
func askOver(ctx context.Context, conn *iroh.Conn, a Assignment) (Reply, error) {
	// A worker with no connection behind it. Reached through AddForTest, and
	// through nothing else - but dereferencing it panicked the driver, and a
	// comment three functions away asserted that it would not (E256).
	if conn == nil {
		return Reply{}, fmt.Errorf("%w: no connection to this worker", ErrWorkerGone)
	}

	s, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return Reply{}, fmt.Errorf("open a stream to a worker: %w", err)
	}

	defer func() { _ = s.Close() }()

	// The context covers opening the stream and **nothing after it**: the read
	// below takes no context, so a worker whose machine vanished after the
	// stream was opened would block until QUIC gave up on the connection - tens
	// of seconds, once per step, which is what the bound existed to prevent
	// (E256). A deadline on the stream is what actually applies it.
	if dl, ok := ctx.Deadline(); ok {
		_ = s.SetDeadline(dl)
	}

	_, err = s.Write([]byte{kindAssign})
	if err != nil {
		return Reply{}, fmt.Errorf("say what this stream is for: %w", err)
	}

	err = WriteMessage(s, Encode(a))
	if err != nil {
		return Reply{}, err
	}

	body, err := ReadMessage(s)
	if err != nil {
		return Reply{}, err
	}

	var reply Reply

	err = json.Unmarshal(body, &reply)
	if err != nil {
		return Reply{}, fmt.Errorf("%w: a reply that is not JSON: %w", ErrMalformed, err)
	}

	return reply, nil
}

// Join dials the driver and serves assignments over that one connection.
//
// The worker's whole life: derive where the driver is, connect, and answer.
// There is nothing to listen on, which is what makes a worker deployable
// anywhere a machine can reach the driver.
func Join(
	ctx context.Context, e *iroh.Endpoint, driver netaddr.EndpointAddr,
	run func(context.Context, Assignment) (Reply, error), onError func(error),
	opts ...JoinOpt,
) error {
	if onError == nil {
		onError = func(error) {}
	}

	var cfg joinCfg
	for _, o := range opts {
		o(&cfg)
	}

	conn, err := e.Connect(ctx, driver, ALPNControl)
	if err != nil {
		return fmt.Errorf("join the fleet: %w", err)
	}

	defer func() { _ = conn.CloseWithError(0, "") }()

	for {
		s, err := conn.AcceptStream(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			return fmt.Errorf("await an assignment: %w", err)
		}

		go answer(ctx, s, run, cfg.held, cfg.self, onError)
	}
}

func answer(
	ctx context.Context, s io.ReadWriteCloser,
	run func(context.Context, Assignment) (Reply, error), held Held, self Reply,
	onError func(error),
) {
	defer func() { _ = s.Close() }()

	// What this stream is for. One byte, first, because a worker's connection
	// now carries two conversations: the work it is being given, and the layers
	// it is being asked for (E279).
	var kind [1]byte

	_, err := io.ReadFull(s, kind[:])
	if err != nil {
		onError(fmt.Errorf("read what a stream is for: %w", err))

		return
	}

	if kind[0] == kindBlobs {
		serveBlobsOver(s, held, onError)

		return
	}

	// What this worker is, asked before it has run anything.
	if kind[0] == kindHello {
		replyErr := replyWith(s, self)
		if replyErr != nil {
			onError(fmt.Errorf("say what this worker is: %w", replyErr))
		}

		return
	}

	body, err := ReadMessage(s)
	if err != nil {
		onError(fmt.Errorf("read an assignment: %w", err))

		return
	}

	a, err := Decode(body)
	if err != nil {
		_ = replyWith(s, Reply{Version: Version, Refused: err.Error()})

		return
	}

	r, err := run(ctx, a)
	if err != nil {
		_ = replyWith(s, Reply{Version: Version, Refused: err.Error()})

		return
	}

	_ = replyWith(s, r)
}

var _ Transport = (*Rendezvous)(nil)

// WaitFor blocks until this many workers have joined, or the context ends.
//
// **The inventory is an input, not an observation.** §4.7.3 requires a
// byte-identical schedule from the same graph and the same worker inventory, and
// placement is decided in one pass before the build starts precisely so that it
// is a pure function rather than a race with whatever finished first. A fleet
// whose size changed mid-build would make the schedule depend on when a machine
// happened to connect.
//
// So the driver waits for its fleet to assemble and then schedules against what
// it has. That is the arrangement prior art on this mechanism reports as
// `workers joined : 2/2` - an *expected* count, waited for - and it is the only
// one that keeps determinism without pretending a fleet is static.
//
// Returns how many joined, which may be fewer than asked for when the context
// ends first. Fewer is a **different inventory** and therefore a different
// schedule, which is honest: the build still happens, with the machines that
// turned up, and nothing pretends otherwise.
func (r *Rendezvous) WaitFor(ctx context.Context, want int) int {
	const poll = 20 * time.Millisecond

	for r.Declared() < want {
		select {
		case <-ctx.Done():
			return r.Declared()

		case <-time.After(poll):
		}
	}

	return r.Declared()
}

// Declared is how many workers have said what they run.
//
// **Not the same as how many have connected.** Placement refuses a worker with
// no platform, so a connection that has not declared is a machine the scheduler
// steps over: counting it lets a driver report a fleet, place nothing on it, and
// build everything itself - a local build wearing a fleet's clothes.
//
// On one machine the connection and the declaration land in the same instant and
// the difference is invisible. Over a relay the declaration is a round trip
// later, which is where this was found (E505).
func (r *Rendezvous) Declared() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0

	for i := range r.conns {
		if r.conns[i].platform != "" {
			n++
		}
	}

	return n
}

// Inventory is the workers to schedule against, as core sees them.
//
// Named by position rather than by endpoint identity, deliberately: the schedule
// must not change because the same machines connected in a different order.
// **The identity decides who runs a step; the inventory decides how many steps
// run at once**, and only the second reaches the schedule.
func (r *Rendezvous) Inventory() []core.Worker {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]core.Worker, 0, len(r.conns))
	for _, w := range r.conns {
		out = append(out, core.Worker{ID: w.id, Platform: platformOf(w.platform)})
	}

	return out
}

// AddForTest registers a worker with no connection behind it.
//
// Exported for tests in this package's external test package, which is where the
// inventory's determinism is asserted - and that assertion needs a fleet of a
// given *size*, not a fleet of real endpoints. Binding three endpoints to check
// that three names come out would be testing iroh.
//
// A nil connection is never assigned to: `askOver` would fail on it and the
// caller would move on, which is the same path a dead worker takes.
func (r *Rendezvous) AddForTest() { r.add(nil) }

// PrimeAll tells every worker what this build stands on, and does not wait.
//
// **Not waited for**, which is the whole of why it helps: a prime the build
// waited on would pay the transfer before the first step instead of during it,
// which is the same second spent in a different order. What it buys is three
// machines fetching while the fourth runs (E341, E342).
//
// Errors are dropped rather than reported. A prime that fails costs the step
// that needed it a fetch, which is what every step did before this existed, and
// a fleet that failed a build over an optimisation would be worse than one
// without it (I5, I11).
func (r *Rendezvous) PrimeAll(ctx context.Context, a Assignment) {
	for _, w := range r.snapshot() {
		if w.conn == nil {
			continue
		}

		go func() { _, _ = r.ask(ctx, w.conn, a) }()
	}
}

// prefer puts the workers that hold this step's inputs at the front.
//
// **The single most consequential ordering in the fleet.** Placement was a strict
// rotation, so consecutive steps went to different workers - and a build is full
// of chains, where a step's base is the layer the step before it produced. A
// chain of n steps then ships its base n-1 times, and a base is the largest
// thing this engine moves (E265).
func prefer(order []joined, holders []string) []joined {
	return preferFree(order, holders, nil)
}

// transferCost is what fetching a base is worth, in step-slots, doubled.
//
// The number that reconciles the two things this ordering has to do. A **chain**
// must stay where its base is, or it ships that base every step. A **fan-out**
// must spread, and almost every build starts `FROM` one common image - so once a
// single worker holds that base, affinity that ignored load would put every step
// of an eight-way parallel build on one machine while seven watched. That is
// worse than no affinity at all.
//
// Loads are doubled so this can be an odd number: at 1, a holder wins a tie at
// equal load and loses as soon as it is one step busier. In words, **fetching a
// base costs about half a step-slot**. It is a model rather than a measurement,
// and the honest thing about it is that it is one line to change when there is a
// measurement to change it to.
const transferCost = 1

// preferFree orders workers by what each would cost, holding and load together.
//
// A preference and not an exclusion: everybody stays in the list, because a
// holder can be busy, gone, or refuse the step, and falling through to a machine
// that has to fetch is slower than the alternative and much better than a failed
// build (I11).
func preferFree(order []joined, holders []string, busy map[string]int) []joined {
	return preferFetching(order, holders, busy, transferCost)
}

// preferFetching is preferFree with the price of a fetch stated rather than
// assumed.
//
// The parameter exists so that the price can come from what the fleet has
// actually been measured to do (`Rate`), while everything about *how* the
// ordering uses it stays in one place - which is the same argument that keeps
// `Predict` calling this function rather than modelling placement itself.
func preferFetching(
	order []joined, holders []string, busy map[string]int, fetch int,
) []joined {
	// No fast path for "nobody holds anything", though the temptation is real:
	// with an empty rank every worker costs the same and the stable sort leaves
	// the rotation as it was. A branch that only ever produces what the code
	// below it produces is a branch no test can tell apart.
	rank := make(map[string]int, len(holders))
	for i, at := range holders {
		if _, seen := rank[at]; !seen && at != "" {
			rank[at] = i
		}
	}

	out := make([]joined, len(order))
	copy(out, order)

	// The largest machine in the fleet, which is what the others are measured
	// against. Normalising by it rather than by each machine's own capacity
	// keeps the units comparable: **the question is how full a machine is, not
	// how many steps it is running**, and a fleet of one large and several small
	// machines otherwise gives the large one an equal share and finishes when
	// the small ones do (E272).
	biggest := 1

	for _, w := range order {
		if roomOf(w) > biggest {
			biggest = roomOf(w)
		}
	}

	cost := func(w joined) int {
		// Reduces to `2 × busy` when every capacity matches, which is the common
		// case and the model every earlier measurement was taken under.
		c := busy[w.id] * 2 * biggest / roomOf(w)

		if _, held := rank[w.at]; !held || w.at == "" {
			c += fetch
		}

		return c
	}

	// Named order breaks ties among holders, so the first one listed is tried
	// first. The driver lists them in the order the assignment references its
	// inputs, which puts the base - the biggest thing that would otherwise move -
	// ahead of the sources.
	place := func(w joined) int {
		if i, held := rank[w.at]; held && w.at != "" {
			return i
		}

		return len(holders)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if a, b := cost(out[i]), cost(out[j]); a != b {
			return a < b
		}

		return place(out[i]) < place(out[j])
	})

	return out
}

// note records what a worker said about itself.
//
// Learned from the replies already flowing rather than asked for separately: a
// worker announces itself in every reply (E260, E267), and the driver is the
// only party that sees both that and who needs the layer next.
//
// Each field is kept only if it was given, so a reply that omits one does not
// erase what an earlier one said.
func (r *Rendezvous) note(id, at, platform string, capacity int) {
	if at == "" && platform == "" && capacity < 1 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range r.conns {
		if r.conns[i].id != id {
			continue
		}

		if at != "" {
			r.conns[i].at = at
		}

		if platform != "" {
			r.conns[i].platform = platform
		}

		if capacity > 0 {
			r.conns[i].capacity = capacity
		}

		return
	}
}

// NoteForTest records what a worker would have said about itself.
//
// Exported for the external test package, which asserts that the inventory
// carries an announcement - a property about what the driver *remembers*, not
// about how it came to hear it, and one that would otherwise need two endpoints
// and a build to observe.
func (r *Rendezvous) NoteForTest(id, at, platform string, capacity int) {
	r.note(id, at, platform, capacity)
}

// load is how many steps each worker is running, as far as this driver knows.
//
// Counted here rather than asked for, because the question is about *this*
// driver's outstanding work: a worker shared between two builds is busier than
// this says, and neither driver can see the other's. Being wrong in that
// direction costs a slower placement, never a wrong one.
func (r *Rendezvous) load() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make(map[string]int, len(r.inflight))
	maps.Copy(out, r.inflight)

	return out
}

func (r *Rendezvous) began(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.inflight == nil {
		r.inflight = map[string]int{}
	}

	r.inflight[id]++
}

func (r *Rendezvous) ended(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.inflight[id] > 0 {
		r.inflight[id]--
	}
}

// roomOf is how many steps a worker can run at once, as it last said.
//
// One when it has not said. The cautious direction: an unknown machine is
// offered a step and then looks full, rather than being treated as infinite and
// handed the whole build - and it stops being a guess the moment the worker
// answers anything, because capacity rides on every reply.
func roomOf(w joined) int {
	if w.capacity < 1 {
		return 1
	}

	return w.capacity
}

// SourceFor is a way to fetch from a worker over the connection it opened.
//
// The driver knows which connection announced which address, so a holder hint
// resolves to a live connection - and the fetch needs nothing to be reachable
// (E279). Reported as absent when no worker has announced that address, so the
// caller falls through to dialling, which is right for a peer this driver never
// spoke to.
func (r *Rendezvous) SourceFor(at string) (Source, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, w := range r.conns {
		if w.at == at && w.conn != nil {
			return &connSource{conn: w.conn, label: at, reach: r.Reach}, true
		}
	}

	return nil, false
}

// connSource fetches blobs over an already-open connection.
type connSource struct {
	conn  *iroh.Conn
	label string
	reach time.Duration
}

func (c *connSource) Name() string {
	if c.label == "" {
		return "a worker"
	}

	return c.label
}

func (c *connSource) Fetch(
	ctx context.Context, ids []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	reach := c.reach
	if reach <= 0 {
		reach = defaultReach
	}

	bounded, cancel := context.WithTimeout(ctx, reach)
	defer cancel()

	s, err := c.conn.OpenStreamSync(bounded)
	if err != nil {
		return nil, fmt.Errorf("open a blob stream to %s: %w", c.Name(), err)
	}

	defer func() { _ = s.Close() }()

	if dl, ok := bounded.Deadline(); ok {
		_ = s.SetDeadline(dl)
	}

	_, err = s.Write([]byte{kindBlobs})
	if err != nil {
		return nil, fmt.Errorf("ask %s for blobs: %w", c.Name(), err)
	}

	err = writeRequest(s, ids, nil, true)
	if err != nil {
		return nil, err
	}

	out := make(map[ir.NodeID]io.Reader, len(ids))

	for _, id := range ids {
		body, present, err := readBlob(s)
		if err != nil {
			// What arrived is still useful, and the caller asks somebody else
			// for the rest.
			return out, nil
		}

		if present {
			out[id] = bytes.NewReader(body)
		}
	}

	return out, nil
}

var _ Source = (*connSource)(nil)

// correctedForTest is what a reply's announced address becomes.
//
// Exported to the tests in this package because the correction happens inside
// `Assign`, between a network round trip and a reply - and the property worth
// asserting is about the string, not about the round trip.
func (r *Rendezvous) correctedForTest(reply Reply, id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, w := range r.conns {
		if w.id == id {
			return correctHost(reply.HeldAt, w.from)
		}
	}

	return reply.HeldAt
}

// priceOf is what fetching this assignment's inputs is worth, in the units
// `preferFetching` orders by.
func (r *Rendezvous) priceOf(a Assignment) int { return r.rate.Slots(a.Hints.Bytes) }

// observed feeds a reply's own account back into the price of the next one.
//
// **Nothing new is measured.** A worker already reports what it fetched, how
// long that took and how long its step ran; those three numbers were kept for
// the account and never used to decide anything (E317).
func (r *Rendezvous) observed(reply Reply) {
	r.rate.Observe(reply.FetchedBytes, reply.FetchMillis, reply.DurationMillis)
}
