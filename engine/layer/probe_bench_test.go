package layer

import (
	"bufio"
	"slices"
	"sort"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

func foldOf(tb testing.TB, n int) *Fold {
	tb.Helper()

	f := NewFold()
	for i := range n {
		p := string(rune('a'+i%26)) + "/dir" + string(rune('a'+(i/26)%26)) + "/file" +
			string(rune('a'+(i/7)%26)) + string(rune('a'+(i/3)%26)) + string(rune('a'+i%17))
		f.merged[p] = entry{path: p, mode: 0o644, size: int64(i)}
	}

	return f
}

// BenchmarkSortShape: sort.Strings is already unstable pdqsort; slices.Sort is
// the same algorithm monomorphised, so any difference is interface dispatch.
func BenchmarkSortShape(b *testing.B) {
	f := foldOf(b, 20000)

	base := make([]string, 0, len(f.merged))
	for p := range f.merged {
		base = append(base, p)
	}

	b.Run("sort.Strings", func(b *testing.B) {
		buf := make([]string, len(base))

		for b.Loop() {
			copy(buf, base)
			sort.Strings(buf)
		}
	})

	b.Run("slices.Sort", func(b *testing.B) {
		buf := make([]string, len(base))

		for b.Loop() {
			copy(buf, base)
			slices.Sort(buf)
		}
	})
}

// BenchmarkHashShape: the same bytes, written through a buffer instead of one
// Write per field. Identical digest, far fewer calls into blake3.
func BenchmarkHashShape(b *testing.B) {
	f := foldOf(b, 20000)

	paths := make([]string, 0, len(f.merged))
	for p := range f.merged {
		paths = append(paths, p)
	}

	slices.Sort(paths)

	b.Run("direct", func(b *testing.B) {
		for b.Loop() {
			h := ir.NewHasher()
			h.Count(len(paths))

			for _, p := range paths {
				e := f.merged[p]
				e.hash(&h.Encoder, withoutTimes)
			}

			h.Sum()
		}
	})

	b.Run("buffered", func(b *testing.B) {
		for b.Loop() {
			h := ir.NewHasher()
			bw := bufio.NewWriterSize(h, 64<<10)
			enc := ir.NewEncoder(bw)
			enc.Count(len(paths))

			for _, p := range paths {
				e := f.merged[p]
				e.hash(enc, withoutTimes)
			}

			_ = bw.Flush()
			h.Sum()
		}
	})
}

// The two must produce the same digest, or the buffer is a key change.
func TestBufferingDoesNotChangeTheDigest(t *testing.T) {
	f := foldOf(t, 500)

	paths := make([]string, 0, len(f.merged))
	for p := range f.merged {
		paths = append(paths, p)
	}

	slices.Sort(paths)

	h := ir.NewHasher()
	h.Count(len(paths))

	for _, p := range paths {
		e := f.merged[p]
		e.hash(&h.Encoder, withoutTimes)
	}

	h2 := ir.NewHasher()
	bw := bufio.NewWriterSize(h2, 64<<10)
	enc := ir.NewEncoder(bw)
	enc.Count(len(paths))

	for _, p := range paths {
		e := f.merged[p]
		e.hash(enc, withoutTimes)
	}

	_ = bw.Flush()

	if h.Sum() != h2.Sum() {
		t.Fatal("buffering changed the digest, so it is a key change and not an optimisation")
	}
}
