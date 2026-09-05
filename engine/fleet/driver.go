package fleet

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

const (
	// EnvWorkers is how many workers this build expects. **Its absence, not a
	// zero, is what means "no fleet"** - see Driver.
	EnvWorkers = "EARTH_FLEET_WORKERS"
	// EnvWait bounds how long the driver waits for them to arrive, as a Go
	// duration (`90s`, `2m`). A worker pool that failed to start must not hang
	// the build.
	EnvWait = "EARTH_FLEET_WAIT"
)

// defaultWait is how long a driver waits for its fleet when nobody said.
//
// Long enough for a CI runner to be scheduled, pull an image and dial; short
// enough that a pool which never starts costs a minute rather than a build. The
// figure is a judgement, which is why it is overridable and why the degrade
// below is loud.
const defaultWait = 90 * time.Second

// Driver is the seam between a build and a fleet, and it is *mostly* a
// pass-through.
//
// Returns the executor the build should use, something to stop when it is done,
// and an error only for configuration that cannot be honoured. Three outcomes:
//
//   - no fleet asked for: the executor it was handed, unchanged. Not a wrapper
//     around it - the same one - so the overwhelmingly common build takes
//     exactly the path this engine was tested with;
//   - a fleet asked for and joined: a Delegating over the workers that arrived;
//   - a fleet asked for and nobody came: the local executor again, **with the
//     shortfall reported**. I11's degrade, out loud (E255).
//
// note receives lines meant for a person. Nil is fine, and means nobody is
// listening - which is why the shortfall is reported through it rather than
// returned: a caller that ignores the message still gets a working build.
// store is where a driver keeps layers it has to bring back from its workers.
//
// An interface parameter rather than a path, so `Driver` does not have to know
// how a store is laid out - and so a caller with no store at all (a plan-only
// build, a test) can pass nothing and get a fleet that never brings anything
// back, which is correct for a build that never runs a step here.
func Driver(
	ctx context.Context, local core.Executor, note func(string), store Store,
	profiles core.Profiles,
) (core.Executor, func(), error) {
	if note == nil {
		note = func(string) {}
	}

	want, err := workersWanted()
	if err != nil {
		return nil, nil, err
	}

	// The common case, and the cheap one: no bind, no wait, no wrapper.
	//
	// Keyed on the worker count rather than on the secret, because a secret in
	// the environment for some later step is not a request for a fleet, and
	// charging that build a bind and a timeout would be a surprise.
	if want <= 0 {
		return local, nil, nil
	}

	wait, err := waitFor()
	if err != nil {
		return nil, nil, err
	}

	session, secret, err := FromEnv()
	if err != nil {
		return nil, nil, err
	}

	// The driver's own lifetime, so shutting the fleet down does not depend on
	// the build's context still being live.
	serving, stop := context.WithCancel(context.WithoutCancel(ctx))

	e, err := BindDriver(serving, session, secret)
	if err != nil {
		stop()

		return nil, nil, fmt.Errorf("bind this driver: %w", err)
	}

	shutdown := func() {
		stop()

		_ = e.Shutdown(context.WithoutCancel(ctx))
	}

	r := &Rendezvous{}

	go func() { _ = r.Accept(serving, e, func(err error) { note(err.Error()) }) }()

	// Blobs on their own endpoint, as a worker does. Two reasons, and the second
	// is the one that forces it: a blob transfer is long and a control message
	// must not queue behind one, and `Accept` on a shared endpoint takes
	// whichever connection arrives regardless of which protocol it wanted.
	//
	// **The driver holds the base of every build**, and until this it served
	// none of it - so a worker that could not reach a peer had nowhere to fall
	// back to, and the fallback in the code had never been reachable (E277).
	self := ""

	if store != nil {
		// A key of its own: the blob endpoint is a second identity and publishes
		// itself, and reusing the driver's would announce two different
		// addresses under one name.
		blobKey, keyErr := key.GenerateSecretKey()
		if keyErr != nil {
			stop()

			return nil, nil, fmt.Errorf("a key for this driver's blob endpoint: %w", keyErr)
		}

		blobsFound := Discovery(blobKey)

		blobs, err := iroh.Bind(serving, append([]iroh.Option{
			iroh.WithALPNs(ALPNBlob), iroh.WithSecretKey(blobKey),
		}, blobsFound.Options()...)...)
		if err != nil {
			stop()

			return nil, nil, fmt.Errorf("bind this driver's blob endpoint: %w", err)
		}

		blobsFound.Announce(serving, blobs)

		go func() {
			_ = ServeBlobs(serving, blobs, store, func(err error) { note(err.Error()) })
		}()

		self = PeerAddr{ID: blobs.ID(), Host: blobs.LocalAddr().String()}.String()
	}

	note(announcement(fmt.Sprint(e.ID()), e.LocalAddr().String(), wait, want))

	deadline, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	got := r.WaitFor(deadline, want)

	if got == 0 {
		// Degrade, not refuse: the build is perfectly possible here, only
		// slower. Named precisely, because a fleet build that silently became a
		// local one looks like a slow fleet and costs somebody an afternoon on
		// the network.
		note(fmt.Sprintf("fleet: 0 of %d worker(s) joined within %v"+
			" - building locally"+
			"\n  check that the workers were given %s=<this driver's address>"+
			" and the same %s", want, wait, EnvDriver, EnvSecret))
		shutdown()

		return local, nil, nil
	}

	if got < want {
		// Proceeding with what arrived. Waiting out the deadline for the rest
		// would spend the difference between a slow build and a late one on a
		// worker whose CI job may simply have failed to start.
		note(fmt.Sprintf("fleet: %d of %d worker(s) joined - building with %d",
			got, want, got))
	} else {
		note(fmt.Sprintf("fleet: %d worker(s) joined", got))
	}

	d := &Delegating{
		Local:   local,
		Fleet:   r,
		Note:    note,
		Self:    self,
		Store:   store,
		Predict: readsFrom(profiles),
		// A worker's layer, fetched the same way a worker fetches one from a
		// peer: the driver is a peer for this purpose, and the address is a
		// claim the worker made about itself (E274).
		Peers: func(at string) (Source, error) {
			// **Over the connection the worker opened**, when there is one.
			//
			// A worker is behind whatever NAT its operator has and nothing ever
			// dials back (E279), which is what the rendezvous back-channel is
			// for. Dialling was never exercised because nothing asked a driver
			// to fetch from a worker until it could keep a step whose inputs
			// were there - and then five steps of sixteen failed and the build
			// took fifteen seconds (E347).
			if src, ok := r.SourceFor(at); ok {
				return src, nil
			}

			p, err := ParsePeerAddr(at)
			if err != nil {
				return nil, err
			}

			to, err := p.Endpoint()
			if err != nil {
				return nil, err
			}

			return &PeerSource{Endpoint: e, Peer: to, Label: at}, nil
		},
	}

	// What this machine is, and how big the things it holds are. Without both,
	// two measured mechanisms are inert in every real build (E330).
	wire(d, DefaultCapacity(), store)

	// And what this fleet cost last time. Without it every build is round one:
	// an unmeasured fleet prices a transfer at nothing, delegates everything and
	// keeps nothing, which measured 1.447s against 1.084s for the same work
	// once it knew (E350, E351).
	d.Remember(store)

	// The account, said once when the build is over. A fleet that was no faster
	// than one machine has to be able to say *why* - transfer, overhead or
	// compute - or the next attempt is a guess (E259).
	report := func() {
		note(d.Spend().Report())

		// What this build measured, for the next one. Best effort: a rate that
		// could not be written costs the next build a warm-up round, and
		// failing a build over it would make an optimisation load-bearing
		// (I5, E351).
		_ = d.Keep()

		shutdown()
	}

	return d, report, nil
}

