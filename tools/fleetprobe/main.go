// Command fleetprobe measures a fleet over a real network.
//
// Every figure this project has for a fleet was taken over loopback, where a
// transfer costs almost nothing and the interesting trade - is moving a base
// worth more than the compute it saves? - cannot arise. This runs the same
// mechanisms between two machines.
//
// It is not a build. The step is a synthetic compute of a stated duration
// producing a layer of a stated size, because the question here is what the
// *fleet* costs and a real step would drown it in its own variance.
//
//	on the worker:  fleetprobe -role worker -driver <driver-host>:<port>
//	on the driver:  fleetprobe -role driver -workers 1 -steps 8 -size 64MiB
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

func main() {
	var (
		role      = flag.String("role", "driver", "driver or worker")
		at        = flag.String("driver", "", "worker: the driver's host:port")
		port      = flag.Int("port", 0, "driver: the port to bind, 0 for any")
		want      = flag.Int("workers", 1, "driver: how many workers to wait for")
		steps     = flag.Int("steps", 8, "driver: how many steps to fan out")
		size      = flag.Int("size", 64<<20, "bytes in each produced layer")
		compute   = flag.Duration("compute", 2*time.Second, "how long a step takes")
		room      = flag.Int("room", 1, "worker: steps at once")
		lazy      = flag.Bool("lazy", false, "fetch only the paths a step is predicted to read")
		miss      = flag.Int("mispredict", 0, "worker: one step in N reads outside its prediction")
		files     = flag.Int("files", 0, "driver: files in a seeded base, 0 for none")
		reads     = flag.Int("reads", 10, "driver: paths a step is predicted to read")
		wait      = flag.Duration("wait", 2*time.Minute, "driver: how long to wait for workers")
		runHere   = flag.Bool("local", false, "driver: run steps here too, so it can decline to delegate")
		localRoom = flag.Int("localroom", 2, "driver: steps at once here, 0 for no limit")
		chain     = flag.Bool("chain", false, "driver: each step stands on the last, as a critical path does")
		width     = flag.Int("width", 0, "driver: steps per level, so the graph is levels rather than one wave")
		repeat    = flag.Int("repeat", 1, "driver: build this many times, each on a fresh base, and report the median")
		remember  = flag.String("remember", "", "driver: keep the store here between runs, as a real build does")
	)

	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var err error

	switch *role {
	case "driver":
		err = drive(ctx, *port, *want, *steps, *size, *compute, *wait, *files,
			*reads, *runHere, *localRoom, *chain, *width, *repeat, *remember)
	case "worker":
		err = serve(ctx, *at, *size, *compute, *room, *lazy, *miss)
	default:
		err = fmt.Errorf("-role is %q, want driver or worker", *role)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "fleetprobe: %v\n", err)

		// Before the exit: `os.Exit` skips deferred calls, so leaving the
		// signal handler to `defer stop()` releases it in writing and not in
		// fact (gocritic exitAfterDefer).
		stop()
		os.Exit(1)
	}
}

// session is fixed, because both ends have to derive the same driver and this
// is a probe rather than a deployment.
var (
	session = fleet.Session{Session: "fleetprobe", RunID: "1", Attempt: 1, Repo: "probe"}
	secret  = []byte("fleetprobe")
)

