package fleet

import (
	"fmt"
	"sync"
	"time"
)

// Spend is where a fleet build's wall-clock went.
//
// The reason this exists at all: a distributed build that is no faster than one
// machine is a common result and an uninformative one. A step on a worker costs
// `transfer + wait + compute` where a step at home costs `compute`, so a fleet
// wins only when the transfer is amortised over many steps and loses whenever it
// is paid per step - and a total that does not separate the three cannot tell
// those apart.
type Spend struct {
	// Delegated and Local are how many steps went each way.
	Delegated int
	Local     int
	// Fetched is how many bytes workers had to move before they could start.
	Fetched int64
	// Fetching is how long they spent moving them, as they measured it.
	Fetching time.Duration
	// Computing is how long the steps themselves took, as they measured it.
	Computing time.Duration
	// Fetches is how many delegated steps moved anything, and Slowest is the
	// longest single one.
	//
	// **A total is not a distribution.** "transfer 2.9s" is one slow fetch or
	// twenty ordinary ones, and those are different problems: a peer or a layer
	// to look at, against a per-fetch cost to remove. Three experiments were
	// spent narrowing a total by subtraction when a count would have said which
	// (E335, E336, E341).
	Fetches int
	Slowest time.Duration
	// Queueing is how long steps waited for a slot on a worker, as they measured
	// it.
	//
	// **Not waste.** A worker with more steps than slots is a worker being used,
	// and this is the number that says so - where before it was indistinguishable
	// from the network, and the two mean opposite things about whether adding
	// machines would help (E336).
	Queueing time.Duration
	// Overhead is the round trip this machine measured, less what the workers
	// admitted to.
	//
	// **The part nobody else can see.** A worker's own numbers cover what it
	// did; they cannot cover a control message queued behind another, a
	// connection being opened, or a step that had not been placed yet. That gap
	// is exactly the symptom of "embarrassingly parallel and yet no faster".
	Overhead time.Duration
}

// Report says where the time went, and names it.
//
// A total without a cause is a number nobody can act on. Transfer-bound,
// overhead-bound and compute-bound are three different problems: the first wants
// peers serving each other rather than a star through the driver, the second
// wants overlap and batching, and the third means the fleet is doing its job and
// the answer is more machines.
//
// A build that delegated nothing names no bottleneck. Zero of everything is not
// evidence of compute, and a report that concluded one from it would be
// inventing it.
func (s Spend) Report() string {
	if s.Delegated == 0 {
		return fmt.Sprintf("fleet: nothing was delegated (%d step(s) ran here)", s.Local)
	}

	total := s.Fetching + s.Computing + s.Queueing + s.Overhead
	if total == 0 {
		return fmt.Sprintf("fleet: %d step(s) delegated, %d here;"+
			" no time was accounted for", s.Delegated, s.Local)
	}

	which, share := "compute", s.Computing
	if s.Fetching > share {
		which, share = "transfer", s.Fetching
	}

	if s.Queueing > share {
		which, share = "queue", s.Queueing
	}

	if s.Overhead > share {
		which, share = "overhead", s.Overhead
	}

	return fmt.Sprintf(
		"fleet: %d step(s) delegated, %d here; %s-bound (%d%%)"+
			"\n  transfer %v for %s in %d fetch(es), slowest %v"+
			"\n  compute %v · queue %v · wire %v",
		s.Delegated, s.Local, which, (share*100)/total,
		s.Fetching.Round(time.Millisecond), readableSize(s.Fetched),
		s.Fetches, s.Slowest.Round(time.Millisecond),
		s.Computing.Round(time.Millisecond), s.Queueing.Round(time.Millisecond),
		s.Overhead.Round(time.Millisecond))
}

// readableSize is a size a person can read at a glance.
func readableSize(n int64) string {
	const unit = 1 << 10

	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := int64(unit), 0
	for n/div >= unit && exp < 3 {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

// account accumulates a Spend across concurrent steps.
type account struct {
	mu sync.Mutex
	s  Spend
}

// delegated records one step that went to a worker.
//
// `round` is what this machine measured; the rest is what the worker claimed.
// Negative claims are floored at zero rather than refused: they cannot change a
// build, and a report is not the place to fail one (A5, I5).
func (a *account) delegated(round time.Duration, r Reply) {
	fetch := millis(r.FetchMillis)
	compute := millis(r.DurationMillis)
	queue := millis(r.QueueMillis)

	over := round - fetch - compute - queue
	// A worker claiming more than the round trip took is reporting its clock
	// rather than this one's, so the overhead is nothing rather than negative.
	over = max(over, 0)

	a.mu.Lock()
	defer a.mu.Unlock()

	a.s.Delegated++
	a.s.Fetching += fetch
	a.s.Computing += compute
	a.s.Queueing += queue
	a.s.Overhead += over

	if r.FetchedBytes > 0 {
		a.s.Fetched += r.FetchedBytes
		a.s.Fetches++

		if fetch > a.s.Slowest {
			a.s.Slowest = fetch
		}
	}
}

// local records one step that ran here.
func (a *account) local() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.s.Local++
}

func (a *account) spend() Spend {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.s
}

// millis turns a worker's claim into a duration, refusing to go backwards.
func millis(n int64) time.Duration {
	if n <= 0 {
		return 0
	}

	return time.Duration(n) * time.Millisecond
}

// Since is what has been spent since an earlier reading.
//
// **A total cannot say what one round cost**, and the fleet's wall clock varies
// by half while a single machine's varies by six parts in a thousand (E349). The
// difference of two readings is a round, which is the unit the question is
// about.
//
// `Slowest` is **not** subtracted: it is a record rather than a sum, and taking
// one from another would report a negative worst transfer whenever the record
// stood from an earlier round. It is recomputed from what this round did, which
// this type cannot know - so it carries the later reading's value and says so.
func (s Spend) Since(was Spend) Spend {
	return Spend{
		Delegated: s.Delegated - was.Delegated,
		Local:     s.Local - was.Local,
		Fetched:   s.Fetched - was.Fetched,
		Fetches:   s.Fetches - was.Fetches,
		Fetching:  s.Fetching - was.Fetching,
		Computing: s.Computing - was.Computing,
		Queueing:  s.Queueing - was.Queueing,
		Overhead:  s.Overhead - was.Overhead,
		Slowest:   s.Slowest,
	}
}
