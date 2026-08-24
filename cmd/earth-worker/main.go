// Command earth-worker joins a fleet and builds steps for it.
//
// A worker has no address anybody needs to know and nothing to listen on: it is
// **told where the driver is** and derives who the driver is from the shared
// secret (C.1). That is what makes it deployable anywhere a machine can reach
// the driver - behind whatever NAT its operator has, with no port forwarded and
// no certificate to manage.
//
//	EARTH_FLEET_SECRET=… EARTH_FLEET_SESSION=… EARTH_FLEET_DRIVER=host:port earth-worker
//
// It takes every core the machine has unless `EARTH_FLEET_CAPACITY` says
// otherwise, which is what a machine somebody is also using wants.
//
// It exits when the driver goes away, which is the right lifetime: a worker
// outliving its fleet is a process nobody is watching.
package main

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func main() {
	err := run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "earth-worker: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Interrupted rather than killed: a worker mid-step should finish telling
	// the driver what happened, and a second interrupt still kills.
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	session, secret, err := fleet.FromEnv()
	if err != nil {
		return err
	}

	id, err := fleet.DriverID(session, secret)
	if err != nil {
		return err
	}

	where, err := driverAt(id, os.Getenv(fleet.EnvDriver))
	if err != nil {
		return err
	}

	// The same reachability the driver binds with: a worker behind NAT that
	// only ever dials out still has to be *fetchable*, and a worker that cannot
	// resolve the driver's identity cannot join at all (E505).
	sk, err := key.GenerateSecretKey()
	if err != nil {
		return fmt.Errorf("a key for this worker: %w", err)
	}

	found := fleet.Discovery(sk)

	e, err := iroh.Bind(ctx, append([]iroh.Option{iroh.WithSecretKey(sk)},
		found.Options()...)...)
	if err != nil {
		return fmt.Errorf("bind this worker: %w", err)
	}

	found.Announce(ctx, e)

	defer func() { _ = e.Shutdown(context.Background()) }()

	// The same sandbox and executor a local build uses. **A delegate is an
	// engine** (C.3): every invariant in §5 binds it as it binds the parent, and
	// the way to be sure of that is to be the same code.
	sb, err := workerSandbox()
	if err != nil {
		return err
	}

	x, err := exec.New(sb)
	if err != nil {
		return fmt.Errorf("start the executor: %w", err)
	}

	defer func() { _ = x.Close() }()

	room, err := fleet.CapacityFromEnv()
	if err != nil {
		return err
	}

	// Where this worker keeps layers, as both a destination and a source. The
	// same directory the executor materialises from - a layer fetched here has
	// to be the layer a build finds.
	layers := &fleet.Layers{Root: sb.StoreDir()}

	// A second endpoint for `earth/blob/1`, so this worker can serve what it
	// produces to its peers rather than every one of them going back to the
	// driver (E260). Separate from the control endpoint because a blob transfer
	// is long and a control message must not queue behind one.
	blobKey, err := key.GenerateSecretKey()
	if err != nil {
		return fmt.Errorf("a key for this worker's blob endpoint: %w", err)
	}

	blobsFound := fleet.Discovery(blobKey)

	blobs, err := iroh.Bind(ctx, append([]iroh.Option{
		iroh.WithALPNs(fleet.ALPNBlob), iroh.WithSecretKey(blobKey),
	}, blobsFound.Options()...)...)
	if err != nil {
		return fmt.Errorf("bind this worker's blob endpoint: %w", err)
	}

	blobsFound.Announce(ctx, blobs)

	defer func() { _ = blobs.Shutdown(context.WithoutCancel(ctx)) }()

	// **Whole layers and the parts of layers this worker holds.**
	//
	// A worker that has just fetched exactly the bytes the next machine needs
	// should be the one to send them; serving only whole layers means every
	// fragment comes from whoever holds everything, which is the driver, and
	// lazy transfer is a star on its cheapest path (E325, E331).
	frags := &fleet.Fragments{Root: sb.StoreDir()}
	served := &fleet.Parts{Whole: layers, Some: frags}

	go func() {
		_ = fleet.ServeBlobs(ctx, blobs, served,
			func(err error) { fmt.Fprintf(os.Stderr, "earth-worker: serving: %v\n", err) })
	}()

	// What this worker announces, so the driver can point later steps here.
	// Advisory: a peer that cannot reach it falls back to the driver (I5).
	me := fleet.PeerAddr{ID: blobs.ID(), Host: blobs.LocalAddr().String()}

	// **Lazy transfer, turned on.** A step's base is primed with the paths it was
	// predicted to read, and anything unpredicted is faulted in while it runs -
	// 1.8% of a 16 MB base measured between machines, and the difference between
	// a fleet of four that is 1.57x faster than one machine and one that is 2.8x
	// slower (E323, E326).
	//
	// The sources are the holders the driver named, per assignment. An earlier
	// comment here said fetching fragments from a peer "wants the holder hints
	// an assignment already carries, which is a refinement rather than a gap" -
	// it was a gap, and the hints were being ignored (E329).

	// **Whoever the driver most recently said holds this build's layers**, not
	// an address chosen before any assignment existed.
	//
	// This used to be the driver's *control* identity dialled with the blob
	// protocol, which that endpoint does not offer - so priming and fault-in
	// have never worked between machines, in the binary people actually run
	// (E314 in the probe, E329 here). `Runner` refreshes the sink from every
	// assignment's holders, corrected and dialled, with the driver last (C.4).
	peers := &fleet.Peers{}
	from := []fleet.Fragmenter{peers}

	x.Prime = func(
		ctx context.Context, stack []ir.NodeID, want []string, into string,
	) error {
		f := &fleet.Filler{Into: into, Stack: stack, From: from, Store: frags}

		return f.Prime(ctx, want)
	}

	x.Fetch = func(
		ctx context.Context, stack []ir.NodeID, into, at string,
	) error {
		f := &fleet.Filler{Into: into, Stack: stack, From: from, Store: frags}

		return f.Fill(ctx, at)
	}

	x.Scratch = sb.StoreDir()

	// **And be told no.** A backend that cannot fault in leaves the base
	// materialised whole, which is slower and correct - so the worker asks
	// rather than assumes, and says so rather than silently priming a base
	// nothing can complete (E305).
	filler, ok := sb.(interface {
		SetFill(func(handle, path string) error)
	})
	if !ok {
		x.Prime = nil
		x.Fetch = nil

		fmt.Fprintln(os.Stderr,
			"earth-worker: this sandbox cannot fault paths in, so steps get"+
				" whole layers")
	} else {
		// The build's context, not a fault-in's: a fetch outliving the build
		// that wanted it is a worker doing work for nobody.
		filler.SetFill(func(handle, path string) error {
			return x.FillFor(ctx, handle, path)
		})
	}

	fmt.Fprintf(os.Stderr,
		"earth-worker: joining %v at %v, serving layers as %v, room for %d step(s),"+
			" fetching what steps read\n",
		id, whereFrom(where), me, room)

	say := func(err error) { fmt.Fprintf(os.Stderr, "earth-worker: %v\n", err) }

	join := func(ctx context.Context) error {
		// Where, not just who. Connect dials the addresses it is handed and
		// consults no resolver of its own, so an identity derived from the
		// secret has to be looked up before it can be dialled (E505).
		at, err := found.Find(ctx, where)
		if err != nil {
			return err
		}

		return fleet.Join(ctx, e, at,
			fleet.Runner(x, core.Worker{ID: "worker"},
				fleet.WithCapacity(room),
				fleet.WithBlobs(layers),
				fleet.WithFragments(frags),
				fleet.WithPeerSink(peers),
				fleet.WithPeers(me.String(), dialPeer(ctx, e, found, os.Getenv(fleet.EnvDriver)))),
			say,
			// Reachable without being reachable: the driver fetches what this
			// worker produced over the connection this worker opened (E279).
			fleet.Serving(layers),
			// What this worker is, said on arrival.
			//
			// Placement refuses a worker that has not declared a platform, and a
			// worker used to declare one by echoing the platform of an assignment
			// it had run - so it could never be given a first step (E503). The
			// platform is the sandbox's, not this process's: a darwin worker runs
			// `linux/<arch>` steps in a VM, and saying `darwin/<arch>` would refuse
			// every step it can actually run.
			fleet.Runs(exec.DefaultPlatform(), room, me.String()))
	}

	// Once if this worker was told where the driver is, repeatedly if it has to
	// find it: an endpoint publishes where it is seconds after it binds, and a
	// single dial at startup loses that race nearly every time (E505).
	return fleet.KeepJoining(ctx, fleet.DefaultPatience, 3*time.Second, join, say)
}

