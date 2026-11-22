package core

import (
	"maps"
	"sort"
	"sync"
)

// Predictions records which way a condition went, per site, across builds.
//
// A site is *where the condition is written* - its source location and its
// text - and not the identity of the filesystem it was asked about. The probe
// that answers a condition stands on everything built before it, so its
// identity changes with almost any commit; keyed on that, history would be
// discarded exactly when someone is iterating, which is when it is worth having.
//
// It exists for the conditions the interpreter *cannot* decide - `IF command -v
// unbuffer`, anything needing a filesystem. Those force the graph to be
// discovered while it runs, which costs the engine its best property: work that
// could have started immediately waits for a condition to be evaluated first.
//
// A prediction lets that work start anyway. The predicted branch is planned and
// scheduled speculatively; when the condition actually runs, a correct
// prediction has already done the work and a wrong one has wasted some.
//
// **It is a hint, and hints may not change results (green paper I5).** The
// branch a build *takes* is decided by running the condition, never by the
// prediction. That is the whole safety argument: a mispredicted branch costs
// time, exactly as a missing cache entry does, and a build with prediction
// disabled produces the same artefacts as one with it enabled.
type Predictions struct {
	mu    sync.Mutex
	taken map[string][2]int // site -> [false count, true count]
	// needs is what each branch of each site went on to require, keyed by
	// needKey so the two branches of one condition stay apart.
	needs map[string][]string
	// idle counts consecutive consultations in which each entry was not
	// wanted. Green paper A.3: extension alone is a ratchet, so an entry is
	// dropped after MaskIdleLimit unused consultations.
	idle map[string]map[string]int
}

// MaskIdleLimit is 𝑁 in green paper A.3: how many consecutive consultations an
// entry may go unwanted before it is dropped from a prefetch mask.
//
// A consultation is a build. Three rather than one, because a branch that
// occasionally takes a shortcut would otherwise discard a mask that is right
// almost always and pay a full cold fetch next time; and three rather than
// thirty, because the cost of keeping a stale entry is a whole image pulled
// before every build, and a base-image bump should stop costing that within a
// working day rather than within a month.
const MaskIdleLimit = 3

// NewPredictions returns an empty predictor.
func NewPredictions() *Predictions {
	return &Predictions{taken: map[string][2]int{}}
}

// Observe records which branch a condition actually took.
func (p *Predictions) Observe(site string, branch bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	counts := p.taken[site]
	if branch {
		counts[1]++
	} else {
		counts[0]++
	}

	p.taken[site] = counts
}

// Predict guesses which branch a condition will take, and whether there is
// enough history to guess at all.
//
// Reports its own ignorance rather than defaulting to true. A site seen once is
// not a pattern, and speculating on no evidence spends the build's parallelism
// on a coin toss - the cost lands on exactly the builds that have no history to
// learn from, which are the ones a new user runs.
func (p *Predictions) Predict(site string) (branch, confident bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	counts := p.taken[site]

	total := counts[0] + counts[1]
	if total < 2 {
		return false, false
	}

	// Confident when the site has been consistent. A condition that alternates
	// is not predictable, and speculating on it wastes half the work it does.
	if counts[1] > counts[0] {
		return true, counts[1]*4 >= total*3
	}

	return false, counts[0]*4 >= total*3
}

// TakeBranch decides a conditional and records what happened.
//
// The condition is *always* evaluated: the predictor is consulted for what to
// speculate on, never for what to do. Separating the two is what keeps a stale
// statistic from becoming a wrong build.
//
// **Production does not call this**, and the reason is worth stating rather
// than leaving to be rediscovered. The shape here makes the invariant
// structural - a caller cannot record without evaluating, because evaluating is
// the argument - and the one place that decides a conditional evaluates by
// *running a step*, which can fail: `decideByRunning` returns a result and an
// error, and neither fits `func() bool`. So `engine/cli` evaluates, then calls
// `recordBranch` with what it got.
//
// That is the invariant held by discipline where a shape was available, which
// is the weaker arrangement. It is held, and it is now asserted end to end
// rather than argued: `TestAConfidentlyWrongPredictionDoesNotDecideTheBranch`
// seeds a history that is confident and wrong, and watches the build take the
// other branch (E131).
func TakeBranch(p *Predictions, site string, evaluate func() bool) bool {
	taken := evaluate()

	if p != nil {
		p.Observe(site, taken)
	}

	return taken
}