func drive(
	ctx context.Context, port, want, steps, size int, compute, wait time.Duration,
	files, reads int, runHere bool, localRoom int, chain bool, width, repeat int,
	remember string,
) error {
	e, err := fleet.BindDriver(ctx, session, secret,
		iroh.WithBindAddr(netip.AddrPortFrom(netip.IPv4Unspecified(), uint16(port)))) //nolint:gosec // a port
	if err != nil {
		return fmt.Errorf("bind: %w", err)
	}

	defer func() { _ = e.Shutdown(context.WithoutCancel(ctx)) }()

	r := &fleet.Rendezvous{Reach: 2 * time.Minute}

	go func() {
		_ = r.Accept(ctx, e, func(err error) { fmt.Fprintln(os.Stderr, "driver:", err) })
	}()

	fmt.Printf("driver at %v, waiting %v for %d worker(s)\n", e.LocalAddr(), wait, want)

	deadline, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	if got := r.WaitFor(deadline, want); got < want {
		return fmt.Errorf("only %d of %d worker(s) joined", got, want)
	}

	// The driver's own store, and a blob endpoint to serve it from - so a worker
	// that cannot reach a peer has somewhere to fall back to (E277).
	root, err := os.MkdirTemp("", "fleetprobe-driver-")
	if err != nil {
		return fmt.Errorf("make a store: %w", err)
	}

	defer func() { _ = os.RemoveAll(root) }()

	err = os.MkdirAll(filepath.Join(root, "layers"), 0o750)
	if err != nil {
		return fmt.Errorf("make a store: %w", err)
	}

	store := &fleet.Layers{Root: root}

	// **A fresh store per run** is what makes this probe measure round one, and
	// a real `earth build` keeps its store between invocations - so a real
	// second build starts knowing what the first measured (E351). This is how
	// that regime is reachable here.
	if remember != "" {
		if err = os.MkdirAll(filepath.Join(remember, "layers"), 0o750); err != nil {
			return fmt.Errorf("make a store: %w", err)
		}

		store = &fleet.Layers{Root: remember}
	}

	blobs, err := iroh.Bind(ctx, iroh.WithALPNs(fleet.ALPNBlob))
	if err != nil {
		return fmt.Errorf("bind for blobs: %w", err)
	}

	defer func() { _ = blobs.Shutdown(context.WithoutCancel(ctx)) }()

	// What a peer asked this driver for, and whether it had it.
	served := &sayingHeld{Held: store}

	go func() {
		_ = fleet.ServeBlobs(ctx, blobs, served,
			func(err error) { fmt.Fprintln(os.Stderr, "serving:", err) })
	}()

	me := fleet.PeerAddr{ID: blobs.ID(), Host: blobs.LocalAddr().String()}

	seen := &sayingTransport{Transport: r}

	// What a step is predicted to read comes from the *driver*, not from the
	// node: `Delegating` asks `Predict` and the worker copies the answer onto
	// the node it hands its executor (E301). Setting the node's Meta here set it
	// on the driver's side of a wire that carries the hint, not the node - and
	// the assignment went out saying "predicted 0 path(s)" (E311).
	var paths []string

	predicted := func(*ir.Node) []string { return paths }

	var (
		first     core.Result
		base      ir.NodeID
		baseBytes int64
		here      *making
	)

	if runHere {
		here = &making{store: store, size: size, compute: compute}
		here.roomFor(localRoom)
	}

	d := &fleet.Delegating{
		// **The driver's own executor**, when there is one. Without it a driver
		// cannot decline to delegate however expensive the transfer, so E318 is
		// unmeasurable: every arrangement delegates and the probe cannot tell a
		// fleet that chose from one that had no choice.
		Local: localFor(here),
		// The same number the local executor was given, or a driver keeps work
		// it cannot run and the queue moves rather than goes (E321).
		Room:    localRoomOf(here, localRoom),
		Note:    func(s string) { fmt.Fprintln(os.Stderr, s) },
		Predict: predicted,
		Fleet:   seen,
		Self:    me.String(),
		Store:   store,
		// The seeded base is not a step's output, so nothing in this build
		// knows its size - and placement prices an unstated base as it prices
		// an unmeasured fleet (E317).
		Sizes: func(id ir.NodeID) int64 {
			if id == base {
				return baseBytes
			}

			return 0
		},
		// **Over the connection the worker opened**, not by dialling it.
		//
		// A worker is behind whatever NAT its operator has and nothing ever
		// dials back (E279), which is why the rendezvous keeps a back-channel.
		// Dialling worked in every earlier measurement because nothing ever
		// asked a *driver* to fetch from a worker - and the moment E347 let it
		// keep a step, five of sixteen failed and the build took fifteen
		// seconds.
		//
		// Falling back to a dial when the worker is not connected here: it may
		// be a peer this driver never accepted, and one that cannot be reached
		// either way is a refusal the ordinary paths handle (I6).
		Peers: func(at string) (fleet.Source, error) {
			if src, ok := r.SourceFor(at); ok {
				return src, nil
			}

			p, err := fleet.ParsePeerAddr(at)
			if err != nil {
				return nil, err
			}

			to, err := p.Endpoint()
			if err != nil {
				return nil, err
			}

			return &fleet.PeerSource{Endpoint: e, Peer: to, Label: at}, nil
		},
	}

	// What an earlier run measured about this fleet, as a real build loads it
	// (E351). With a fresh store this finds nothing, which is round one.
	d.Remember(store)

	// **Repeated, and cold each time.** The spread of one configuration measured
	// 1.467s, 1.527s and 1.674s - about fifteen per cent - so a single run
	// cannot tell a change of ten per cent from nothing, and three changes were
	// reported as having no effect when what they had was an effect smaller
	// than the noise (E349).
	//
	// Each round seeds a *different* base, because repeating on the same one
	// measures a warm fleet, which is a different regime rather than a second
	// sample of this one.
	var (
		rounds []time.Duration
		took   time.Duration
	)

	for round := range max(repeat, 1) {
		paths = nil
		before := d.Spend()

		if files > 0 {
			id, seedSize, seedErr := seedBase(store, files, round)
			if seedErr != nil {
				return seedErr
			}

			base, baseBytes = id, seedSize

			first = core.Result{Layer: id}

			for i := range min(reads, files) {
				paths = append(paths, fmt.Sprintf("usr/lib/lib%d.so", i))
			}

			fmt.Printf("seeded %v (%d files, steps read %d); the driver's store has"+
				" it: %v\n", id, files, len(paths), store.Has(id))
		} else {
			// One step to make the base the rest share.
			var stepErr error

			first, stepErr = d.Run(ctx, node(), core.Worker{ID: "w"}, nil, nil)
			if stepErr != nil {
				return fmt.Errorf("the first step: %w", stepErr)
			}

			base, baseBytes = first.Layer, first.Bytes
		}

		began := time.Now()

		switch {
		case width > 1:
			// **Levels**: `width` steps at once, then a barrier, then the next
			// round on what this one made. The shape a build actually has, and the
			// one that asks whether a fleet wins the parallel part by more than it
			// loses at each barrier (E345).
			on := first.Layer

			for range max(steps/width, 1) {
				var (
					wg   sync.WaitGroup
					made = make([]ir.NodeID, width)
				)

				for i := range width {
					// `wg.Go` is `Add(1)` and `defer Done()` in one place, so the
					// pair cannot drift apart (modernize waitgroupgo).
					wg.Go(func() {
						res, err := d.Run(ctx, node(), core.Worker{ID: "w"},
							[]ir.NodeID{on}, nil)
						if err != nil {
							fmt.Fprintln(os.Stderr, "step:", err)

							return
						}

						made[i] = res.Layer
					})
				}

				wg.Wait()

				if made[0] != (ir.NodeID{}) {
					on = made[0]
				}
			}

		case chain:
			// **One at a time, each on what the last produced.** No parallelism to
			// sell, which is the point: this is a build's critical path, and the
			// only thing a fleet can do with it is fail to make it worse (E343).
			on := first.Layer

			for range steps {
				res, err := d.Run(ctx, node(), core.Worker{ID: "w"},
					[]ir.NodeID{on}, nil)
				if err != nil {
					fmt.Fprintln(os.Stderr, "step:", err)

					break
				}

				on = res.Layer
			}

		default:
			var wg sync.WaitGroup

			for range steps {
				wg.Go(func() {
					_, err := d.Run(ctx, node(), core.Worker{ID: "w"},
						[]ir.NodeID{first.Layer}, nil)
					if err != nil {
						fmt.Fprintln(os.Stderr, "step:", err)
					}
				})
			}

			wg.Wait()
		}

		spent := time.Since(began)
		rounds = append(rounds, spent)

		// **What this round decided**, not what every round has decided so far.
		// The fleet's wall clock varies by half while a single machine's varies
		// by six parts in a thousand, so the question is what differs between
		// rounds - and a cumulative account cannot say (E349, E350).
		r := d.Spend().Since(before)

		fmt.Printf("round %d      %v · %d delegated, %d here · %d fetch(es)"+
			" for %v\n", round+1, spent.Round(time.Millisecond),
			r.Delegated, r.Local, r.Fetches, r.Fetching.Round(time.Millisecond))
	}

	// What this build measured, for the next one - which is what a real build
	// does when it exits (E351).
	if err := d.Keep(); err != nil {
		fmt.Fprintln(os.Stderr, "keeping the rate:", err)
	}

	took = median(rounds)

	s := d.Spend()

	fmt.Printf("\n%d steps of %v producing %d bytes each, %d worker(s)\n",
		steps, compute, size, want)
	fmt.Printf("wall clock   %v (median of %d)\n",
		took.Round(time.Millisecond), len(rounds))

	if len(rounds) > 1 {
		fmt.Printf("rounds       %v\n", rounds)
	}
	fmt.Printf("%s\n", s.Report())

	// What the model said the same arrangement would move, so a surprise is
	// visible rather than merely absent (E268).
	// The base is an **input** to this build, not a step's output: it is seeded
	// on the driver and every delegated step has to pull it. Modelling it as
	// something a worker produced made the largest transfer in the run free
	// (E315).
	shape := fanOut(base, steps, int64(size))

	switch {
	case width > 1:
		shape = levelsFrom(base, max(steps/width, 1), width, int64(size))
	case chain:
		shape = chainFrom(base, steps, int64(size))
	}

	f := fleet.PredictWith(shape, want, steps,
		map[ir.NodeID]int64{base: baseBytes})
	fmt.Printf("forecast     %d byte(s) in %d transfer(s)\n", f.Moved, f.Transfers)

	if f.Moved != s.Fetched {
		fmt.Printf("MISMATCH     forecast %d, moved %d\n", f.Moved, s.Fetched)
	}

	return nil
}

