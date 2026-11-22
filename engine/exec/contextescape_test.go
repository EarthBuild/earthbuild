package exec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Staging a context entry does not follow a symlink out of the context.
//
// The interpreter refuses such a COPY when it plans it; this is the same rule
// where the bytes are read, so a graph built some other way - or a check that
// is later loosened - still cannot put a host file into a build.
func TestStagingDoesNotFollowALinkOutOfTheContext(t *testing.T) {
	t.Parallel()

	outside := filepath.Join(t.TempDir(), "secret")

	err := os.WriteFile(outside, []byte("host only"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.TempDir()

	err = os.Symlink(outside, filepath.Join(ctx, "lnk"))
	if err != nil {
		t.Fatal(err)
	}

	e := &Executor{Context: ctx}
	n := &ir.Node{Op: ir.Op{Kind: ir.OpLocal, Args: []string{"lnk"}}}
	staged := t.TempDir()

	err = e.copyContextInto(n, staged)
	if err == nil {
		t.Error("lnk was staged, and it leads out of the context to " + outside)
	}

	b, readErr := os.ReadFile(filepath.Join(staged, "lnk"))
	if readErr == nil && string(b) == "host only" {
		t.Error("the host file's contents were copied into the staged context")
	}
}
