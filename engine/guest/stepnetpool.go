package guest

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
)

// stepNets keeps a step's network built before the step asks for it.
//
// **The setup moves off the critical path; the freshness stays.** A step's own
// network namespace costs about 6ms of a 17ms step - a third of the guest-side
// overhead of a small one - and it is built while the step waits. It does not
// have to be: the guest is running the *previous* step at the time, and that is
// when this one's network could be made.
//
// Every step still gets a namespace nobody has used. That is not incidental. A
// namespace is self-cleaning because it is destroyed, and this engine already
// knows that steps leave processes running behind them - `stepWaitDelay` exists
// because one inherited a pipe and held a build open (E519). Handing a used
// namespace to the next step would hand it that process's listening socket
// too, which is the port collision E923 was about, returning nondeterministic
// and much harder to find. So: built ahead, used once, destroyed.
type stepNets struct {
	build func() (string, func(), string)
	depth int

	ready chan readyNet

	// off is set when the machine cannot give a step its own network. The
	// reason is systemic - no resolver, no `ip netns`, no `CONFIG_MACVLAN` -
	// so retrying it once per step would spawn a goroutine each time to learn
	// the same thing.
	off  atomic.Bool
	why  atomic.Pointer[string]
	once sync.Once

	// fromPool counts takes answered without building, which is the whole
	// claim this makes. A pool that is never read is setup cost with extra
	// steps, and nothing else would say so.
	fromPool atomic.Int64
}

// readyNet is a network built and not yet given to a step.
type readyNet struct {
	at   string
	done func()
}

// newStepNets returns a pool that keeps depth networks ahead of demand.
//
// **Depth zero is not depth one.** It clamped, so a run asked to build each
// network when the step asked for it got the pool anyway - and the A/B that was
// supposed to say whether any of this was worth doing compared the feature
// against itself and reported no difference.
func newStepNets(build func() (string, func(), string), depth int) *stepNets {
	if depth < 0 {
		depth = 0
	}

	return &stepNets{build: build, depth: depth, ready: make(chan readyNet, depth)}
}

// take gives a step its network, building one only if none is ready.
//
// The first call of a build pays in full - nothing has been built yet, and
// building ahead before anything asks would spend a veth, an address and two
// iptables rules on a build that may have no steps at all.
func (p *stepNets) take() (string, func(), string) {
	if p.off.Load() {
		if why := p.why.Load(); why != nil {
			return "", func() {}, *why
		}

		return "", func() {}, ""
	}

	if p.depth > 0 {
		select {
		case got := <-p.ready:
			p.fromPool.Add(1)
			p.fill()

			return got.at, got.done, ""
		default:
		}
	}

	at, done, why := p.build()
	if why != "" {
		p.stop(why)

		return "", done, why
	}

	p.fill()

	return at, done, ""
}

// fill builds towards depth, in the background.
//
// Bounded by the channel rather than by a counter: a send that would block is
// a pool that is already full, and dropping the network built for it is
// cheaper than the bookkeeping to avoid building it.
func (p *stepNets) fill() {
	for range p.depth - len(p.ready) {
		go func() {
			if p.off.Load() {
				return
			}

			at, done, why := p.build()
			if why != "" {
				p.stop(why)

				return
			}

			select {
			case p.ready <- readyNet{at: at, done: done}:
			default:
				done()
			}
		}()
	}
}

// stop records why this machine cannot give a step its own network, once.
func (p *stepNets) stop(why string) {
	p.once.Do(func() {
		p.why.Store(&why)
		p.off.Store(true)
	})
}

// drain destroys what was built and never used.
//
// **Because a namespace nobody used is still a veth, an address and two
// iptables rules on the machine.** In a microVM they go with the machine; on a
// host they do not, and the retry in openStepNet already exists because
// namespaces get left behind.
func (p *stepNets) drain() {
	p.off.Store(true)

	for {
		select {
		case got := <-p.ready:
			got.done()
		default:
			return
		}
	}
}

// EnvStepNetAhead is how many step networks the guest keeps built ahead of
// demand. Default: 1. `0` builds each one when the step asks, as this did
// before there was a pool.
//
// One rather than the parallelism, deliberately. The cost this removes is a
// step's wait, and a step only waits once: with one ready, a build whose steps
// are slower than 6ms never waits again. A deeper pool spends veths, addresses
// and iptables rules on networks a build may never reach - and the depth that
// would be right is the parallelism, which the guest does not know.
const EnvStepNetAhead = "EARTH_STEP_NET_AHEAD"

// defaultStepNetAhead is the depth when nothing says otherwise.
const defaultStepNetAhead = 1

// nets is this server's pool, made on first use.
//
// On the Server rather than a package variable, so two servers in one process -
// which the tests make - do not share a pool of namespaces built for one of
// them.
func (s *Server) nets() *stepNets {
	s.netsOnce.Do(func() {
		s.stepNets = newStepNets(openStepNet, stepNetAhead())
	})

	return s.stepNets
}

// stepNetAhead reads the depth, refusing nothing: a value nobody can parse is
// the default rather than a build that will not start.
func stepNetAhead() int {
	at := os.Getenv(EnvStepNetAhead)
	if at == "" {
		return defaultStepNetAhead
	}

	n, err := strconv.Atoi(at)
	if err != nil || n < 0 {
		fmt.Fprintf(os.Stderr, "earth-guestd: %s is %q, which is not a depth;"+
			" building each step's network when the step asks\n", EnvStepNetAhead, at)

		return 0
	}

	return n
}
