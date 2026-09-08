package store_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/store"
)

// A layer write that did not finish is reclaimed, not kept for ever.
//
// **The store filled with rubble nothing could clear.** A layer is written to
// `.<id>.partial-<n>` and renamed into place when it is whole, so an
// interrupted write leaves that directory behind. `candidates` parses each
// entry's name as a node id and skips what does not parse - "a name it does not
// understand is not its business" - which is right for a stranger's file and
// wrong for this engine's own debris.
//
// Nothing else removes them, so every failed write leaked its bytes
// permanently. One session of killed guests and ENOSPC mid-copy took a 195G
// store to 7M free, and a collector asked for 20G could not find it: the space
// was in partials it was declining to look at.
//
// Safe to remove at collection time because a store is collected by the agent
// at startup, before any step runs, and a device-backed store is claimed
// exclusively - so no partial can belong to a write in progress. That is the
// same assumption `Prune` already documents.
func TestAnUnfinishedLayerWriteIsReclaimed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	keep := layerIn(t, root, "kept", 4096)
	usedAt(t, root, keep, -time.Minute)

	// The debris of an interrupted write, named as the writer names it.
	partial := filepath.Join(root, "layers",
		".2e5c6835880250b0c63219172c3850eb73487cc387b2d7c50e60c384d894861a.partial-4152642701")

	err := os.MkdirAll(filepath.Join(partial, "usr", "bin"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(partial, "usr", "bin", "earth"), make([]byte, 8192), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// A ceiling far above what the real layer costs, so nothing would be
	// collected on age alone: only the debris should go.
	report, err := store.CollectUntil(root, 1<<30, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Error("an unfinished layer write survived a collection, so its bytes are lost for good")
	}

	if !stillThere(root, keep) {
		t.Error("a finished layer was taken while clearing debris")
	}

	if report.Freed() == 0 {
		t.Error("the report does not account for what clearing the debris freed")
	}
}
