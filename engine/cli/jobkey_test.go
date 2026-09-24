package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The adversarial suite of docs-internals/job-skipping.md, written before the
// mechanism. **A false skip is a green tick on a build that never ran**, so the
// cases that could produce one are the point of the file and everything else is
// scaffolding.

// contextLayer is the identity a context layer is filed under in these tests.
const contextLayer = "a-context-layer"

// placedAt is the copy this suite's fixtures all make: the context path `src`
// landing at `/w/src` inside the step.
func placedAt() []core.Placement {
	return []core.Placement{{Layer: contextLayer, From: "src", To: "/w/src"}}
}

// tree writes a fixture context and returns its root.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()

	for name, body := range files {
		at := filepath.Join(root, name)

		err := os.MkdirAll(filepath.Dir(at), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(at, []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	return root
}

// keyFor records what a build read, then derives the key against a checkout.
func keyFor(t *testing.T, root string, obs core.Observation) (string, error) {
	t.Helper()

	in, err := hostInputsFrom(map[string]bool{contextLayer: true}, placedAt(), obs, root)
	if err != nil {
		return "", err
	}

	return jobKey(ir.NodeID{'s', 'h', 'a', 'p', 'e'}, in), nil
}

// rekeyAfter re-derives the key from a record against a changed checkout.
func rekeyAfter(t *testing.T, root string, obs core.Observation, change func()) (string, string) {
	t.Helper()

	before, err := keyFor(t, root, obs)
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	change()

	after, err := keyFor(t, root, obs)
	if err != nil {
		t.Fatalf("re-derive: %v", err)
	}

	return before, after
}

// read is an observation of nothing but the paths named.
func read(paths ...string) core.Observation {
	obs := core.Observation{Reads: map[string]ir.NodeID{}, Listings: map[string]ir.NodeID{}}
	for _, p := range paths {
		obs.Reads[p] = ir.NodeID{}
	}

	return obs
}

// 1. The case the whole mechanism exists for: a README nobody opened.
func TestAFileNobodyReadDoesNotMoveTheKey(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/read.txt": "one", "src/README.md": "one"})

	before, after := rekeyAfter(t, root, read("/w/src/read.txt"), func() {
		err := os.WriteFile(filepath.Join(root, "src/README.md"), []byte("two"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	})

	if before != after {
		t.Error("a file nothing read moved the key")
	}
}

// 2. And the file a step did read moves it.
func TestAFileThatWasReadMovesTheKey(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/read.txt": "one"})

	before, after := rekeyAfter(t, root, read("/w/src/read.txt"), func() {
		err := os.WriteFile(filepath.Join(root, "src/read.txt"), []byte("two"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	})

	if before == after {
		t.Error("a file the build read changed and the key did not move")
	}
}

// 3. **A file appearing where a step enumerated.** A glob that would now match
// it changes what the step does, and no recorded read mentions the new file -
// the listing is the only thing that moves.
func TestANewFileInAnEnumeratedDirectoryMovesTheKey(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.rs": "one"})

	obs := read()
	obs.Listings["/w/src"] = ir.NodeID{}

	before, after := rekeyAfter(t, root, obs, func() {
		err := os.WriteFile(filepath.Join(root, "src/b.rs"), []byte("new"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	})

	if before == after {
		t.Error("a file appeared in a directory the build listed and the key did not move")
	}
}

// 4. A file a step looked for and did not find now exists.
func TestAFileThatWasAbsentAndNowExistsMovesTheKey(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.rs": "one"})

	obs := read()
	obs.Negative = []string{"/w/src/build.rs"}

	before, after := rekeyAfter(t, root, obs, func() {
		err := os.WriteFile(filepath.Join(root, "src/build.rs"), []byte("new"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	})

	if before == after {
		t.Error("a file the build found absent now exists and the key did not move")
	}
}

// 5. A file a step read was deleted.
func TestADeletedFileMovesTheKey(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/read.txt": "one"})

	before, after := rekeyAfter(t, root, read("/w/src/read.txt"), func() {
		err := os.Remove(filepath.Join(root, "src/read.txt"))
		if err != nil {
			t.Fatal(err)
		}
	})

	if before == after {
		t.Error("a file the build read was deleted and the key did not move")
	}
}

// 6. **Same bytes, different mode.** A script that stopped being executable
// runs differently, so a key over contents alone would skip a build that now
// fails. The digest is the entry's, not the file's contents.
func TestAModeChangeMovesTheKey(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/run.sh": "#!/bin/sh\n"})

	before, after := rekeyAfter(t, root, read("/w/src/run.sh"), func() {
		err := os.Chmod(filepath.Join(root, "src/run.sh"), 0o400)
		if err != nil {
			t.Fatal(err)
		}
	})

	if before == after {
		t.Error("a read file's mode changed and the key did not move")
	}
}

// 7. A symlink a step followed now points elsewhere.
func TestARepointedSymlinkMovesTheKey(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one", "src/b.txt": "two"})

	at := filepath.Join(root, "src/link")
	err := os.Symlink("a.txt", at)
	if err != nil {
		t.Fatal(err)
	}

	before, after := rekeyAfter(t, root, read("/w/src/link"), func() {
		err := os.Remove(at)
		if err != nil {
			t.Fatal(err)
		}

		err = os.Symlink("b.txt", at)
		if err != nil {
			t.Fatal(err)
		}
	})

	if before == after {
		t.Error("a symlink the build followed was repointed and the key did not move")
	}
}

// 11. **The tracer said it missed something.** L2 pays a miss for this; a job
// key pays a wrong skip, so it is refused outright.
func TestAnIncompleteObservationHasNoKey(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})

	obs := read("/w/src/a.txt")
	obs.Incomplete = true

	_, err := keyFor(t, root, obs)
	if err == nil {
		t.Error("an incomplete observation produced a key")
	}
}

// 12. A read that maps to no placement is not a host input and must not be
// quietly dropped where it cannot be explained at all.
func TestAReadOfAPathNoCopyPlacedIsNotAHostInput(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})

	// /etc/alpine-release is the base image's, covered by the pinned digest in
	// the shape rather than by a host input.
	in, err := hostInputsFrom(map[string]bool{contextLayer: true}, placedAt(),
		read("/w/src/a.txt", "/etc/alpine-release"), root)
	if err != nil {
		t.Fatal(err)
	}

	if len(in) != 1 || in[0].Path != "src/a.txt" {
		t.Errorf("host inputs are %v, want only the one the context placed", in)
	}
}

// A host input the checkout no longer has is not an error: it is a difference,
// and the key must move rather than the derivation fail.
func TestAMissingHostInputIsADifferenceNotAFailure(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{})

	_, err := keyFor(t, root, read("/w/src/gone.txt"))
	if err != nil {
		t.Errorf("a host path that is not there failed the derivation: %v", err)
	}
}

// **A directory's digest is the entry at it, not everything under it.**
//
// `watcher.list` states the rule: "a read of a directory digests the entry *at*
// it - its mode and ownership - which does not change when a file appears
// inside it; the listing is the only thing that does". Sealing the subtree
// instead makes every directory input as coarse as the whole context, so a file
// nobody read moves the key and nothing is ever skipped.
//
// Found end to end, not here: the first run recorded `/w` as an input, its
// digest covered every file under the copied tree, and the one case the
// mechanism exists for - a README nothing opened - rebuilt.
func TestADirectoryInputIsNotItsContents(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/read.txt": "one", "src/other.txt": "one"})

	was := sealOf(root, "src")

	err := os.WriteFile(filepath.Join(root, "src/other.txt"), []byte("two"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if sealOf(root, "src") != was {
		t.Error("a file changing inside a directory moved the directory's own digest")
	}

	// A file appearing inside it does not either - that is what the listing is
	// for, and the listing does move.
	listed := listingOf(root, "src")

	err = os.WriteFile(filepath.Join(root, "src/new.txt"), []byte("new"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if sealOf(root, "src") != was {
		t.Error("a file appearing inside a directory moved the directory's own digest")
	}

	if listingOf(root, "src") == listed {
		t.Error("a file appearing inside a directory did not move its listing")
	}

	// The directory's own mode is part of it, or two directories differing in
	// what they permit would look alike.
	//nolint:gosec // a directory mode, which gosec reads as a file's
	err = os.Chmod(filepath.Join(root, "src"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	if sealOf(root, "src") == was {
		t.Error("a directory's own mode changed and its digest did not")
	}
}

// **One copy with many sources, all landing in one directory.**
//
// Every other test here places a single tree. A real project does not:
// midnight-node's node build is `COPY --dir Cargo.lock Cargo.toml docs .sqlx
// ledger node pallets primitives metadata res runtime util tests relay
// partner-chains .` - fifteen sources, some files and some directories, all
// arriving side by side at the root.
//
// The rewrite matches the longest destination first, so the hazard is a
// placement whose destination is a prefix of another's: `/res` and `/runtime`
// share no prefix, but `/node` and `/node_modules` would, and `/` and anything
// always do.
func TestManyPlacementsIntoOneDirectory(t *testing.T) {
	t.Parallel()

	sources := []string{
		"Cargo.lock", "Cargo.toml", "docs", ".sqlx", "ledger", "node",
		"pallets", "primitives", "metadata", "res", "runtime", "util",
		"tests", "relay", "partner-chains",
	}

	files := map[string]string{}
	places := make([]core.Placement, 0, len(sources))

	for _, src := range sources {
		files[src+"/f.rs"] = "x"

		places = append(places, core.Placement{Layer: contextLayer, From: src, To: "/" + src})
	}

	root := tree(t, files)

	// A read from each, plus one that belongs to none of them.
	obs := read("/etc/alpine-release")
	for _, src := range sources {
		obs.Reads["/"+src+"/f.rs"] = ir.NodeID{}
	}

	got, err := hostInputsFrom(map[string]bool{contextLayer: true}, places, obs, root)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	if len(got) != len(sources) {
		t.Fatalf("mapped %d reads of %d, and dropped the base image's: %v",
			len(got), len(sources), got)
	}

	for i, in := range got {
		if !strings.HasSuffix(in.Path, "/f.rs") {
			t.Errorf("input %d is %q, which is not a path in the checkout", i, in.Path)
		}

		if in.Digest == gone.String() {
			t.Errorf("%s did not re-read", in.Path)
		}
	}
}

// **A destination that is a prefix of another must not steal its reads.**
//
// `COPY --dir node node_modules .` puts one at /node and the other at
// /node_modules. What prevents `/node` claiming the other's reads is the
// separator - the match is against `to + "/"` - and not the order placements
// are tried in: reversing that order leaves this passing, which is how the
// comment that used to be here was found to be attributing it to the wrong
// mechanism.
func TestAPlacementDoesNotStealAPrefixedSiblingsReads(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		"node/a.rs": "one", "node_modules/b.js": "two",
	})

	places := []core.Placement{
		{Layer: contextLayer, From: "node", To: "/node"},
		{Layer: contextLayer, From: "node_modules", To: "/node_modules"},
	}

	obs := read("/node_modules/b.js")

	got, err := hostInputsFrom(map[string]bool{contextLayer: true}, places, obs, root)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].Path != "node_modules/b.js" {
		t.Errorf("a read of /node_modules/b.js mapped to %v", got)
	}

	if got[0].Digest == gone.String() {
		t.Error("the mapped path does not exist in the checkout")
	}
}

// **And the order *is* load-bearing, for nested placements.**
//
// `COPY --dir src /w` beside `COPY --dir vendor /w/vendor` puts one inside the
// other, and both destinations match a read under the inner one. The longest
// destination has to win or every read of /w/vendor is named as though it came
// from src.
func TestANestedPlacementWinsOverTheOneAroundIt(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.rs": "one", "vendor/b.rs": "two"})

	places := []core.Placement{
		{Layer: contextLayer, From: "src", To: "/w"},
		{Layer: contextLayer, From: "vendor", To: "/w/vendor"},
	}

	got, err := hostInputsFrom(map[string]bool{contextLayer: true},
		places, read("/w/vendor/b.rs"), root)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].Path != "vendor/b.rs" {
		t.Fatalf("a read under the inner placement mapped to %v", got)
	}

	if got[0].Digest == gone.String() {
		t.Error("the mapped path does not exist in the checkout")
	}
}
