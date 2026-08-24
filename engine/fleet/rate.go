package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Rate is what this fleet has been observed to cost, in bytes and in steps.
//
// **The measurement `transferCost` was standing in for.** That constant priced
// a fetch at half a step-slot regardless of size, and said so: "a model rather
// than a measurement, and the honest thing about it is that it is one line to
// change when there is a measurement to change it to". E315 took the
// measurement - 1.6 MiB in 245ms against steps of 400ms - and the important
// part is not the number but that it **scales**. Half a step is about right for
// a 1.6 MiB base and absurd for a 500 MB one, which at the same rate is worth
// three hundred steps.
//
// A fleet that prices every base the same delegates work whose inputs cost more
// to ship than the work is worth. That is the failure the second attempt at this
// project reported - never faster than one machine, on a graph that was
// embarrassingly parallel - and it is the one thing a constant cannot express.
//
// Cumulative rather than windowed: a fleet's network does not change during a
// build, and a moving window would make placement depend on which steps happened
// to run recently, which is a build whose shape depends on its own timing.
type Rate struct {
	mu sync.Mutex

	bytes, transferMillis int64
	steps, stepMillis     int64
	// fetches is how many delegated steps moved anything. See Typical.
	fetches int64
	// least is the cheapest fetch observed, which is this fleet's fixed cost per
	// transfer. See Slots.
	least int64
}

// Observe records one delegated step: what it fetched, how long that took, and
// how long the step itself ran.
//
// A step that fetched nothing still says something - how long a step is worth -
// so it is counted for the second pair and not the first. Ignoring it would
// leave a warm fleet, where almost nothing is fetched, with no idea what a step
// costs at exactly the point placement matters most.
func (r *Rate) Observe(bytes, transferMillis, stepMillis int64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if bytes > 0 && transferMillis > 0 {
		r.bytes += bytes
		r.transferMillis += transferMillis
		r.fetches++

		// The cheapest fetch anybody has seen bounds what the next one can
		// cost: a transfer is a request, a round trip and an answer before it
		// is any bytes at all. See Slots.
		if r.least == 0 || transferMillis < r.least {
			r.least = transferMillis
		}
	}

	if stepMillis > 0 {
		r.steps++
		r.stepMillis += stepMillis
	}
}

// Slots is what fetching this many bytes is worth, in doubled step-slots.
//
// Doubled to match `preferFree`'s units, where a busy step counts two - which
// is what lets a cost of 1 mean "half a step" and be an integer.
//
// **`transferCost` whenever this cannot do better**, which is a fleet that has
// measured nothing and a base whose size nobody stated. A zero size means "not
// known", never "free": pricing an unknown base at nothing would make the
// cheapest machine the one with the most to fetch, which is not a degraded
// answer but an inverted one.
func (r *Rate) Slots(bytes int64) int {
	// **An unstated size is not a small one.** This guard was deleted in E317
	// as redundant with the floor below - mutation could remove it and no test
	// noticed, because the floor applied to everything and gave the same answer.
	//
	// It was carrying a distinction the floor could not: "nobody said" must
	// never be free, and "somebody said, and it is a kilobyte" may be. Flooring
	// both at half a step made every transfer cost something, so a driver
	// holding the inputs kept every step it was ever given (E343).
	if bytes <= 0 {
		return transferCost
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.bytes <= 0 || r.transferMillis <= 0 || r.steps <= 0 || r.stepMillis <= 0 {
		// An unmeasured fleet: the constant, which is what every build used
		// before there was anything better.
		return transferCost
	}

	// How long these bytes would take, in the same units as a step - and never
	// less than the cheapest transfer this fleet has managed.
	//
	// **A transfer costs something before it has moved a byte.** This was purely
	// proportional, so a small layer was free and spreading a level of work over
	// every machine looked costless - and two workers beat four on a
	// level-shaped build, eight fetches against sixteen, because each machine
	// added to a level adds a fetch (E346). A fetch of a four-kilobyte layer was
	// measured in the hundreds of milliseconds, almost none of it the bytes.
	//
	// Measured rather than assumed: the least any fetch has taken is a bound on
	// what the next one cannot beat.
	millis := bytes * r.transferMillis / r.bytes

	// **Two fetches at least.** The minimum of one observation is that
	// observation, so a fleet that has fetched a megabyte once would price a
	// kilobyte at what the megabyte cost - a fixed cost inferred from a sample
	// with nothing to be fixed against.
	if r.fetches >= 2 {
		millis = max(millis, r.least)
	}
	step := r.stepMillis / r.steps

	if step <= 0 {
		return transferCost
	}

	// Rounded up, and **not floored**.
	//
	// A base of a kilobyte on a fast link genuinely costs nothing worth
	// counting, and pricing it at half a step made a driver that held the
	// inputs keep every step it was given - the fleet switched off by a
	// rounding rule (E343). The case the floor existed for, a size nobody
	// stated, is refused above where it belongs.
	return int((2*millis + step - 1) / step)
}

// Measured reports whether anything has been observed yet.
func (r *Rate) Measured() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.bytes > 0 && r.transferMillis > 0 && r.steps > 0 && r.stepMillis > 0
}

