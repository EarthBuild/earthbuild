package exec_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// layerAt writes files into a layer of a store and returns its id.
func layerAt(t *testing.T, store, name string, files map[string]string) ir.NodeID {
	t.Helper()

	id := (&ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{name}}}).ID()

	root := filepath.Join(store, "layers", id.String())

	for rel, body := range files {
		p := filepath.Join(root, rel)

		err := os.MkdirAll(filepath.Dir(p), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(p, []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := os.MkdirAll(root, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	return id
}

func viewOf(t *testing.T, store string, stack ...ir.NodeID) interface {
	Digest(string) (ir.NodeID, bool)
	ListingDigest(string) (ir.NodeID, bool)
} {
	t.Helper()

	v, err := exec.LayerStore(store).View(context.Background(), stack)
	if err != nil {
		t.Fatal(err)
	}

	return v
}

// A view answers what the merged stack holds, without mounting it.
//
// `ViewSource` has been declared since S1 and implemented by nothing outside a
// test fake, so the entire L2 path - profiles, `Consistent`, Κ₂ - has never run
// against a real filesystem. That is the shape flattening was in before E49:
// carried, recorded and keyed for months without once running, and wrong when
// it finally did.
//
// The contract is a cost as much as an answer. Verifying a prediction must touch
// only the paths the prediction names; a view that materialised the stack would
// cost the mount L2 exists to avoid, and L2 would be slower than the rebuild it
// replaces. So this walks the stack per path, newest first.
func TestAViewAnswersFromTheStackWithoutMounting(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	base := layerAt(t, store, "base", map[string]string{
		"etc/hosts": "127.0.0.1\n",
		testTool:    "old\n",
	})
	over := layerAt(t, store, "over", map[string]string{
		testTool: "new\n",
	})

	t.Run("the newest layer wins", func(t *testing.T) {
		t.Parallel()

		got, ok := viewOf(t, store, base, over).Digest("/usr/tool")
		if !ok {
			t.Fatal("a path present in the top layer reported absent")
		}

		want, err := layer.PathDigest(
			filepath.Join(store, "layers", over.String(), "usr", "tool"))
		if err != nil {
			t.Fatal(err)
		}

		if got != want {
			t.Error("the view returned the layer underneath, so a step keyed on" +
				" the new file would verify against the old one")
		}
	})

	t.Run("a path only lower down is found", func(t *testing.T) {
		t.Parallel()

		_, ok := viewOf(t, store, base, over).Digest("/etc/hosts")
		if !ok {
			t.Error("a path present only in the base reported absent")
		}
	})

	t.Run("a path in no layer is absent", func(t *testing.T) {
		t.Parallel()

		_, ok := viewOf(t, store, base, over).Digest("/nothing/here")
		if ok {
			t.Error("a path nothing holds reported present")
		}
	})

	// The one that decides correctness. 𝑁 records that a path was *absent*, and
	// `Consistent` rejects a base where it now exists. The mirror is a path a
	// higher layer deleted: a view walking layers newest-first and returning the
	// first hit would report the deleted file as present, with the *lower*
	// layer's content - and a step keyed on reading it would verify against a
	// base where it is gone.
	t.Run("a deletion in a higher layer hides the file", func(t *testing.T) {
		t.Parallel()

		deleted := layerAt(t, store, "deleted", map[string]string{
			".wh.etc-marker": "",
		})

		// The engine's own marker convention: `.wh.<name>` beside where <name>
		// would be. Written properly so the test is about the view, not about
		// the fixture.
		root := filepath.Join(store, "layers", deleted.String(), "usr")

		err := os.MkdirAll(root, 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(root, ".wh.tool"), nil, 0o600)
		if err != nil {
			t.Fatal(err)
		}

		_, ok := viewOf(t, store, base, over, deleted).Digest("/usr/tool")
		if ok {
			t.Error("a file deleted by a higher layer reported present:" +
				"\n  a step that observed it absent would verify against a base holding it")
		}
	})

	t.Run("a whiteout marker is not itself a path", func(t *testing.T) {
		t.Parallel()

		deleted := layerAt(t, store, "deleted2", map[string]string{})

		root := filepath.Join(store, "layers", deleted.String(), "usr")

		err := os.MkdirAll(root, 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(root, ".wh.tool"), nil, 0o600)
		if err != nil {
			t.Fatal(err)
		}

		_, ok := viewOf(t, store, base, deleted).Digest("/usr/.wh.tool")
		if ok {
			t.Error("the deletion marker is visible as a file in the merged view")
		}
	})
}

// A directory's listing digest changes when its contents do.
//
// 𝐷 exists so that one entry can stand for every negative lookup inside a
// directory: *"if the listing digest is unchanged, every absent path in it is
// still absent"* (§3.4). That claim is only true if adding a file changes the
// digest, so the subsumption is a property of this function and not of the
// specification's prose.
func TestAListingDigestSubsumesWhatIsAbsentFromIt(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	base := layerAt(t, store, "l-base", map[string]string{"inc/a.h": "a\n"})

	first, ok := viewOf(t, store, base).ListingDigest("/inc")
	if !ok {
		t.Fatal("a directory that exists reported absent")
	}

	t.Run("a file added in a higher layer changes it", func(t *testing.T) {
		t.Parallel()

		added := layerAt(t, store, "l-added", map[string]string{testHeader: "b\n"})

		got, ok := viewOf(t, store, base, added).ListingDigest("/inc")
		if !ok {
			t.Fatal("the directory reported absent once a layer added to it")
		}

		if got == first {
			t.Error("a header appearing in an include directory did not change" +
				" its listing digest, so 𝐷 does not subsume 𝑁 and a compiler's" +
				" probe would verify against a base where the header now exists")
		}
	})

	t.Run("a directory in no layer is absent", func(t *testing.T) {
		t.Parallel()

		_, ok := viewOf(t, store, base).ListingDigest("/no/such/dir")
		if ok {
			t.Error("a directory nothing holds reported present")
		}
	})

	t.Run("the same contents digest the same however layered", func(t *testing.T) {
		t.Parallel()

		// One layer holding both, versus two layers holding one each. The
		// merged view is identical, so the digest must be - or a step's
		// prediction would fail against a base assembled differently, which is
		// exactly what a base-image bump does.
		both := layerAt(t, store, "l-both", map[string]string{
			"inc/a.h": "a\n", testHeader: "b\n",
		})
		split := layerAt(t, store, "l-split", map[string]string{testHeader: "b\n"})

		one, ok1 := viewOf(t, store, both).ListingDigest("/inc")
		two, ok2 := viewOf(t, store, base, split).ListingDigest("/inc")

		if !ok1 || !ok2 {
			t.Fatal("one of the two arrangements reported the directory absent")
		}

		if one != two {
			t.Error("the same merged directory digests differently depending on" +
				" how it was layered, so L2 never hits across a base change -" +
				" which is the only thing it exists for")
		}
	})
}
