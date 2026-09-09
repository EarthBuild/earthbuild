package guest

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// builder is a stand-in for openStepNet: it counts, and it can be told to
// report the degrade a real one reports when the machine cannot support a
// private namespace.
type builder struct {
	mu     sync.Mutex
	made   int
	closed int
	why    string
}

func (b *builder) build() (string, func(), string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.why != "" {
		return "", func() {}, b.why
	}

	b.made++
	n := b.made

	return "netns-" + string(rune('0'+n)), func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		b.closed++
	}, ""
}

func (b *builder) counts() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.made, b.closed
}

// eventually waits for a condition a background goroutine brings about.
func eventually(t *testing.T, why string, cond func() bool) {
	t.Helper()

	for range 200 {
		if cond() {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatal(why)
}

// A step gets a network that was built before it asked.
//
// **The setup moves off the step's critical path, not off the step.** Every
// step still gets a namespace nobody has used - which is what makes it
// self-cleaning, and what stops one step's leftover listener colliding with the
// next (E519's background process, E923's port). What changes is when it is
// built: while the previous step is running, rather than while this one waits.
//
// Measured at ~6ms of a ~17ms step before this, on both backends.
func TestAStepGetsANetworkBuiltBeforeItAsked(t *testing.T) {
	t.Parallel()

	b := &builder{}
	p := newStepNets(b.build, 1)

	defer p.drain()

	// The first take pays for itself: nothing has been built yet.
	at, done, why := p.take()
	if why != "" || at == "" {
		t.Fatalf("the first take failed: %q %q", at, why)
	}

	done()

	// And it leaves one ready behind it.
	eventually(t, "no replacement was built after a take", func() bool {
		made, _ := b.counts()

		return made == 2
	})

	// The second take is answered from the pool rather than built.
	before, _ := b.counts()

	at, done, why = p.take()
	if why != "" || at == "" {
		t.Fatalf("the second take failed: %q %q", at, why)
	}

	done()

	if got := p.fromPool.Load(); got != 1 {
		t.Errorf("%d takes were answered from the pool, want 1"+
			"\n  a pool that is never read is setup cost with extra steps", got)
	}

	// The replacement is built behind the step, so this is what it settles at
	// rather than what it is the instant the take returns.
	eventually(t, "the take from the pool did not leave a replacement", func() bool {
		after, _ := b.counts()

		return after == before+1
	})
}

// A machine that cannot give a step its own network says so once.
//
// The degrade is systemic - no resolver, no `ip netns` - so a pool that kept
// retrying it would spawn a goroutine per step to learn the same thing.
func TestADegradeStopsThePool(t *testing.T) {
	t.Parallel()

	b := &builder{why: "this machine resolves through a loopback address only"}
	p := newStepNets(b.build, 1)

	defer p.drain()

	for range 3 {
		at, _, why := p.take()
		if at != "" || why != b.why {
			t.Fatalf("take = %q, %q; want the degrade to be passed through", at, why)
		}
	}

	if p.off.Load() != true {
		t.Error("the pool keeps trying a degrade that will not change")
	}
}

// Nothing the pool built and nobody used is left behind.
func TestDrainClosesWhatWasNeverUsed(t *testing.T) {
	t.Parallel()

	b := &builder{}
	p := newStepNets(b.build, 2)

	at, done, _ := p.take()
	if at == "" {
		t.Fatal("take failed")
	}

	done()

	eventually(t, "the pool never filled", func() bool {
		made, _ := b.counts()

		return made >= 3
	})

	p.drain()

	made, closed := b.counts()
	if closed != made {
		t.Errorf("%d built, %d closed: a namespace nobody used is a veth,"+
			" an address and two iptables rules left on the machine",
			made, closed)
	}
}

var _ = atomic.Bool{}

// Depth zero means no pool, which is what the setting says it means.
//
// **A control arm that is not a control measures nothing.** The first A/B of
// this feature compared `EARTH_STEP_NET_AHEAD=0` against `=1` and found no
// difference, because the constructor clamped a depth below one back up to one
// - so both arms had the pool and the run said the feature was worthless. The
// same shape as reading `-P` as parallelism: an arm that did not do what it
// was named after.
func TestDepthZeroBuildsEachNetworkWhenTheStepAsks(t *testing.T) {
	t.Parallel()

	b := &builder{}
	p := newStepNets(b.build, 0)

	defer p.drain()

	for range 3 {
		at, done, why := p.take()
		if why != "" || at == "" {
			t.Fatalf("take failed: %q %q", at, why)
		}

		done()
	}

	if got := p.fromPool.Load(); got != 0 {
		t.Errorf("%d takes came from a pool that was asked not to exist", got)
	}

	// Three taken, three built, and nothing built ahead of anything.
	eventually(t, "the pool built ahead when it was told not to", func() bool {
		made, _ := b.counts()

		return made == 3
	})

	if made, _ := b.counts(); made != 3 {
		t.Errorf("%d networks built for 3 steps, want 3", made)
	}
}
