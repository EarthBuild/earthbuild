package exec_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestTheUnpackersDigestIsTheEnginesDigest pins two declarations of one number.
//
// **The unpacker tells the store what it hashed instead of the store reading it
// back** - 0.958s of a cold `golang:1.26-alpine` pull. That is sound only while
// the two compute the same function, and they cannot share a constant: `ir`
// imports `engine/image` for `Healthcheck`, so `image` naming `ir` would be an
// import cycle.
//
// So the agreement is asserted here, in a package that can see both. If it ever
// breaks, every layer placed from a pull is filed under a name no read of the
// tree will reproduce - a false miss at best and, since ids index the cache, a
// store that cannot find what it just wrote (I3).
func TestTheUnpackersDigestIsTheEnginesDigest(t *testing.T) {
	t.Parallel()

	if image.HashSize != ir.HashSize {
		t.Fatalf("the unpacker hashes to %d bytes and the engine to %d",
			image.HashSize, ir.HashSize)
	}

	for _, body := range []string{"", "a", "the quick brown fox", string(make([]byte, 1<<16))} {
		engine := ir.NewHasher()

		_, err := engine.Write([]byte(body))
		if err != nil {
			t.Fatal(err)
		}

		unpacker := image.NewContentHasher()

		_, err = unpacker.Write([]byte(body))
		if err != nil {
			t.Fatal(err)
		}

		if ir.NodeID(unpacker.Sum(nil)) != engine.Sum() {
			t.Fatalf("over %d bytes the unpacker gives %x and the engine %v",
				len(body), unpacker.Sum(nil), engine.Sum())
		}
	}
}
