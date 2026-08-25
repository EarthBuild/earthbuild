package image_test

import (
	"archive/tar"
	"bytes"
	"fmt"
	"runtime"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestHashingOnTheWayInCostsNoBufferPerFile.
//
// **A layer is fifteen thousand files, and one 32KiB copy buffer each is half a
// gigabyte of garbage.** `io.CopyN` allocates a buffer whenever it cannot hand
// the copy to a `ReaderFrom`, and hashing on the way in puts an `io.MultiWriter`
// between the copy and the file - so the digesting arm allocated that buffer per
// entry where the plain arm hands the file straight to the kernel and allocates
// none.
//
// That was most of what hashing cost. On `golang:1.26-alpine`'s largest layer it
// added 785ms to a 1.6s unpack, where blake3 over its 228MB is about 143ms at
// the 1590 MB/s that guest manages - so the bytes were never the cost (E682).
//
// Asserted against the plain arm rather than against a number. What matters is
// that hashing does not add an allocation that scales with the number of files,
// and the floor - headers, names, the digest map - is the unpack's own and moves
// for reasons that have nothing to do with this.
func TestHashingOnTheWayInCostsNoBufferPerFile(t *testing.T) {
	const (
		files = 3000
		body  = 4096
	)

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)
	content := bytes.Repeat([]byte("x"), body)

	for i := range files {
		err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     fmt.Sprintf("f%05d", i),
			Mode:     0o644,
			Size:     int64(len(content)),
		})
		if err != nil {
			t.Fatal(err)
		}

		_, err = tw.Write(content)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	perFile := func(t *testing.T) uint64 {
		t.Helper()

		var before, after runtime.MemStats

		runtime.GC()
		runtime.ReadMemStats(&before)

		got, uerr := image.UnpackApart(bytes.NewReader(buf.Bytes()), t.TempDir())
		if uerr != nil {
			t.Fatal(uerr)
		}

		runtime.ReadMemStats(&after)

		if n := len(got.Digests); n != 0 && n != files {
			t.Fatalf("unpacked %d digests, want %d or none", n, files)
		}

		return (after.TotalAlloc - before.TotalAlloc) / files
	}

	// The plain arm first, so the digesting arm is not measured against a cold
	// allocator.
	t.Setenv(image.EnvNoKnownDigests, "1")

	plain := perFile(t)

	t.Setenv(image.EnvNoKnownDigests, "")

	hashing := perFile(t)

	t.Logf("%d bytes per file plain, %d hashing, %+d for the hashing",
		plain, hashing, int64(hashing)-int64(plain))

	// blake3's own state and the digest map entry are real and per file. A copy
	// buffer is 32KiB, and nothing between these two arms should be near it.
	const budget = 4 << 10

	if hashing > plain+budget {
		t.Errorf("hashing adds %d bytes per file over the plain unpack, want under %d"+
			"\n  a copy buffer per entry is half a gigabyte of garbage on a real"+
			"\n  layer, and it was most of what hashing on the way in cost",
			hashing-plain, budget)
	}
}
