package fleet_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// What a lazy base costs against a whole one, across sizes that mean something.
//
// **The number the decision rests on.** E283 measured that a step names tens to
// low hundreds of paths against a tree of twenty thousand files; E286 measured
// one fragment of a forty-one file layer at 2.8%. This is the curve between
// them, for bases and read sets the shape a real build has (E298).
//
// Reported rather than asserted on a figure: the answer depends on file sizes,
// and a test that failed because somebody changed a fixture would be a test
// nobody reads. What it does assert is the shape - that the fragment is smaller,
// and that the manifest is what stops it being negligible.
func TestWhatALazyBaseCosts(t *testing.T) {
	t.Parallel()

	for _, files := range []int{100, 1000, 5000} {
		for _, reads := range []int{10, 100} {
			if reads > files {
				continue
			}

			store := t.TempDir()
			id := aBaseOf(t, store, files)

			layers := &fleet.Layers{Root: store}

			whole, err := layers.Get(id)
			if err != nil {
				t.Fatal(err)
			}

			want := make([]string, 0, reads)
			for i := range reads {
				want = append(want, fmt.Sprintf("usr/lib/lib%d.so", i))
			}

			manifest, packed, err := layers.Fragment(id, want)
			if err != nil {
				t.Fatal(err)
			}

			cold := len(manifest) + len(packed)

			// Warm is the number that decides this. A proof crosses once per
			// layer (E299), and a build reads a base over many steps - so the
			// first fetch pays for it and every one after it does not.
			warm := len(packed)

			t.Logf("%5d files, %3d read: whole %8d | cold %7d (%5.1f%%)"+
				" | warm %7d (%5.1f%%)",
				files, reads, len(whole),
				cold, 100*float64(cold)/float64(len(whole)),
				warm, 100*float64(warm)/float64(len(whole)))

			moved := cold

			// Only where the read set is a small fraction of the base, which
			// is the case lazy transfer is for. Reading every file of a
			// hundred-file base legitimately costs more than the layer - the
			// fragment *is* the layer, plus a manifest - and a test that
			// expected otherwise would be expecting magic.
			if reads*4 < files && moved >= len(whole) {
				t.Errorf("%d files, %d read: a lazy base moved %d of %d",
					files, reads, moved, len(whole))
			}
		}
	}
}

// What one fault costs, once the base is primed.
//
// The other half of the arithmetic. A prime pays for the manifest once; a fault
// afterwards pays for one file and a round trip, because the manifest is already
// here. So a prediction that misses by a handful is cheap and one that misses by
// hundreds is not - which is the argument for `MaxPredicted` from the other
// direction (E287).
func TestWhatOneFaultCostsOnceTheBaseIsPrimed(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	id := aBaseOf(t, store, 1000)

	src := &countingFragmenter{
		inner: &fromStore{layers: &fleet.Layers{Root: store}},
	}

	base := t.TempDir()

	f := &fleet.Filler{
		Into:  base,
		Stack: []ir.NodeID{id},
		From:  []fleet.Fragmenter{src},
		Store: &fleet.Fragments{Root: t.TempDir()},
	}

	want := make([]string, 0, 100)
	for i := range 100 {
		want = append(want, fmt.Sprintf("usr/lib/lib%d.so", i))
	}

	err := f.Prime(context.Background(), want)
	if err != nil {
		t.Fatal(err)
	}

	primed := src.bytes

	// One path nobody predicted.
	err = f.Fill(context.Background(), filepath.Join(base, "usr", "lib", "lib500.so"))
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("prime of 100 paths moved %d bytes; one fault after it moved %d",
		primed, src.bytes-primed)

	if src.bytes-primed >= primed {
		t.Errorf("one fault moved %d bytes against a prime of %d;"+
			" a fault is meant to be one file and a round trip",
			src.bytes-primed, primed)
	}
}

// countingFragmenter counts what crossed.
type countingFragmenter struct {
	inner *fromStore
	bytes int
}

func (c *countingFragmenter) Fragment(
	ctx context.Context, id ir.NodeID, want []string, proof bool,
) ([]byte, []byte, error) {
	m, p, err := c.inner.Fragment(ctx, id, want, proof)
	c.bytes += len(m) + len(p)

	return m, p, err
}

// aBaseOf writes a layer with n files of a size a shared library has.
func aBaseOf(t *testing.T, root string, n int) ir.NodeID {
	t.Helper()

	tmp := t.TempDir()

	must(t, os.MkdirAll(filepath.Join(tmp, "usr", "lib"), 0o750))

	for i := range n {
		// **Distinct contents.** `byte(i)` wraps at 256, so five thousand files
		// would have two hundred and fifty-six distinct bodies - and a pack
		// stores contents once per digest (E262), so the "whole layer" would be
		// a fiftieth of its apparent size and every ratio measured against it
		// would be wrong in the flattering direction.
		body := bytes.Repeat([]byte(fmt.Sprintf("%08d", i)), 1024)

		must(t, os.WriteFile(
			filepath.Join(tmp, "usr", "lib", fmt.Sprintf("lib%d.so", i)),
			body, 0o600))
	}

	c, err := layer.Take(tmp)
	if err != nil {
		t.Fatal(err)
	}

	at := filepath.Join(root, "layers", c.ID.String())
	must(t, os.MkdirAll(filepath.Dir(at), 0o750))
	must(t, os.Rename(tmp, at))

	return c.ID
}