// Typical is what a delegated step that fetched anything actually moved.
//
// **What a prediction is worth, measured rather than assumed.** `Hints.Bytes` is
// the size of a step's inputs, which is what crosses when a worker fetches whole
// layers - and with a prediction it fetches about a hundredth of that. The
// driver went on pricing every predicted step as though the whole base would
// move, and kept work it should have shipped: at four workers a 16 MB base moved
// 1.1 MiB in total while the decision was made against 16 MB a step (E326).
//
// Zero when nothing has been fetched, which is **not** "free": the caller falls
// back to the stated size, because pricing a transfer at nothing sends work to
// whichever machine has the most to move (E317).
func (r *Rate) Typical() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.fetches <= 0 {
		return 0
	}

	return r.bytes / r.fetches
}

// kept is a rate as it survives a process.
//
// A plain struct rather than the type itself: `Rate` has a lock, and a lock is
// not something to serialise. The fields are what `Slots` reads and nothing
// else - a snapshot that carried more would be a promise about what the
// arithmetic uses.
type kept struct {
	Bytes          int64 `json:"bytes"`
	TransferMillis int64 `json:"transferMillis"`
	Steps          int64 `json:"steps"`
	StepMillis     int64 `json:"stepMillis"`
	Fetches        int64 `json:"fetches"`
	Least          int64 `json:"least"`
}

// Save writes what this fleet has been measured to cost.
//
// **Every real build is round one** (E350). A build that has measured nothing
// prices a transfer at zero, delegates everything, and keeps nothing - 1.447s
// against 1.084s for the same work once it knows. Each invocation is a fresh
// process, so that knowledge is earned and discarded, over and over.
//
// The engine already keeps what a step read last time for exactly this reason
// (§4.6). What a fleet costs is the same kind of fact (E351).
func (r *Rate) Save(at string) error {
	r.mu.Lock()
	k := kept{
		Bytes: r.bytes, TransferMillis: r.transferMillis,
		Steps: r.steps, StepMillis: r.stepMillis,
		Fetches: r.fetches, Least: r.least,
	}
	r.mu.Unlock()

	if k.Fetches == 0 && k.Steps == 0 {
		// Nothing was learnt, so there is nothing to keep - and writing an
		// empty file would replace what an earlier build did learn.
		return nil
	}

	b, err := json.Marshal(k)
	if err != nil {
		return fmt.Errorf("record what this fleet costs: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
		return fmt.Errorf("record what this fleet costs: %w", err)
	}

	err = os.WriteFile(at, b, 0o600)
	if err != nil {
		return fmt.Errorf("record what this fleet costs: %w", err)
	}

	return nil
}

// Load reads what an earlier build measured, and is never an error.
//
// A missing file is the first build on a machine; a damaged one is a cache of
// something measurable, and the answer to both is to measure again. Refusing a
// build over either would make an optimisation load-bearing (I5, I11).
func (r *Rate) Load(at string) error {
	b, err := os.ReadFile(at) //nolint:gosec // a path this engine composed
	if err != nil {
		return nil
	}

	var k kept

	if json.Unmarshal(b, &k) != nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.bytes, r.transferMillis = k.Bytes, k.TransferMillis
	r.steps, r.stepMillis = k.Steps, k.StepMillis
	r.fetches, r.least = k.Fetches, k.Least

	return nil
}
