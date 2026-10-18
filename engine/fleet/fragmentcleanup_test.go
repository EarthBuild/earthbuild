package fleet

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A fragment that fails leaves nothing behind.
//
// `PutVerified` unpacks beside where the fragment will live, so filing it is a
// rename rather than a copy, and only renames once the contents have been
// checked against the manifest. Everything before that point is provisional: a
// fragment that fails verification, or arrives truncated, must take its
// half-unpacked directory with it.
//
// Otherwise a worker accumulates `.incoming-*` directories, one per failed
// transfer, in the same place it looks for fragments - and the failure that
// produced them is the case where transfers are already going wrong (E282).
//
// The mutant deleting the cleanup survived `go test ./engine/fleet/` and also
// survived `tests/fleet+all` against a fleet that really delegated, so neither
// suite was watching the floor.
func TestAFragmentThatFailsLeavesNothingBehind(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	f := &Fragments{Root: root}

	// A manifest that cannot be read is still a manifest for the id derived
	// from it, so this gets past the identity check and fails at verification -
	// which is after the unpack, which is the point.
	manifest := []byte("this is not a manifest")
	id := layer.ManifestID(manifest)

	// A real layer stream, so the unpack succeeds and the failure lands where
	// the cleanup is: after it. An empty tar does not do - the stream has a
	// magic of its own and is refused before the deferred cleanup is armed.
	src := t.TempDir()

	err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var packed bytes.Buffer

	err = layer.Pack(src, &packed)
	if err != nil {
		t.Fatal(err)
	}

	err = f.PutVerified(id, []string{"usr/bin/sh"}, manifest, &packed)
	if err == nil {
		t.Fatal("a fragment whose manifest cannot be read was accepted")
	}

	// Nothing provisional survives. The fragments live under the root, and the
	// half-unpacked ones are named for arriving.
	var left []string

	walkErr := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && strings.HasPrefix(d.Name(), ".incoming-") {
			left = append(left, p)
		}

		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}

	if len(left) != 0 {
		t.Errorf("a failed fragment left %d directory(ies) behind: %v"+
			"\n  they accumulate one per failed transfer, in the place this"+
			" worker looks for fragments", len(left), left)
	}
}