func serve(
	ctx context.Context, at string, size int, compute time.Duration, room int,
	lazy bool, miss int,
) error {
	if at == "" {
		return fmt.Errorf("-driver is required for a worker")
	}

	addr, err := netip.ParseAddrPort(at)
	if err != nil {
		return fmt.Errorf("-driver %q is not host:port: %w", at, err)
	}

	id, err := fleet.DriverID(session, secret)
	if err != nil {
		return err
	}

	root, err := os.MkdirTemp("", "fleetprobe-")
	if err != nil {
		return fmt.Errorf("make a store: %w", err)
	}

	defer func() { _ = os.RemoveAll(root) }()

	err = os.MkdirAll(filepath.Join(root, "layers"), 0o750)
	if err != nil {
		return fmt.Errorf("make a store: %w", err)
	}

	store := &fleet.Layers{Root: root}

	blobs, err := iroh.Bind(ctx, iroh.WithALPNs(fleet.ALPNBlob))
	if err != nil {
		return fmt.Errorf("bind for blobs: %w", err)
	}

	defer func() { _ = blobs.Shutdown(context.WithoutCancel(ctx)) }()

	// **Whole layers and parts of layers.** A worker that has just fetched
	// exactly the bytes the next machine needs should be the one to send them,
	// or fragments come only from whoever holds everything and the fleet is a
	// star on its cheapest path (E325).
	served := &fleet.Parts{Whole: store}

	go func() {
		_ = fleet.ServeBlobs(ctx, blobs, served,
			func(err error) { fmt.Fprintln(os.Stderr, "serving:", err) })
	}()

	ctl, err := iroh.Bind(ctx)
	if err != nil {
		return fmt.Errorf("bind: %w", err)
	}

	defer func() { _ = ctl.Shutdown(context.WithoutCancel(ctx)) }()

	me := fleet.PeerAddr{ID: blobs.ID(), Host: blobs.LocalAddr().String()}

	fmt.Printf("worker serving as %v, room for %d, joining %v at %v\n",
		me, room, id, addr)

	x := &making{store: store, size: size, compute: compute, miss: miss}

	// **No sources on the executor.** `Runner` provisions from the holders the
	// driver named, dialled and corrected; the pair built here was aimed at the
	// driver's *control* identity and reached a protocol that serves no blobs,
	// which is why every lazy run between machines quietly fetched whole layers
	// and then, once that was fixed, refused outright (E314, E323).

	// **Lazy is the runner's job now, not this executor's.** The sources here
	// were built from the driver's *control* identity and speak no blob
	// protocol, so every lazy run between machines fell back to whole layers
	// without saying so (E314). `Runner` has the holders the driver named,
	// dialled and corrected, which is where a fragment has to come from (E323).
	var frags *fleet.Fragments

	if lazy {
		frags = &fleet.Fragments{Root: root}
		served.Some = frags
	}

	return fleet.Join(ctx, ctl, netaddr.NewEndpointAddr(id).WithIP(addr),
		fleet.Runner(x, core.Worker{ID: "probe"},
			fleet.WithCapacity(room),
			// No configured fallback: the driver names itself among the
			// holders of every step now (E277), so a worker needs to know
			// nothing beyond where to join.
			fleet.WithBlobs(store),
			fleet.WithFragments(frags),
			fleet.WithPeers(me.String(), func(a string) (fleet.Source, error) {
				a = fleet.AtDriver(addr.String())(a)

				p, err := fleet.ParsePeerAddr(a)
				if err != nil {
					return nil, err
				}

				to, err := p.Endpoint()
				if err != nil {
					return nil, err
				}

				return &fleet.PeerSource{Endpoint: ctl, Peer: to, Label: a}, nil
			})),
		func(err error) { fmt.Fprintln(os.Stderr, "worker:", err) },
		fleet.Serving(store))
}

