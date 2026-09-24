package store_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// ladderInStore writes one fat base and n thin layers, and returns the stack.
func ladderInStore(tb testing.TB, root string, base, n int) []ir.NodeID {
	tb.Helper()

	out := make([]ir.NodeID, 0, n+1)

	for i := range n + 1 {
		dir := tb.TempDir()

		count := 1
		if i == 0 {
			count = base // the deps layer
		}

		for j := range count {
			p := filepath.Join(dir, fmt.Sprintf("d%02d", j%16), fmt.Sprintf("f%d-%d", i, j))
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				tb.Fatal(err)
			}

			if err := os.WriteFile(p, []byte{byte(i), byte(j)}, 0o600); err != nil {
				tb.Fatal(err)
			}
		}

		took, err := layer.Take(dir)
		if err != nil {
			tb.Fatal(err)
		}

		m, err := layer.Manifest(dir)
		if err != nil {
			tb.Fatal(err)
		}

		if err := os.MkdirAll(filepath.Dir(store.ManifestPath(root, took.ID)), 0o750); err != nil {
			tb.Fatal(err)
		}

		store.NoteManifest(root, took.ID, m)

		out = append(out, took.ID)
	}

	return out
}

// BenchmarkBuildAsksItsLadder is what a build's Κₜ derivation costs end to end.
//
// **The unit is the build, not the fold.** A target's step 𝑖 asks about a stack
// of 𝑖 layers, so the bill is the sum over the ladder - and reporting one fold's
// cost charges the whole base to one step. Fresh is what every step paid before
// store.Folder; rolling is what the ladder costs when the prefix is reused.
func BenchmarkBuildAsksItsLadder(b *testing.B) {
	const (
		base  = 20000 // a Rust deps layer's order of magnitude
		steps = 48
	)

	root := b.TempDir()
	stack := ladderInStore(b, root, base, steps)

	b.Run("fresh", func(b *testing.B) {
		st := store.DirStore(root)

		for b.Loop() {
			for d := 1; d <= steps; d++ {
				if _, ok := st.TreeOf(stack[:d]); !ok {
					b.Fatal("the ladder did not fold")
				}
			}
		}
	})

	b.Run("rolling", func(b *testing.B) {
		for b.Loop() {
			f := store.NewFolder(root)

			for d := 1; d <= steps; d++ {
				if _, ok := f.TreeOf(stack[:d]); !ok {
					b.Fatal("the ladder did not fold")
				}
			}
		}
	})
}

// BenchmarkInterleavedChains is the scheduler's actual shape.
//
// **Steps claim the cache before they take a slot**, so Κₜ derivations from
// independent branches interleave: the folder is asked about chain A, then B,
// then A again. One held prefix answers a chain and is thrown away by its
// neighbour, so the reuse measured on a single ladder is not what a parallel
// build gets. This says how much is left.
func BenchmarkInterleavedChains(b *testing.B) {
	const (
		base  = 20000
		steps = 12
	)

	root := b.TempDir()

	// One shared base, then n independent chains over it.
	for _, chains := range []int{1, 2, 4} {
		ladders := make([][]ir.NodeID, chains)
		shared := ladderInStore(b, root, base, 0)

		for c := range chains {
			ladders[c] = append(append([]ir.NodeID{}, shared...),
				ladderInStore(b, root, 1, steps-1)[1:]...)
		}

		b.Run(fmt.Sprintf("chains=%d", chains), func(b *testing.B) {
			for b.Loop() {
				f := store.NewFolder(root)

				// Round-robin, which is what a semaphore of width n produces.
				for d := 1; d <= steps; d++ {
					for c := range chains {
						if _, ok := f.TreeOf(ladders[c][:d]); !ok {
							b.Fatal("the ladder did not fold")
						}
					}
				}
			}
		})
	}
}
