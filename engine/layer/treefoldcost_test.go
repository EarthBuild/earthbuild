package layer_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// manifestLadder builds n layers of m entries each and returns their manifests.
//
// Entry paths are disjoint between layers, so the fold merges rather than
// overwrites - the case that costs the most, and the shape a deep build has.
func manifestLadder(tb testing.TB, n, m int) [][]byte {
	tb.Helper()

	out := make([][]byte, n)

	for i := range n {
		dir := tb.TempDir()

		for j := range m {
			p := filepath.Join(dir, fmt.Sprintf("d%02d", j%16), fmt.Sprintf("f%d-%d", i, j))
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				tb.Fatal(err)
			}

			if err := os.WriteFile(p, []byte{byte(i), byte(j)}, 0o600); err != nil {
				tb.Fatal(err)
			}
		}

		m, err := layer.Manifest(dir)
		if err != nil {
			tb.Fatal(err)
		}

		out[i] = m
	}

	return out
}

// BenchmarkTreeFromManifests measures the fold against stack depth.
//
// **A ladder, because the per-layer cost is the slope.** Κₜ asks this of every
// step's base, and a linear target's step 𝑖 has a stack of 𝑖 layers - so a
// build's total is the sum over the ladder, not one run's figure. Reporting a
// single depth would charge the whole intercept to one layer.
func BenchmarkTreeFromManifests(b *testing.B) {
	const perLayer = 256

	for _, depth := range []int{1, 2, 4, 8, 16, 32} {
		ms := manifestLadder(b, depth, perLayer)

		b.Run(fmt.Sprintf("depth=%d", depth), func(b *testing.B) {
			b.ReportMetric(float64(depth*perLayer), "entries")
			b.ResetTimer()

			for b.Loop() {
				if _, ok := layer.TreeFromManifests(ms); !ok {
					b.Fatal("the ladder did not fold")
				}
			}
		})
	}
}

// BenchmarkFoldParts splits the fold into decode, merge and digest.
//
// Which of the three dominates decides what a memo must cache: a decode cache
// keyed on the layer is trivial, a merged-map cache per stack prefix is not.
func BenchmarkFoldParts(b *testing.B) {
	ms := manifestLadder(b, 8, 256)

	b.Run("decode", func(b *testing.B) {
		for b.Loop() {
			for _, m := range ms {
				layer.DecodeForBench(b, m)
			}
		}
	})

	b.Run("whole", func(b *testing.B) {
		for b.Loop() {
			_, _ = layer.TreeFromManifests(ms)
		}
	})
}

// BenchmarkBuildShapedLadder is what a build pays, not what one fold costs.
//
// **The shape is one fat base and many thin layers**: a deps layer of tens of
// thousands of entries, then a step's worth of output each time. A linear target
// of N steps asks Κₜ about stacks of depth 1..N, so the base is re-folded N
// times and the build's bill is the sum over the ladder - quadratic in N even
// though nothing about the base changed.
func BenchmarkBuildShapedLadder(b *testing.B) {
	const thin = 24 // what a step adds

	for _, c := range []struct{ base, steps int }{
		{8192, 16}, {8192, 32}, {8192, 64}, {16384, 32}, {32768, 32},
	} {
		ms := make([][]byte, 0, c.steps+1)
		ms = append(ms, manifestLadder(b, 1, c.base)...)
		ms = append(ms, manifestLadder(b, c.steps, thin)...)

		b.Run(fmt.Sprintf("base=%d/steps=%d", c.base, c.steps), func(b *testing.B) {
			for b.Loop() {
				// Every step asks about its own base: depths 1..steps.
				for d := 1; d <= c.steps; d++ {
					_, _ = layer.TreeFromManifests(ms[:d])
				}
			}
		})
	}
}

// BenchmarkDigestParts splits Digest into its sort and its hashing.
//
// If the sort dominates, keeping the paths sorted as they go removes it without
// changing a single byte of the digest - which matters, because the digest of a
// one-layer stack is that layer's own content id and the two tiers must agree.
func BenchmarkDigestParts(b *testing.B) {
	ms := manifestLadder(b, 1, 20000)

	f := layer.NewFold()
	if !f.Add(ms[0]) {
		b.Fatal("the base did not fold")
	}

	b.Run("sort-only", func(b *testing.B) {
		for b.Loop() {
			layer.SortCostForBench(f)
		}
	})

	b.Run("whole-digest", func(b *testing.B) {
		for b.Loop() {
			f.Digest()
		}
	})
}
