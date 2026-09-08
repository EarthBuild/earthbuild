package store

import (
	"os"
	"path/filepath"
	"testing"
)

// Collection stops as soon as the filesystem says there is room, and never
// measures the store to decide.
//
// **statfs is 2.2us; walking this store is 5.1s.** The collector asked the
// expensive question: SizeAll walked the whole store to derive a ceiling, and
// candidates walked it again to size every layer - on a store of 696,191 files,
// warm, that is five seconds spent measuring before a single byte is freed. A
// five-second budget was therefore spent almost entirely on measurement, which
// is why collection could not keep pace with the builds filling the store.
//
// Free space is the actual goal, it is one syscall, and it is exact - where a
// ceiling derived from a size estimate is neither. It also retires the E574
// class of bug outright: there is no size estimate left to be wrong.
func TestCollectionStopsWhenTheFilesystemSaysThereIsRoom(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	layers := filepath.Join(root, "layers")

	if err := os.MkdirAll(layers, 0o750); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"1111111111111111111111111111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222222222222222222222222222",
		"3333333333333333333333333333333333333333333333333333333333333333",
		"4444444444444444444444444444444444444444444444444444444444444444",
	} {
		if err := os.MkdirAll(filepath.Join(layers, name), 0o750); err != nil {
			t.Fatal(err)
		}
	}

	// A filesystem that gains ten bytes of room per reading, so the stop
	// condition is the thing under test rather than the host's actual disk.
	// The first reading is the one taken before anything is removed.
	reads := 0
	free := func(string) (uint64, error) {
		defer func() { reads++ }()

		return uint64(reads) * 10, nil
	}

	report, err := collectUntilFree(root, 25, nil, nil, free)
	if err != nil {
		t.Fatal(err)
	}

	// Three removals take it from 0 to 30, which is the first reading at or
	// above 25 - the fourth layer is not touched.
	if report.Removed != 3 {
		t.Fatalf("removed %d layers, wanted 3: it should stop at the first reading with room",
			report.Removed)
	}

	left, err := os.ReadDir(layers)
	if err != nil {
		t.Fatal(err)
	}

	if len(left) != 1 {
		t.Errorf("%d layers left, wanted 1", len(left))
	}
}

// A store that already has room is not touched at all.
func TestAStoreWithRoomIsNotCollected(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	layers := filepath.Join(root, "layers")

	if err := os.MkdirAll(filepath.Join(layers,
		"1111111111111111111111111111111111111111111111111111111111111111"), 0o750); err != nil {
		t.Fatal(err)
	}

	report, err := collectUntilFree(root, 10, nil, nil,
		func(string) (uint64, error) { return 100, nil })
	if err != nil {
		t.Fatal(err)
	}

	if report.Removed != 0 {
		t.Errorf("removed %d layers from a store that already had room", report.Removed)
	}
}
