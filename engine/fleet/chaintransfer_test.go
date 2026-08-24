package fleet_test

import (
	"context"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// producing is a worker's executor: it writes a real layer into its own store.
//
// Real, because the number under measurement is bytes moved, and a fake that
// produces a digest without a tree moves nothing however wrong the placement is.
type producing struct {
	store *fleet.Layers
	size  int
	// compute is how long a step pretends to take, so a fleet can be timed
	// against one machine (E271). Zero means no pretending.
	compute time.Duration

	served *countingHeld

	mu    sync.Mutex
	ran   int
	asked int
	saw   []string
}

// countingHeld records how many blobs this machine handed to somebody else.
type countingHeld struct {
	fleet.Held

	mu   sync.Mutex
	gave int
}

func (c *countingHeld) Get(id ir.NodeID) ([]byte, error) {
	c.mu.Lock()
	c.gave++
	c.mu.Unlock()

	return c.Held.Get(id) //nolint:wrapcheck // a fixture
}

func (c *countingHeld) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.gave
}

// Name makes this worker a source of last resort that serves nothing, purely so
// the test can count how many times this machine went looking.
func (p *producing) Name() string { return "counted" }

// Fetch serves nothing and records that somebody asked.
func (p *producing) Fetch(
	_ context.Context, _ []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	p.mu.Lock()
	p.asked++
	p.mu.Unlock()

	return nil, nil
}

// looked is how many times this worker had to go outside for an input.
func (p *producing) looked() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.asked
}

// seen is what each step this worker ran was based on, and whether the store
// held it by the time the step began.
func (p *producing) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]string(nil), p.saw...)
}

// count is how many steps this worker took.
func (p *producing) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.ran
}

func (p *producing) Run(
	_ context.Context, _ *ir.Node, _ core.Worker, base []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	had := ""
	if len(base) > 0 {
		had = base[0].String()[:8]

		if !p.store.Has(base[0]) {
			had += "!MISSING"
		}
	}

	p.mu.Lock()
	p.ran++
	p.saw = append(p.saw, had)
	p.mu.Unlock()

	if p.compute > 0 {
		time.Sleep(p.compute)
	}

	tmp, err := os.MkdirTemp(p.store.Root, "produced-")
	if err != nil {
		return core.Result{}, err
	}

	// A layer that contains its base's name, so each step of the chain produces
	// a distinct one - a chain whose steps all produced the same layer would
	// need no transfer whatever the placement.
	name := "step"
	if len(base) > 0 {
		name = base[0].String()
	}

	body := make([]byte, p.size)
	copy(body, name)

	err = os.WriteFile(filepath.Join(tmp, "out"), body, 0o600)
	if err != nil {
		return core.Result{}, err
	}

	c, err := layer.Take(tmp)
	if err != nil {
		return core.Result{}, err
	}

	// Already there, which happens the moment two steps produce one layer -
	// and in this fixture every fanned-out step does, because they share a base.
	// `Layers.Put` tolerates exactly this for exactly this reason; a fixture that
	// did not turned a normal collision into a refusal, which then hid a real
	// accounting bug behind a noisy one (E270).
	if p.store.Has(c.ID) {
		_ = os.RemoveAll(tmp)

		return core.Result{Layer: c.ID, Content: c.Content, Bytes: c.Bytes}, nil
	}

	err = os.Rename(tmp, filepath.Join(p.store.Root, "layers", c.ID.String()))
	if err != nil {
		_ = os.RemoveAll(tmp)

		if !p.store.Has(c.ID) {
			return core.Result{}, err
		}
	}

	return core.Result{Layer: c.ID, Content: c.Content, Bytes: c.Bytes}, nil
}

