package fleet_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// aLayer writes a small tree into a store's layers directory and returns its id.
func aLayer(t *testing.T, root string) ir.NodeID {
	t.Helper()

	tmp := t.TempDir()

	err := os.MkdirAll(filepath.Join(tmp, "etc"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(tmp, "etc", "hosts"), []byte("127.0.0.1 localhost\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	c, err := layer.Take(tmp)
	if err != nil {
		t.Fatal(err)
	}

	at := filepath.Join(root, "layers", c.ID.String())

	err = os.MkdirAll(filepath.Dir(at), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Rename(tmp, at)
	if err != nil {
		t.Fatal(err)
	}

	return c.ID
}

// A layer moves between two stores and arrives as itself.
//
// The end of the road that E261 found blocked: one machine holds a layer, another
// needs it as a base, and until now there was no way to get it there. "Arrives as
// itself" is the whole property - the receiving store files it under the digest
// it computed from what arrived, not under the digest it was hoping for.
func TestALayerMovesBetweenStoresAndArrivesAsItself(t *testing.T) {
	t.Parallel()

	theirs := t.TempDir()
	mine := t.TempDir()

	id := aLayer(t, theirs)

	from := &fleet.Layers{Root: theirs}
	into := &fleet.Layers{Root: mine}

	if into.Has(id) {
		t.Fatal("the receiving store already had it")
	}

	moved, err := fleet.Provision(t.Context(), into,
		fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{id}},
		&fleet.LayerSource{Label: "them", Held: from})
	if err != nil {
		t.Fatalf("provisioning a layer: %v", err)
	}

	if !into.Has(id) {
		t.Fatal("the layer did not arrive")
	}

	// And it is genuinely that layer, not merely a directory with the right
	// name: a store that filed whatever arrived under the digest that was asked
	// for would pass every test above this line.
	got, err := layer.Take(filepath.Join(mine, "layers", id.String()))
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != id {
		t.Errorf("what arrived is %v, filed as %v", got.ID, id)
	}

	if moved.Bytes == 0 {
		t.Error("moving a layer was accounted as costing nothing")
	}
}

// A store refuses to file a layer under a digest it does not have.
//
// The check that makes a peer safe to fetch from (§5.3). A layer is a directory
// named by its digest, so a store that trusted the name would serve corruption
// for ever after - and every key derived from it would name something else.
func TestAStoreRefusesALayerThatIsNotWhatItClaims(t *testing.T) {
	t.Parallel()

	theirs := t.TempDir()
	real := aLayer(t, theirs)

	// A source that answers for one digest with a different layer's bytes.
	other := t.TempDir()
	wrong := aLayerWithContent(t, other, "something else entirely")

	mine := &fleet.Layers{Root: t.TempDir()}

	_, err := fleet.Provision(t.Context(), mine,
		fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{real}},
		&fleet.LayerSource{Label: "liar", Held: swapped{
			from: &fleet.Layers{Root: other}, want: real, give: wrong,
		}})
	if err == nil {
		t.Fatal("a layer that was not what it claimed was accepted")
	}

	if mine.Has(real) {
		t.Error("and it was filed under the digest that was asked for" +
			"\n  every key derived from that base would name something else")
	}
}

// swapped answers every request with one particular other layer.
type swapped struct {
	from *fleet.Layers
	want ir.NodeID
	give ir.NodeID
}

func (s swapped) Has(id ir.NodeID) bool { return id == s.want }

func (s swapped) Get(ir.NodeID) ([]byte, error) { return s.from.Get(s.give) }

func aLayerWithContent(t *testing.T, root, content string) ir.NodeID {
	t.Helper()

	tmp := t.TempDir()

	err := os.WriteFile(filepath.Join(tmp, "file"), []byte(content), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	c, err := layer.Take(tmp)
	if err != nil {
		t.Fatal(err)
	}

	at := filepath.Join(root, "layers", c.ID.String())

	err = os.MkdirAll(filepath.Dir(at), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Rename(tmp, at)
	if err != nil {
		t.Fatal(err)
	}

	return c.ID
}

// A layer that did not arrive whole leaves nothing behind.
//
// A half-unpacked directory sitting under the right digest is worse than no
// layer at all: `LayerStore.Has` answers yes, the cache treats it as a usable
// base, and the build proceeds on a tree that is missing files. So the unpack
// happens beside the store and is renamed in only once it has been checked.
func TestAPartialLayerIsNotLeftUnderItsName(t *testing.T) {
	t.Parallel()

	theirs := t.TempDir()
	id := aLayer(t, theirs)

	from := &fleet.Layers{Root: theirs}

	whole, err := from.Get(id)
	if err != nil {
		t.Fatal(err)
	}

	mine := &fleet.Layers{Root: t.TempDir()}

	_, _, err = mine.Put(truncatedReader(whole))
	if err == nil {
		t.Fatal("a truncated layer stream was accepted")
	}

	if mine.Has(id) {
		t.Error("a partial layer is sitting under its digest;" +
			" every later build would treat it as a usable base")
	}

	// Nor anywhere else visible: a scratch directory left behind fills a disk
	// one failed transfer at a time.
	entries, err := os.ReadDir(filepath.Join(mine.Root, "layers"))
	if err == nil && len(entries) != 0 {
		t.Errorf("%d directory(ies) left behind by a failed transfer", len(entries))
	}
}

// truncatedReader gives back most of a stream and then stops.
func truncatedReader(b []byte) io.Reader {
	return bytes.NewReader(b[:len(b)*3/4])
}

// watchingReader looks at the store the moment the unpack starts reading.
type watchingReader struct {
	r    io.Reader
	dir  string
	seen []string
}

func (w *watchingReader) Read(p []byte) (int, error) {
	if w.seen == nil {
		ents, err := os.ReadDir(w.dir)
		if err == nil {
			w.seen = []string{}

			for _, e := range ents {
				w.seen = append(w.seen, e.Name())
			}
		}
	}

	return w.r.Read(p) //nolint:wrapcheck // a fixture
}

// A layer is unpacked beside the store, not in the system temp directory.
//
// A rename within a filesystem is a rename; a rename across one is a copy of
// every byte. A layer is the largest thing this engine moves, so unpacking into
// `/tmp` would silently double the cost of receiving one on any machine where
// `/tmp` is a different filesystem - which is most of them, and none of them a
// developer's laptop, so it would be found in production and nowhere else.
//
// Observed while it happens: the scratch directory exists only between the start
// of the unpack and the rename, so the reader is what looks.
func TestALayerIsUnpackedBesideTheStore(t *testing.T) {
	t.Parallel()

	theirs := t.TempDir()
	id := aLayer(t, theirs)

	packed, err := (&fleet.Layers{Root: theirs}).Get(id)
	if err != nil {
		t.Fatal(err)
	}

	mine := &fleet.Layers{Root: t.TempDir()}

	err = os.MkdirAll(filepath.Join(mine.Root, "layers"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	w := &watchingReader{r: bytes.NewReader(packed), dir: filepath.Join(mine.Root, "layers")}

	_, _, err = mine.Put(w)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if len(w.seen) == 0 {
		t.Fatalf("nothing was being unpacked inside %s while the stream was"+
			" read\n  the scratch directory is somewhere else, so filing the"+
			" layer copies it rather than renaming it", mine.Root)
	}
}
