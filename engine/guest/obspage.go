package guest

import (
	"sort"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// pageBudget is how many bytes of entries one observe reply may carry.
//
// Half the frame, so that the fixed part of a `Response` and JSON's own
// punctuation cannot push a page over the limit that the budget was chosen to
// respect. Halving costs a round trip on a very large observation and removes a
// whole class of "just under, then just over" arithmetic.
const pageBudget = maxMessage / 2

// entryOverhead is what one entry costs beyond its own bytes.
//
// Quotes, a colon, a comma, and the hex of a digest. Approximate on purpose: it
// exists to keep the estimate above the truth, and an estimate that runs low is
// the only kind that matters here.
const entryOverhead = 80

// observationPage renders one page of an observation, from the given entry.
//
// **A step whose observation does not fit in a frame loses the second cache
// tier**, and it is the largest steps that do not fit: this repository's own
// `+unit-test` produces 19.58 MB against a 16 MiB limit, and was told "nothing
// observed this step" for it (E620). Measured before this was written, because a
// tier restored where it never hits would be worth nothing: a step with a 16 MB
// observation hits L2 and skips a twenty-second body (E621).
//
// **Ordered, because two runs of one build must not key differently.** The
// entries are sorted before they are cut into pages, so which bucket Go walked
// first cannot reach the wire - the same argument green paper (4.6) makes about
// the observed key itself.
//
// Returns the page, the index to ask for next, and whether there is more. A
// caller that ignores `more` gets a prefix rather than a lie: the page is honest
// about what it contains, and `Incomplete` still travels on every one of them.
func observationPage(obs core.Observation, from int) (Response, int, bool) {
	entries := flatten(obs)

	resp := Response{
		Reads:      map[string]string{},
		Listings:   map[string]string{},
		Incomplete: obs.Incomplete,
		Why:        obs.Why,
	}

	if from < 0 {
		from = 0
	}

	spent := 0
	i := from

	for ; i < len(entries); i++ {
		e := entries[i]

		// At least one entry per page, whatever it costs: a page that refuses
		// everything cannot make progress, and a single path longer than the
		// budget would otherwise loop for ever.
		if spent > 0 && spent+len(e.path)+entryOverhead > pageBudget {
			break
		}

		switch e.kind {
		case entryRead:
			resp.Reads[e.path] = e.digest
		case entryListing:
			resp.Listings[e.path] = e.digest
		case entryNegative:
			resp.Negative = append(resp.Negative, e.path)
		}

		spent += len(e.path) + entryOverhead
	}

	return resp, i, i < len(entries)
}

// The three things an observation records, in the order they are paged.
const (
	entryRead = iota
	entryListing
	entryNegative
)

type entry struct {
	kind   int
	path   string
	digest string
}

// flatten is the observation as one deterministic sequence.
//
// Sorted within each kind and the kinds in a fixed order, so an index means the
// same entry on every call - which is what lets a caller ask for "from 40000"
// and get the rest rather than a different slice of the same set.
func flatten(obs core.Observation) []entry {
	out := make([]entry, 0, len(obs.Reads)+len(obs.Listings)+len(obs.Negative))

	out = appendSorted(out, obs.Reads, entryRead)
	out = appendSorted(out, obs.Listings, entryListing)

	neg := append([]string(nil), obs.Negative...)
	sort.Strings(neg)

	for _, p := range neg {
		out = append(out, entry{kind: entryNegative, path: p})
	}

	return out
}

func appendSorted(out []entry, m map[string]core.Key, kind int) []entry {
	paths := make([]string, 0, len(m))
	for p := range m {
		paths = append(paths, p)
	}

	sort.Strings(paths)

	for _, p := range paths {
		d := m[p]
		out = append(out, entry{kind: kind, path: p, digest: d.String()})
	}

	return out
}

// estimate is roughly what a response will cost on the wire.
//
// Used by the test that asserts a page fits. Deliberately the same arithmetic
// the pager budgets with, so the two cannot disagree about what "too big" means.
func estimate(r Response) int {
	n := 0

	for p := range r.Reads {
		n += len(p) + entryOverhead
	}

	for p := range r.Listings {
		n += len(p) + entryOverhead
	}

	for _, p := range r.Negative {
		n += len(p) + entryOverhead
	}

	return n
}