// making is a step: wait, then leave a layer of the stated size behind.
type making struct {
	store *fleet.Layers

	// frags and from turn this worker lazy: a base primed with the paths a step
	// was predicted to read, instead of the whole layer (E308).
	frags *fleet.Fragments
	from  []fleet.Fragmenter
	whole []fleet.Source

	// miss makes one step in `miss` read outside its prediction, so the cost of
	// a wrong hint can be measured rather than assumed (E328). Zero is a probe
	// that always predicts perfectly, which is what every measurement before
	// this one was.
	miss int

	// Sized fields last, so the pointer-bearing ones above sit together and the
	// collector stops scanning sooner (govet fieldalignment).
	size    int
	compute time.Duration

	// room bounds how many steps run at once, which is what makes this machine
	// a machine. A synthetic step is a sleep, so without it eight steps take
	// the time of one and the driver outruns any fleet it is compared against
	// (E271, E321). Nil means no limit, which is right for a worker - `Runner`
	// already bounds it.
	room chan struct{}

	mu sync.Mutex
	n  int
}

func (m *making) Run(
	ctx context.Context, n *ir.Node, _ core.Worker, base []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	// What this step reads, when this executor is the one fetching it.
	//
	// On a worker it is not: `Runner` provisions from the holders the driver
	// named, whole or in part, before the step is handed over (E323). Only the
	// driver's own executor, which has no runner in front of it, still fetches
	// here - and it has nothing to fetch, because it holds what it seeded.
	if len(base) > 0 && (m.frags != nil || len(m.whole) > 0) {
		err := m.fetch(ctx, n, base)
		if err != nil {
			return core.Result{}, err
		}
	}

	if m.room != nil {
		select {
		case m.room <- struct{}{}:
			defer func() { <-m.room }()

		case <-ctx.Done():
			return core.Result{}, ctx.Err() //nolint:wrapcheck // a probe
		}
	}

	select {
	case <-time.After(m.compute):
	case <-ctx.Done():
		return core.Result{}, ctx.Err() //nolint:wrapcheck // a probe
	}

	m.mu.Lock()
	m.n++
	seq := m.n
	m.mu.Unlock()

	// A step that reads what nobody predicted. Only when it was *given* a
	// prediction: the retry arrives with that field cleared (E327), so the
	// mechanism under measurement is what tells the two attempts apart and this
	// needs no memory of which step it is.
	if m.miss > 0 && len(n.Meta.ReadsPredicted) > 0 && seq%m.miss == 0 {
		return core.Result{}, core.MissingInput{
			Layer: baseOf(base),
			// A file the base has and the prediction did not name, which is
			// what a misprediction looks like: not a missing file, a file
			// nobody thought to fetch.
			Path:  fmt.Sprintf("usr/lib/lib%d.so", 1000+seq),
			Where: "this step was told to read outside its prediction",
		}
	}

	tmp, err := os.MkdirTemp(m.store.Root, "made-")
	if err != nil {
		return core.Result{}, err //nolint:wrapcheck // a probe
	}

	// Distinct per step, so each produces its own layer rather than colliding -
	// which is the fixture bug E270 was hiding behind.
	body := make([]byte, m.size)
	copy(body, fmt.Sprintf("%d/%v", seq, base))

	err = os.WriteFile(filepath.Join(tmp, "out"), body, 0o600)
	if err != nil {
		return core.Result{}, err //nolint:wrapcheck // a probe
	}

	c, err := layer.Take(tmp)
	if err != nil {
		return core.Result{}, err //nolint:wrapcheck // a probe
	}

	if !m.store.Has(c.ID) {
		_ = os.Rename(tmp, filepath.Join(m.store.Root, "layers", c.ID.String()))
	}

	return core.Result{Layer: c.ID, Content: c.Content, Bytes: c.Bytes}, nil
}

