package cli

import (
	"path/filepath"
)

// planSkipSuffix names the native engine's auto-skip store, which sits beside
// buildkit's rather than in it.
//
// **`--auto-skip-db-path` names a file, not a directory** - `bolt.Open` takes a
// path - so a second store cannot be put inside it, and the two cannot share
// one: `LocalBuildkitSkipper` refuses any key that is not twenty bytes, and this
// store is not a set of keys at all. A sibling in the same directory gives a CI
// cache one path to carry, leaves either generation readable by the engine that
// wrote it, and cannot corrupt the other by being written.
const planSkipSuffix = ".plan-skip"

// planSkipBeside is where the record store lives for a given auto-skip
// database, or empty where the caller named none.
func planSkipBeside(db string) string {
	if db == "" {
		return ""
	}

	return db + planSkipSuffix
}

// skipRecordStoreFor is where a build records what it read.
//
// The engine's own store by default, which is where it already keeps what it
// learned between builds (see savePredictions); `--auto-skip-db-path` moves it,
// so one directory holds both engines' answers and CI caches one path.
func skipRecordStoreFor(db string) (skipRecordStore, error) {
	if at := planSkipBeside(db); at != "" {
		return skipRecordStore{at: at}, nil
	}

	dir, err := storeDir()
	if err != nil {
		return skipRecordStore{}, err
	}

	return skipRecordStore{at: filepath.Join(dir, "plan-skip")}, nil
}
