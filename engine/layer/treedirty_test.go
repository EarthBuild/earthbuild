package layer

import "testing"

// A path whose parent directories are not themselves entries dirties them all.
//
// **Unreachable through a manifest, and that is why it is tested here.**
// addImplicitDirs invents an entry for every ancestor, so every manifest names
// `a` alongside `a/b.txt` - and resyncing `a` dirties the root on its own. A
// fold that only dirtied a path's last component would therefore pass every
// test that goes through a manifest while being wrong, and would become wrong
// in fact the day a manifest source stops inventing those entries.
//
// Driven against the trie directly, which is the only place the input exists.
func TestAPathWhoseParentsAreNotEntriesDirtiesTheChain(t *testing.T) {
	t.Parallel()

	f := NewFold()

	f.merged["src/deep/main.go"] = entry{path: "src/deep/main.go", mode: 0o644}
	f.resync("src/deep/main.go")

	before := f.Digest()

	// The same path, changed, with nothing recorded for `src` or `src/deep`.
	f.merged["src/deep/main.go"] = entry{path: "src/deep/main.go", mode: 0o644, size: 99}
	f.resync("src/deep/main.go")

	if after := f.Digest(); after == before {
		t.Error("a change three directories down did not reach the root" +
			"\n  the directories on the way kept the names they had, so 𝜏 is a" +
			"\n  base that no longer exists and Κₜ would hit on it (I3)")
	}
}

// The same, for a path being removed rather than written.
func TestRemovingADeepPathDirtiesTheChain(t *testing.T) {
	t.Parallel()

	f := NewFold()

	f.merged["a/b/c.txt"] = entry{path: "a/b/c.txt", mode: 0o644}
	f.merged["a/b/d.txt"] = entry{path: "a/b/d.txt", mode: 0o644}
	f.resync("a/b/c.txt")
	f.resync("a/b/d.txt")

	before := f.Digest()

	delete(f.merged, "a/b/c.txt")
	f.resync("a/b/c.txt")

	if after := f.Digest(); after == before {
		t.Error("removing a file three directories down did not reach the root")
	}
}