// dialPeer turns a holder hint into somewhere to fetch from.
//
// The address is another machine's claim about itself, forwarded by a driver
// that did not check it (A5). Nothing here trusts it: a name that will not parse
// is refused, and bytes that do arrive are checked against the digest that was
// asked for - so the worst a wrong address can do is cost a retry.
func dialPeer(
	ctx context.Context, e *iroh.Endpoint, found *fleet.Reachable, driver string,
) func(string) (fleet.Source, error) {
	// Empty where this worker was never told the driver's address, and
	// `AtDriver` then leaves every hint alone - which is right: the fixup exists
	// to replace an *unspecified* host with the one we dialled, and a worker
	// that discovered its driver has no such answer to substitute (E505).
	fix := fleet.AtDriver(driver)

	return func(at string) (fleet.Source, error) {
		at = fix(at)

		p, err := fleet.ParsePeerAddr(at)
		if err != nil {
			return nil, err
		}

		to, err := p.Endpoint()
		if err != nil {
			return nil, err
		}

		// A peer that bound to the wildcard is an identity and nothing more, so
		// it has to be looked up the same way the driver was (E505).
		to, err = found.Find(ctx, to)
		if err != nil {
			return nil, err
		}

		return &fleet.PeerSource{Endpoint: e, Peer: to, Label: at}, nil
	}
}