// workersWanted is how many workers were asked for, or zero for none.
func workersWanted() (int, error) {
	v := os.Getenv(EnvWorkers)
	if v == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(v)
	if err != nil {
		// Refused rather than assumed: reading a typo as zero turns it into a
		// silently local build, which is the outcome the loud degrade above
		// exists to make visible.
		return 0, fmt.Errorf("%s is %q, which is not a number"+
			"\n  it is how many workers this build waits for; unset it for a"+
			" local build", EnvWorkers, v)
	}

	return n, nil
}

// waitFor is how long to wait for them.
func waitFor() (time.Duration, error) {
	v := os.Getenv(EnvWait)
	if v == "" {
		return defaultWait, nil
	}

	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s is %q, which is not a duration like 90s or 2m: %w",
			EnvWait, v, err)
	}

	if d <= 0 {
		return 0, fmt.Errorf("%s is %q; a driver that waits no time at all"+
			" never sees a worker, so this would always build locally",
			EnvWait, v)
	}

	return d, nil
}

// readsFrom turns the profile store into a read-set predictor.
//
// The same source Κ₂ uses, asked the same question: what did a step of this
// class look at last time. Nothing new is recorded for this - the observation
// was already being kept, and until now nothing sent it anywhere (E287).
//
// Nil profiles means a driver with nothing to say, which is what a first build
// has.
func readsFrom(profiles core.Profiles) func(*ir.Node) []string {
	if profiles == nil {
		return nil
	}

	return func(n *ir.Node) []string {
		obs, ok := profiles.Get(core.StepClass(n))
		if !ok {
			return nil
		}

		out := make([]string, 0, len(obs.Reads))
		for p := range obs.Reads {
			out = append(out, p)
		}

		// Sorted, because a hint that varied with a map's iteration order would
		// name one fragment differently on every build - and a fragment is named
		// by the paths it holds (E282).
		sort.Strings(out)

		return out
	}
}