// localFor is the driver's executor, or one that refuses when -local is off.
//
// Both arms exist because both arrangements are worth measuring: a driver with
// no local option is the CI shape, where the invoking machine is small and the
// fleet is the point; a driver that can run steps is the laptop shape, where
// declining to ship a large base is often the whole win (E318).
func localFor(m *making) core.Executor {
	if m == nil {
		return refusingLocal{}
	}

	return m
}

// refusingLocal is the driver's local executor, which this probe does not have.
type refusingLocal struct{}

func (refusingLocal) Run(
	context.Context, *ir.Node, core.Worker, []ir.NodeID, [][]ir.NodeID,
) (core.Result, error) {
	return core.Result{}, fmt.Errorf("this probe has no local executor;" +
		" a step that fell back to it is a fleet that did not take it")
}

func node() *ir.Node {
	return &ir.Node{
		Op:   ir.Op{Kind: ir.OpExec, Args: []string{"probe"}},
		Meta: ir.Meta{Source: "fleetprobe"},
	}
}

// chainFrom is n steps, each standing on what the last produced.
//
// **The shape a fan-out cannot measure.** A fan-out is every step ready at once,
// so a fleet is saturated before any start-up cost can matter and three correct
// changes in a row measured nothing (E335, E340, E342). A chain has no
// parallelism to sell: the only thing a fleet can do with it is fail to make it
// worse, and that is a question worth being able to ask (E343).
func chainFrom(base ir.NodeID, n int, size int64) []fleet.Step {
	out := make([]fleet.Step, 0, n)
	on := base

	for i := range n {
		made := ir.NodeID{byte(i + 2)} //nolint:gosec // a probe
		out = append(out, fleet.Step{
			Base:     []ir.NodeID{on},
			Produces: made,
			Size:     size,
		})
		on = made
	}

	return out
}

