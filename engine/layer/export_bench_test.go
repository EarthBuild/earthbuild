package layer

import (
	"sort"
	"testing"
)

// DecodeForBench exposes manifest decoding to the package's benchmarks, which
// need the cost of the parse apart from the cost of the fold around it.
func DecodeForBench(tb testing.TB, m []byte) int {
	es, err := decodeManifest(m)
	if err != nil {
		tb.Fatal(err)
	}

	return len(es)
}

// SortCostForBench is the sort inside Digest, apart from the hashing.
//
// Digest sorts every path on every call, so a rolling fold that stopped
// re-applying the base still re-sorts it once per step above.
func SortCostForBench(f *Fold) int {
	paths := make([]string, 0, len(f.merged))
	for p := range f.merged {
		paths = append(paths, p)
	}

	sort.Strings(paths)

	return len(paths)
}
