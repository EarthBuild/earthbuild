package cli

import (
	"path/filepath"
	"strings"
)

// exportGroups decides which artifacts may be written at once.
//
// Returns groups of indices into dests. A group runs in sequence, in the order
// given; the groups themselves are independent and may run concurrently. The
// first index of each group is ascending, so the result is stable and a build
// writes the same way twice.
//
// **The question is not whether two destinations differ, but whether they are
// certainly different places.** Grouping by equality answered the first one:
// `out` and `out/sub/x` are different strings and the same tree, so writing
// them at once races - one export is removing a directory the other is filling.
// Two artifacts may go in parallel exactly when neither destination contains
// the other.
//
// **A path prefix, not a string prefix.** `out/ab` is a string prefix of
// `out/abc` and they are separate directories, so `strings.HasPrefix` alone
// serialises work that never needed it - and, worse, reads as if it had checked
// something.
//
// Containment is transitive, so the groups are the connected components of
// "contains or is contained by": `a/b/c`, `a` and `a/b` are one group even
// though the first and the last of those were given far apart.
//
// O(n²) in the number of artifacts, deliberately. A build with a thousand
// `SAVE ARTIFACT`s does not exist, and the alternative - sorting and merging
// runs - is where an off-by-one hides in a rule about which files get written.
func exportGroups(dests []string) [][]int {
	if len(dests) == 0 {
		return nil
	}

	// Union-find over "shares a place with", so containment can be discovered
	// in any order it is given.
	parent := make([]int, len(dests))
	for i := range parent {
		parent[i] = i
	}

	var find func(int) int

	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}

		return i
	}

	for i := range dests {
		for k := i + 1; k < len(dests); k++ {
			if !samePlace(dests[i], dests[k]) {
				continue
			}

			a, b := find(i), find(k)
			if a != b {
				parent[b] = a
			}
		}
	}

	var (
		groups [][]int
		at     = map[int]int{}
	)

	for i := range dests {
		root := find(i)

		g, seen := at[root]
		if !seen {
			at[root] = len(groups)
			groups = append(groups, []int{i})

			continue
		}

		groups[g] = append(groups[g], i)
	}

	return groups
}

// samePlace reports whether writing a and b could touch the same files.
//
// True when they are equal, and when either is a directory the other is inside.
// Cleaned first so `out/./a` and `out/a` are not mistaken for different places;
// not resolved through symlinks, which would need the filesystem and would still
// be a guess about a tree the build has not written yet.
func samePlace(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)

	return a == b || holds(a, b) || holds(b, a)
}

// holds reports whether the directory outer contains inner.
//
// The separator is the whole point: without it `out/ab` contains `out/abc`.
func holds(outer, inner string) bool {
	return strings.HasPrefix(inner, outer+string(filepath.Separator))
}
