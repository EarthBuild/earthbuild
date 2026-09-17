package helper

import "sort"

// Needed is which keys an export must read, and what last time's map still says.
//
// **An export otherwise re-reads the whole cache to learn what it already knew.**
// A warm Go build cache holds 88,000 units and a step touches a hundred of them;
// framing and hashing the other 87,900 produces exactly the digests the last map
// already records. They dedupe in 𝔅, being the same bytes under the same names,
// so the cost is work rather than space - the kind of waste that never announces
// itself.
//
// `immutable` is the helper's claim that a key's unit never changes content, and
// it has to be the helper's because **npm is a counter-example**: a cacache
// bucket is append-only and holds several records, so a key present in both
// indexes may have gained one. A key set that compares equal is then a cache
// that has changed, and skipping on that basis would file a map naming last
// build's bytes for a unit that has grown. Where the claim is absent, everything
// is exported, which is what this did before the claim existed.
//
// The result is bounded by the index in both directions: a key here and not in
// the last map is exported, and a key in the last map and no longer here is
// dropped. Without the second half the map would grow monotonically and never
// forget, so a tool that prunes its own cache would leave this naming units
// nobody can serve.
func Needed(prev Map, index []string, immutable bool) (want []string, keep Map) {
	if !immutable || len(prev) == 0 {
		want = append(want, index...)
		sort.Strings(want)

		return want, Map{}
	}

	keep = make(Map, len(prev))

	for _, k := range index {
		if id, named := prev[k]; named {
			keep[k] = id

			continue
		}

		want = append(want, k)
	}

	sort.Strings(want)

	return want, keep
}