// levelsFrom is `levels` rounds of `width` steps, each round standing on what
// the round before produced.
//
// **The shape a build actually has.** A fan-out is every step ready at once,
// which flatters a fleet; a chain is one at a time, which cannot use one. A real
// graph is levels, and a fleet has to win the parallel part by more than it
// loses at each barrier (E345).
func levelsFrom(base ir.NodeID, levels, width int, size int64) []fleet.Step {
	out := make([]fleet.Step, 0, levels*width)
	on := base
	next := 2

	for range levels {
		var first ir.NodeID

		for range width {
			made := ir.NodeID{byte(next)} //nolint:gosec // a probe
			next++

			out = append(out, fleet.Step{
				Base:     []ir.NodeID{on},
				Produces: made,
				Size:     size,
			})

			if first == (ir.NodeID{}) {
				first = made
			}
		}

		on = first
	}

	return out
}

func fanOut(base ir.NodeID, n int, size int64) []fleet.Step {
	out := make([]fleet.Step, 0, n)

	for i := range n {
		out = append(out, fleet.Step{
			Base:     []ir.NodeID{base},
			Produces: ir.NodeID{byte(i + 2)}, //nolint:gosec // a probe
			Size:     size,
		})
	}

	return out
}

// fetch obtains what a step reads: the part of its base, or all of it.
func (m *making) fetch(ctx context.Context, n *ir.Node, base []ir.NodeID) error {
	a := fleet.Assignment{
		Base:  base,
		Hints: fleet.Hints{ReadsPredicted: n.Meta.ReadsPredicted},
	}

	if m.frags != nil && len(a.Hints.ReadsPredicted) > 0 {
		_, err := fleet.ProvisionFragments(ctx, m.frags, a, m.from...)
		if err != nil {
			return fmt.Errorf("prime: %w", err)
		}

		return nil
	}

	// The whole base, which is what this is measured against. Without it the
	// two modes are not a comparison: one fetches and the other does nothing.
	_, err := fleet.Provision(ctx, m.store, a, m.whole...)
	if err != nil {
		return fmt.Errorf("fetch the base: %w", err)
	}

	return nil
}