// sizer is a store that can say how big a layer is.
type sizer interface {
	Size(id ir.NodeID) (int64, bool)
}

// wire fills in what a driver knows about itself.
//
// **Two fields the probe set by hand and the product did not**, which made two
// measured mechanisms inert in a real build (E330):
//
//   - `Room` of zero means "as many as arrive", so this machine finishes every
//     step in one wave however much is running - the infinitely-parallel
//     denominator E321 exists to correct;
//   - `Sizes` of nil means every layer no step produced, which is the base image
//     of every build, weighs nothing - so E317's transfer term is always absent
//     and the driver delegates as though the network were free.
//
// Measured once per layer. A layer is a directory of thousands of files and
// every delegable step asks what it weighs; walking it per step would put a stat
// storm in front of the mechanism that exists to avoid moving bytes.
func wire(d *Delegating, room int, store Keeper) {
	d.Room = room

	from, ok := store.(sizer)
	if !ok {
		return
	}

	var (
		mu   sync.Mutex
		seen = map[ir.NodeID]int64{}
	)

	d.Sizes = func(id ir.NodeID) int64 {
		mu.Lock()
		defer mu.Unlock()

		if n, ok := seen[id]; ok {
			return n
		}

		n, _ := from.Size(id)
		seen[id] = n

		return n
	}
}

// announcement is what a driver says while it waits.
//
// It said the driver's *identity* and nothing else - the one term a worker does
// not need, because it derives it from the shared secret - and omitted the
// address, which is the one term it cannot derive. Its own failure note then
// told the reader to set `EARTH_FLEET_DRIVER=<this driver's address>`, naming a
// value the tool had never printed (E502).
//
// The identity stays: a worker does not need it and a person reading two builds'
// logs side by side does.
func announcement(id, addr string, wait time.Duration, want int) string {
	line := fmt.Sprintf("fleet: %s, waiting %v for %d worker(s)", id, wait, want)

	if addr == "" {
		// Nothing rather than an empty setting. `EARTH_FLEET_DRIVER=` looks
		// like something to copy.
		return line
	}

	// A wildcard bind is honest and unusable: nobody can dial `[::]`. Worse, a
	// worker elsewhere needs no address at all - it finds this driver by the
	// identity it derives from the shared secret - so offering one that cannot
	// work would send a reader looking for a networking problem they do not
	// have (E505).
	ap, err := netip.ParseAddrPort(addr)
	if err == nil && ap.Addr().IsUnspecified() {
		return fmt.Sprintf(
			"%s\n  a worker elsewhere needs only the same %s; on this machine,"+
				" %s=127.0.0.1:%d",
			line, EnvSecret, EnvDriver, ap.Port())
	}

	return fmt.Sprintf("%s\n  workers join with %s=%s and the same %s",
		line, EnvDriver, addr, EnvSecret)
}
