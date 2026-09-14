package remote_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/remote"
)

// A bytestream resource name is read from its markers, not from its shape.
//
// **`instance_name` may contain slashes and is allowed to be empty**, so
// counting segments from the left gets the wrong answer for two different
// clients. The parts that can be found are the literal `blobs` (or `uploads`)
// segments; everything before the first is a name this service does not use,
// and anything after the size is metadata a client attached for itself.
func TestABlobNameIsFoundByItsMarkers(t *testing.T) {
	t.Parallel()
	t.Cleanup(ir.SelectHashForTest(t, ir.HashSHA256))

	hash := ir.DigestOf([]byte("some blob"))

	for name, res := range map[string]string{
		"a bare download":            "blobs/" + hash.String() + "/9",
		"with an instance":           "my-instance/blobs/" + hash.String() + "/9",
		"an instance with slashes":   "some/deep/instance/blobs/" + hash.String() + "/9",
		"a leading slash":            "/blobs/" + hash.String() + "/9",
		"an upload":                  "uploads/0c5e-4f/blobs/" + hash.String() + "/9",
		"an upload with an instance": "inst/uploads/0c5e-4f/blobs/" + hash.String() + "/9",
		"an upload with metadata":    "uploads/0c5e-4f/blobs/" + hash.String() + "/9/some/thing",
		"a named digest function":    "blobs/sha256/" + hash.String() + "/9",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, size, err := remote.BlobName(res)
			if err != nil {
				t.Fatalf("%q: %v", res, err)
			}

			if got != hash {
				t.Errorf("%q names %v, want %v", res, got, hash)
			}

			if size != 9 {
				t.Errorf("%q is %d bytes, want 9", res, size)
			}
		})
	}
}

// What cannot be served is refused by name rather than guessed at.
func TestAnUnusableResourceNameIsRefused(t *testing.T) {
	t.Parallel()
	t.Cleanup(ir.SelectHashForTest(t, ir.HashSHA256))

	hash := ir.DigestOf([]byte("some blob")).String()

	for name, tc := range map[string]struct{ res, says string }{
		// Compression is a capability this service does not advertise, so a
		// client asking for it has been told something by somebody else.
		"compressed":      {res: "compressed-blobs/zstd/" + hash + "/9", says: "compress"},
		"no blobs marker": {res: "something/else/" + hash + "/9", says: "blobs"},
		"no size":         {res: "blobs/" + hash, says: "size"},
		"a bad size":      {res: "blobs/" + hash + "/not-a-number", says: "size"},
		"a bad hash":      {res: "blobs/nonsense/9", says: "digest"},
		"empty":           {res: "", says: "blobs"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, _, err := remote.BlobName(tc.res)
			if err == nil {
				t.Fatalf("%q was accepted", tc.res)
			}

			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("%q is refused with %q, which does not mention %q",
					tc.res, err, tc.says)
			}
		})
	}
}