// seedBase writes a layer of n distinct files into a store and returns its id.
//
// Distinct, because a pack stores contents once per digest and a base of
// identical files would be a fiftieth of its apparent size - which is how E298's
// first table was wrong by a factor of fifty.
func seedBase(store *fleet.Layers, n, salt int) (ir.NodeID, int64, error) {
	tmp, err := os.MkdirTemp("", "seed-")
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("seed a base: %w", err)
	}

	defer func() { _ = os.RemoveAll(tmp) }()

	err = os.MkdirAll(filepath.Join(tmp, "usr", "lib"), 0o750)
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("seed a base: %w", err)
	}

	for i := range n {
		// **Salted, and the salt is not decoration.** E349 deleted it after
		// observing that two seeds already differ - on darwin, where the
		// directory mtimes a layer's identity includes were far enough apart to
		// separate them. On Linux they were not, so two rounds seeded the same
		// layer and every round after the first measured a warm fleet (E357).
		//
		// Content is a property of the corpus rather than of the filesystem's
		// clock, so this is the same everywhere.
		body := bytes.Repeat([]byte(fmt.Sprintf("%04d%04d", salt, i)), 1024)

		err = os.WriteFile(
			filepath.Join(tmp, "usr", "lib", fmt.Sprintf("lib%d.so", i)), body, 0o600)
		if err != nil {
			return ir.NodeID{}, 0, fmt.Errorf("seed a base: %w", err)
		}
	}

	c, err := layer.Take(tmp)
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("seed a base: %w", err)
	}

	at := filepath.Join(store.Root, "layers", c.ID.String())

	err = os.MkdirAll(filepath.Dir(at), 0o750)
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("seed a base: %w", err)
	}

	return c.ID, c.Bytes, os.Rename(tmp, at)
}

// sayingTransport prints what the first assignment carried.
//
// A probe exists to say what happened, and "the worker had no sources" is only
// half a sentence without what it was told (E311).
type sayingTransport struct {
	fleet.Transport

	once sync.Once
}

func (t *sayingTransport) Assign(
	ctx context.Context, a fleet.Assignment,
) (fleet.Reply, error) {
	t.once.Do(func() {
		fmt.Printf("assignment: base %d, holders %v, predicted %d path(s)\n",
			len(a.Base), a.Hints.Holders, len(a.Hints.ReadsPredicted))
	})

	return t.Transport.Assign(ctx, a) //nolint:wrapcheck // a probe
}

// Fragment forwards, so wrapping a store does not take a capability away.
//
// **A decorator that narrows the interface it decorates.** The blob server asks
// whether its store can send part of a layer with a type assertion; a wrapper
// that answers only `Has` and `Get` fails it silently, and every lazy request
// was answered with a whole layer that the caller then refused as not a
// fragment. The instrument that found E312 became the reason E323 could not be
// measured.
func (h *sayingHeld) Fragment(
	id ir.NodeID, want []string,
) (manifest, packed []byte, err error) {
	f, ok := h.Held.(interface {
		Fragment(ir.NodeID, []string) ([]byte, []byte, error)
	})
	if !ok {
		return nil, nil, fmt.Errorf("this store cannot fragment %v", id)
	}

	return f.Fragment(id, want)
}

// sayingHeld reports every lookup a peer makes.
type sayingHeld struct {
	fleet.Held

	once sync.Once
}

func (h *sayingHeld) Has(id ir.NodeID) bool {
	got := h.Held.Has(id)

	h.once.Do(func() {
		fmt.Printf("a peer asked for %v; this store has it: %v\n", id, got)
	})

	return got
}

// roomFor bounds how many steps this executor runs at once.
//
// Zero or less is no limit, which is what a worker wants: `Runner` bounds it
// already and a second gate would only queue behind the first.
func (m *making) roomFor(n int) {
	if n > 0 {
		m.room = make(chan struct{}, n)
	}
}

// localRoomOf is what this driver should say its own capacity is.
//
// Zero when there is no local executor: a driver that cannot run anything has
// no capacity to fill, and claiming one would make it decline work it has
// nowhere to put.
func localRoomOf(m *making, n int) int {
	if m == nil {
		return 0
	}

	return n
}

// baseOf is the layer a mispredicting step blames, or nothing if it stands on
// none.
func baseOf(base []ir.NodeID) ir.NodeID {
	if len(base) == 0 {
		return ir.NodeID{}
	}

	return base[0]
}

// median is the middle of what was measured, which is what a noisy wall clock
// can honestly report.
//
// The middle rather than the mean: one round that hit a garbage collection or a
// busy network moves a mean and does not move a median, and the question these
// numbers answer is "what does this usually cost" (E349).
func median(of []time.Duration) time.Duration {
	if len(of) == 0 {
		return 0
	}

	sorted := slices.Clone(of)
	slices.Sort(sorted)

	return sorted[len(sorted)/2]
}