// driverAt is where to reach the driver: its identity, and its address if this
// worker was told one.
//
// A worker refused to start without `EARTH_FLEET_DRIVER`, reasoning that it
// derives *who* the driver is and has to be told *where*. The first half is
// right; the second is not. `netaddr.NewEndpointAddr` takes addresses
// variadically and iroh finds a peer by node id through discovery and relays
// when it has none - which is how N GitHub runners, with no route to each other
// and no address worth telling anybody, form a mesh at all (E505).
//
// The address stays as a **hint**. On one machine or one LAN it is the fast
// path, and skipping discovery is worth having when the answer is already known.
//
// A malformed one is still refused. The one thing worse than no address is a
// typo read as none: a worker that quietly fell back to discovery would take
// longer to fail and never say why.
func driverAt(id key.EndpointID, at string) (netaddr.EndpointAddr, error) {
	if at == "" {
		return netaddr.NewEndpointAddr(id), nil
	}

	ap, err := netip.ParseAddrPort(at)
	if err != nil {
		return netaddr.EndpointAddr{}, fmt.Errorf(
			"%s is %q, which is not host:port: %w"+
				"\n  leave it unset to find the driver by its identity instead",
			fleet.EnvDriver, at, err)
	}

	return netaddr.NewEndpointAddr(id).WithIP(ap), nil
}

// whereFrom says how this worker is reaching the driver, for the log.
func whereFrom(at netaddr.EndpointAddr) string {
	if len(at.Addrs()) == 0 {
		return "an address it discovers"
	}

	return fmt.Sprint(at.Addrs()[0])
}
