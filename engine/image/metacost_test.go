package image

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// What a regular file costs an unpacker, split three ways.
//
// A cold build spends 2.9s writing about 10,000 files of a golang base, which is
// 294µs each - far more than the writing. Every file gets `open`, `write`,
// `close`, and then Chmod and Chtimes *by path*: two more resolutions of a path
// this code has just written and still held a descriptor for. Whether that is
// where the time goes is a question with an answer, so it gets one before the
// permissions path is rewritten (E533).
var benchPayload = make([]byte, 4096)

// BenchmarkWriteOnly is the floor: no metadata at all.
func BenchmarkWriteOnly(b *testing.B) {
	dir := b.TempDir()

	b.ResetTimer()

	for i := 0; b.Loop(); i++ {
		name := filepath.Join(dir, "f"+itoa(i))

		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			b.Fatal(err)
		}

		_, err = f.Write(benchPayload)
		if err != nil {
			b.Fatal(err)
		}

		err = f.Close()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMetaByPath is what the unpacker does today.
func BenchmarkMetaByPath(b *testing.B) {
	dir := b.TempDir()
	when := time.Unix(1000000, 0)

	b.ResetTimer()

	for i := 0; b.Loop(); i++ {
		name := filepath.Join(dir, "f"+itoa(i))

		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			b.Fatal(err)
		}

		_, err = f.Write(benchPayload)
		if err != nil {
			b.Fatal(err)
		}

		err = f.Close()
		if err != nil {
			b.Fatal(err)
		}

		if err := os.Chmod(name, 0o600); err != nil {
			b.Fatal(err)
		}

		err = os.Chtimes(name, when, when)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMetaByDescriptor sets the mode on the descriptor already open, and
// leaves only the time to the path.
func BenchmarkMetaByDescriptor(b *testing.B) {
	dir := b.TempDir()
	when := time.Unix(1000000, 0)

	b.ResetTimer()

	for i := 0; b.Loop(); i++ {
		name := filepath.Join(dir, "f"+itoa(i))

		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			b.Fatal(err)
		}

		_, err = f.Write(benchPayload)
		if err != nil {
			b.Fatal(err)
		}

		err = f.Chmod(0o600)
		if err != nil {
			b.Fatal(err)
		}

		err = f.Close()
		if err != nil {
			b.Fatal(err)
		}

		err = os.Chtimes(name, when, when)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}

	var b [20]byte

	p := len(b)

	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}

	return string(b[p:])
}

// BenchmarkWriteParallel asks whether creating files is something this machine
// will do several of at once. The unpacker is strictly sequential, and if the
// filesystem serialises creation anyway then making it concurrent buys nothing
// but a way to corrupt a layer.
func BenchmarkWriteParallel(b *testing.B) {
	dir := b.TempDir()
	when := time.Unix(1000000, 0)

	var n atomic.Int64

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			name := filepath.Join(dir, "f"+itoa(int(n.Add(1))))

			f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			if err != nil {
				b.Error(err)

				return
			}

			_, err = f.Write(benchPayload)
			if err != nil {
				b.Error(err)

				return
			}

			err = f.Close()
			if err != nil {
				b.Error(err)

				return
			}

			if err := os.Chmod(name, 0o600); err != nil {
				b.Error(err)

				return
			}

			err = os.Chtimes(name, when, when)
			if err != nil {
				b.Error(err)

				return
			}
		}
	})
}

// BenchmarkWriteParallelSpread is the same question without the shared
// directory: every worker writes into its own, which is closer to a real archive
// and removes the one lock every entry in the benchmark above contends on.
func BenchmarkWriteParallelSpread(b *testing.B) {
	root := b.TempDir()
	when := time.Unix(1000000, 0)

	var workers atomic.Int64

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		dir := filepath.Join(root, "w"+itoa(int(workers.Add(1))))
		err := os.MkdirAll(dir, 0o750)
		if err != nil {
			b.Error(err)

			return
		}

		for i := 0; pb.Next(); i++ {
			name := filepath.Join(dir, "f"+itoa(i))

			f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			if err != nil {
				b.Error(err)

				return
			}

			_, err = f.Write(benchPayload)
			if err != nil {
				b.Error(err)

				return
			}

			err = f.Close()
			if err != nil {
				b.Error(err)

				return
			}

			if err := os.Chmod(name, 0o600); err != nil {
				b.Error(err)

				return
			}

			err = os.Chtimes(name, when, when)
			if err != nil {
				b.Error(err)

				return
			}
		}
	})
}

// BenchmarkCreateEmpty separates making a file from filling it. If an empty file
// costs what a 4KB one costs, the wall is the directory entry and no amount of
// cleverness about the writing will move it.
func BenchmarkCreateEmpty(b *testing.B) {
	dir := b.TempDir()

	b.ResetTimer()

	for i := 0; b.Loop(); i++ {
		f, err := os.OpenFile(filepath.Join(dir, "f"+itoa(i)), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			b.Fatal(err)
		}

		err = f.Close()
		if err != nil {
			b.Fatal(err)
		}
	}
}
