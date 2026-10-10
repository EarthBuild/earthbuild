package guest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A committed layer digests to the name it is filed under.
//
// The question E86 left open. A layer is stored under the digest its capture
// computed, and walking the stored directory afterwards produces a different
// one - measured on a clean build, so it is not a crash and not concurrency:
//
//	stored=03d5b28eabf6  full=77766c5d89b7  content=eb7dfe8ceed7
//
// Neither the with-times digest nor the without-times one comes back. That
// makes a layer the one part of this store whose identity cannot be checked:
// green paper **I2** says every blob is verified against its digest before use
// and `blob.Get` does exactly that, while nothing re-digests a directory.
//
// Asked in-process because that is where it can be bisected. A build has a
// guest, an overlay, a delta full of whiteouts and a shared store, and any of
// them could be the cause; a `Take`, a `commit` and a second `Take` has two
// steps and one of them is wrong.
func TestACommittedLayerKeepsItsDigest(t *testing.T) {
	t.Parallel()

	delta := t.TempDir()
	store := t.TempDir()

	err := os.MkdirAll(filepath.Join(delta, "w"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(delta, "w", "a.txt"), []byte("one\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	before, err := layer.Take(delta)
	if err != nil {
		t.Fatal(err)
	}

	err = ExportCommit(context.Background(), store, delta, before.ID)
	if err != nil {
		t.Fatal(err)
	}

	after, err := layer.Take(filepath.Join(store, "layers", before.ID.String()))
	if err != nil {
		t.Fatal(err)
	}

	if after.ID != before.ID {
		t.Errorf("committing changed the layer's identity:\n  before %s\n  after  %s",
			before.ID, after.ID)
	}

	// Reported separately, because which of the two moves says where to look:
	// the content digest excludes timestamps, so a difference in one and not
	// the other is the mtime clamp and a difference in both is the tree.
	if after.Content != before.Content {
		t.Errorf("committing changed the layer's contents:\n  before %s\n  after  %s",
			before.Content, after.Content)
	}
}