// Needed records what a branch went on to require.
//
// Images, because they are the expensive part of reaching a branch: a pull is
// network-bound and nothing else proceeds during it. Knowing them before the
// condition has been run is what lets the bytes move early.
//
// Over-inclusive on purpose. What is recorded is everything the build used, not
// a precise subtree, because attributing images to a branch exactly would need
// bookkeeping the interpreter has no other reason to carry - and being wrong in
// this direction costs bandwidth, which is the whole reason this tier is called
// free.
func (p *Predictions) Needed(site string, branch bool, refs []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.needs == nil {
		p.needs = map[string][]string{}
	}

	if p.idle == nil {
		p.idle = map[string]map[string]int{}
	}

	key := needKey(site, branch)

	wanted := make(map[string]bool, len(refs))
	for _, r := range refs {
		wanted[r] = true
	}

	counts := p.idle[key]
	if counts == nil {
		counts = map[string]int{}
	}

	// Union on consultation, and drop what has gone unwanted for long enough.
	// Extension alone converges on every image the project has ever used
	// (green paper A.3), which §A.4 names as degeneration into eager transfer.
	merged := make([]string, 0, len(p.needs[key])+len(refs))

	for _, r := range p.needs[key] {
		if wanted[r] {
			counts[r] = 0
			merged = append(merged, r)

			continue
		}

		counts[r]++

		if counts[r] >= MaskIdleLimit {
			delete(counts, r)

			continue
		}

		merged = append(merged, r)
	}

	seen := make(map[string]bool, len(merged))
	for _, r := range merged {
		seen[r] = true
	}

	for _, r := range refs {
		if !seen[r] {
			merged = append(merged, r)
			seen[r] = true
			counts[r] = 0
		}
	}

	sort.Strings(merged)

	p.needs[key] = merged
	p.idle[key] = counts
}

// IdleSnapshot copies the unused-consultation counts, for something outside to
// write down.
//
// Separate from NeedsSnapshot because they are separately persisted and because
// a store written before this existed has no counts - which decodes as none,
// and means nothing is dropped early rather than everything at once.
//
// **The counts must survive the process.** A consultation is a build, so a
// count that reset on exit would never reach the limit and the ratchet would
// have no release - while every unit test passed.
func (p *Predictions) IdleSnapshot() map[string]map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make(map[string]map[string]int, len(p.idle))

	for k, v := range p.idle {
		out[k] = maps.Clone(v)
	}

	return out
}

// RestoreIdle loads the counts earlier builds recorded.
func (p *Predictions) RestoreIdle(idle map[string]map[string]int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.idle = make(map[string]map[string]int, len(idle))

	for k, v := range idle {
		p.idle[k] = maps.Clone(v)
	}
}

// Needs reports what the given branch of a site required last time.
func (p *Predictions) Needs(site string, branch bool) []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]string{}, p.needs[needKey(site, branch)]...)
}

// Sites lists every condition with any history.
func (p *Predictions) Sites() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]string, 0, len(p.taken))
	for k := range p.taken {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

// needKey distinguishes the two branches of one site.
func needKey(site string, branch bool) string {
	if branch {
		return site + "\x00true"
	}

	return site + "\x00false"
}

// NeedsSnapshot copies what each branch required, for something outside to
// store.
func (p *Predictions) NeedsSnapshot() map[string][]string {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make(map[string][]string, len(p.needs))
	for k, v := range p.needs {
		out[k] = append([]string{}, v...)
	}

	return out
}

// RestoreNeeds loads what earlier builds recorded.
func (p *Predictions) RestoreNeeds(needs map[string][]string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.needs == nil {
		p.needs = map[string][]string{}
	}

	for k, v := range needs {
		p.needs[k] = append([]string{}, v...)
	}
}

// Snapshot copies what has been learned, for something outside to store.
//
// core reaches the outside through ports and never through the filesystem, so
// persistence is not its business: it holds the arithmetic and hands the
// numbers over. The layer that owns files decides where they live.
func (p *Predictions) Snapshot() map[string][2]int {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make(map[string][2]int, len(p.taken))
	maps.Copy(out, p.taken)

	return out
}

// Restore loads what an earlier build learned.
func (p *Predictions) Restore(counts map[string][2]int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	maps.Copy(p.taken, counts)
}
