package exec_test

import (
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// A host-only build keeps its records in the user's store, not the shared tmp.
//
// Its action cache and profile store were opened in `os.TempDir()`, which on
// Linux is one `/tmp` for every user, under names anyone can predict: another
// user could create them first and read or rewrite what the build recorded.
func TestAHostOnlyBuildKeepsItsStorePrivate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	e, err := exec.NewHostOnly(dir)
	if err != nil {
		t.Fatal(err)
	}

	if got := e.Sandbox().StoreDir(); got != dir {
		t.Errorf("a host-only build stores in %s, and was given %s", got, dir)
	}

	_, err = exec.NewHostOnly("")
	if err == nil {
		t.Errorf("a host-only build with no store was accepted, and would fall back to %s",
			os.TempDir())
	}
}
