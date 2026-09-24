package cli

import (
	"path/filepath"
	"testing"
)

// **Beside the database, not inside it.** `--auto-skip-db-path` names a file -
// `bolt.Open` takes a path, not a directory - so a second store cannot be put
// "in" it. A sibling in the same directory is the next best thing: one path to
// cache in CI, one flag, and the two generations cannot corrupt each other
// because they are not the same file.
func TestThePlanStoreSitsBesideTheAutoSkipDatabase(t *testing.T) {
	t.Parallel()

	got := planSkipBeside("/x/y/skip.db")
	if want := filepath.Join("/x/y", "skip.db"+planSkipSuffix); got != want {
		t.Errorf("beside %q the store is %q, want %q", "/x/y/skip.db", got, want)
	}

	if planSkipBeside("") != "" {
		t.Error("with no database named, nothing is derived from it")
	}
}

// Named or not, a store is had: the engine's own directory is the default.
//
// Not parallel: t.Setenv, which the runtime refuses alongside t.Parallel.
func TestAStoreIsHadWithOrWithoutAPathBeingNamed(t *testing.T) {
	named, err := skipRecordStoreFor("/x/y/skip.db")
	if err != nil || named.at == "" {
		t.Errorf("a named database gave %+v, %v", named, err)
	}

	t.Setenv(envCacheDir, t.TempDir())

	own, err := skipRecordStoreFor("")
	if err != nil || own.at == "" {
		t.Errorf("no database named gave %+v, %v", own, err)
	}

	if named.at == own.at {
		t.Error("naming a database did not move the store")
	}
}