// A chain of steps stays on one machine, and its base never moves.
//
// The measurement that decides whether a fleet can win at all. Each step's base
// is what the step before it produced, so a rotation that sends consecutive steps
// to different workers ships a base every single time - and a base is the
// largest thing this engine moves.
//
// Two workers, four steps, real endpoints, real stores. What is counted is bytes
// transferred, because that is the number a network turns into seconds.
func TestAChainDoesNotShipItsBaseEveryStep(t *testing.T) {
	t.Parallel()

	const (
		steps     = 4
		layerSize = 256 << 10
	)

	local := netip.AddrPortFrom(netip.IPv6Loopback(), 0)
	session := fleet.Session{Session: "chain", RunID: "1", Attempt: 1, Repo: "r"}
	secret := []byte("shared")

	driver, err := fleet.BindDriver(t.Context(), session, secret, iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = driver.Shutdown(context.WithoutCancel(t.Context())) })

	r := &fleet.Rendezvous{Reach: 20 * time.Second}

	go func() { _ = r.Accept(t.Context(), driver, func(err error) { t.Logf("driver: %v", err) }) }()

	id, err := fleet.DriverID(session, secret)
	if err != nil {
		t.Fatal(err)
	}

	workers := make([]*producing, 0, 2)

	for i := range 2 {
		w := startWorker(t, i, local,
			netaddr.NewEndpointAddr(id).WithIP(driver.LocalAddr()), 0, 0)
		workers = append(workers, w)
	}

	for deadline := time.Now().Add(20 * time.Second); r.Workers() < 2 &&
		time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}

	if r.Workers() < 2 {
		t.Skipf("only %d worker(s) joined", r.Workers())
	}

	d := &fleet.Delegating{Local: &countingLocal{}, Fleet: r}

	var base []ir.NodeID

	for i := range steps {
		res, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w"}, base, nil)
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}

		base = []ir.NodeID{res.Layer}
	}

	s := d.Spend()

	t.Logf("%d steps, %d worker(s): %d byte(s) moved, %v fetching, %v computing,"+
		" %v overhead", steps, len(workers), s.Fetched, s.Fetching, s.Computing, s.Overhead)

	// And the forecast said so beforehand. **A disagreement here is a bug in one
	// of them**, which is the whole reason to predict with the same code that
	// places: a simulator with its own model would agree until somebody edited
	// one of the two, and its agreement would then mean nothing (E268).
	want := fleet.Predict(chainOf(steps, layerSize), 2, 1)
	if want.Moved != s.Fetched {
		t.Errorf("the forecast said %d byte(s) and the fleet moved %d"+
			"\n  one of the two is wrong, and neither is allowed to be the"+
			" model that gets to be approximately right",
			want.Moved, s.Fetched)
	}

	// One transfer at most: the chain may start on either worker, and once it
	// has started it should stay there. Round-robin over four steps moves three
	// bases; affinity moves none after the first placement.
	if s.Fetched > layerSize {
		t.Errorf("moved %d bytes for a chain of %d steps whose layers are ~%d"+
			" bytes each\n  a chain that changes machines carries its base with"+
			" it, and a fleet that does that is slower than one machine no"+
			" matter how many machines it has", s.Fetched, steps, layerSize)
	}
}

// startWorker brings up one worker: its own store, its own endpoints.
// startWorker brings a worker up, already configured.
//
// `compute` is a parameter rather than a field the caller sets afterwards,
// because the worker is serving before this returns: assigning to it from the
// test races the goroutine reading it, which `-race` reports on any test sharing
// the package. *Failure class: a field set after the thing that reads it has
// started.*
func startWorker(
	t *testing.T, n int, local netip.AddrPort, driver netaddr.EndpointAddr,
	room int, compute time.Duration,
) *producing {
	t.Helper()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "layers"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	store := &fleet.Layers{Root: root}

	blobs, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local),
		iroh.WithALPNs(fleet.ALPNBlob))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = blobs.Shutdown(context.WithoutCancel(t.Context())) })

	served := &countingHeld{Held: store}

	go func() {
		_ = fleet.ServeBlobs(t.Context(), blobs, served,
			func(err error) { t.Logf("worker %d serving: %v", n, err) })
	}()

	ctl, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = ctl.Shutdown(context.WithoutCancel(t.Context())) })

	x := &producing{store: store, size: 256 << 10, served: served, compute: compute}
	me := fleet.PeerAddr{ID: blobs.ID(), Host: blobs.LocalAddr().String()}

	go func() {
		_ = fleet.Join(t.Context(), ctl, driver,
			fleet.Runner(x, core.Worker{ID: "w"},
				fleet.WithCapacity(room),
				fleet.WithBlobs(store, x),
				fleet.WithPeers(me.String(), func(at string) (fleet.Source, error) {
					x.mu.Lock()
					x.asked++
					x.mu.Unlock()

					p, err := fleet.ParsePeerAddr(at)
					if err != nil {
						return nil, err
					}

					to, err := p.Endpoint()
					if err != nil {
						return nil, err
					}

					return &fleet.PeerSource{Endpoint: ctl, Peer: to, Label: at}, nil
				})),
			func(err error) { t.Logf("worker %d: %v", n, err) })
	}()

	return x
}

// A fan-out spreads across the fleet even though every step shares a base.
//
// The end-to-end half of the ordering's other duty. E265's affinity keeps a
// chain on one machine; left unchecked it would keep *everything* on one
// machine, because almost every build starts from one common image and the first
// worker to hold it would then be the preferred place for every step in the
// build.
//
// Run concurrently, because that is the only condition under which the question
// arises: steps placed one at a time on an idle fleet all belong on the holder,
// and it is a machine already working that is not the cheapest place for the
// next one.
func TestAFanOutSpreadsAcrossARealFleet(t *testing.T) {
	t.Parallel()

	const (
		wide      = 8
		layerSize = 64 << 10
	)

	local := netip.AddrPortFrom(netip.IPv6Loopback(), 0)
	session := fleet.Session{Session: "fanout", RunID: "1", Attempt: 1, Repo: "r"}
	secret := []byte("shared")

	driver, err := fleet.BindDriver(t.Context(), session, secret, iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = driver.Shutdown(context.WithoutCancel(t.Context())) })

	r := &fleet.Rendezvous{Reach: 20 * time.Second}

	go func() { _ = r.Accept(t.Context(), driver, func(err error) { t.Logf("driver: %v", err) }) }()

	id, err := fleet.DriverID(session, secret)
	if err != nil {
		t.Fatal(err)
	}

	at := netaddr.NewEndpointAddr(id).WithIP(driver.LocalAddr())

	workers := make([]*producing, 0, 3)
	for i := range 3 {
		workers = append(workers, startWorker(t, i, local, at, 0, 0))
	}

	for deadline := time.Now().Add(20 * time.Second); r.Workers() < 3 &&
		time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}

	if r.Workers() < 3 {
		t.Skipf("only %d worker(s) joined", r.Workers())
	}

	seen := &loggingTransport{Transport: r}
	d := &fleet.Delegating{Local: &countingLocal{}, Fleet: seen}

	// One step to make the base everybody shares.
	first, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Then a wide fan-out from it, all at once.
	var wg sync.WaitGroup

	errs := make(chan error, wide)

	for range wide {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w"},
				[]ir.NodeID{first.Layer}, nil)
			if err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("a fanned-out step failed: %v", err)
	}

	ran := 0
	for _, w := range workers {
		if w.count() > 0 {
			ran++
		}
	}

	if ran < 2 {
		t.Errorf("%d of %d worker(s) ran anything for an %d-way fan-out"+
			"\n  every step shared a base, so affinity sent them all to whoever"+
			" held it and the rest of the fleet watched", ran, len(workers), wide)
	}

	moved := d.Spend().Fetched

	per := make([]int, 0, len(workers))
	for _, w := range workers {
		per = append(per, w.count())
	}

	t.Logf("%d-way fan-out over %d workers: %d ran %v, %d byte(s) moved",
		wide, len(workers), ran, per, moved)

	for i, w := range workers {
		t.Logf("  worker %d saw %v, looked %d, served %d",
			i, w.seen(), w.looked(), w.served.count())
	}

	t.Logf("  replies %v", seen.replies())

	// Exact, and it was not always. This assertion was an upper bound for one
	// iteration while a disagreement was chased down - the model said two
	// transfers, the fleet reported one or none - and the disagreement turned
	// out to be a refusal path that dropped what it had already moved (E270).
	// The bound is restored to equality because the reason to weaken it is gone,
	// which is the only good reason to restore one.
	want := fleet.Predict(fanOutOf(wide, 256<<10), len(workers), wide)
	if want.Moved != moved {
		t.Errorf("the forecast said %d byte(s) and the fleet moved %d"+
			"\n  one of the two is wrong, and neither is allowed to be the"+
			" model that gets to be approximately right\n  replies: %v",
			want.Moved, moved, seen.replies())
	}
}

// chainOf is a chain of n steps, each based on the one before.
func chainOf(n int, size int64) []fleet.Step {
	out := make([]fleet.Step, 0, n)

	for i := range n {
		s := fleet.Step{Produces: ir.NodeID{byte(i + 1)}, Size: size}
		if i > 0 {
			s.Base = []ir.NodeID{{byte(i)}}
		}

		out = append(out, s)
	}

	return out
}

// fanOutOf is one step and then n independent steps from it.
func fanOutOf(n int, size int64) []fleet.Step {
	out := []fleet.Step{{Produces: ir.NodeID{1}, Size: size}}

	for i := range n {
		out = append(out, fleet.Step{
			Base:     []ir.NodeID{{1}},
			Produces: ir.NodeID{byte(i + 2)},
			Size:     size,
		})
	}

	return out
}

// loggingTransport records what every reply said, as the driver saw it.
//
// The probe E269 named: it distinguishes a reply whose transfer was never
// reported from one that never arrived.
type loggingTransport struct {
	fleet.Transport

	mu   sync.Mutex
	said []string
}

func (l *loggingTransport) Assign(
	ctx context.Context, a fleet.Assignment,
) (fleet.Reply, error) {
	r, err := l.Transport.Assign(ctx, a)

	l.mu.Lock()

	switch {
	case err != nil:
		l.said = append(l.said, "err:"+err.Error())
	case r.Refused != "":
		l.said = append(l.said, "refused:"+r.Refused)
	default:
		l.said = append(l.said, strconv.FormatInt(r.FetchedBytes, 10))
	}

	l.mu.Unlock()

	return r, err //nolint:wrapcheck // a fixture
}

func (l *loggingTransport) replies() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]string(nil), l.said...)
}
