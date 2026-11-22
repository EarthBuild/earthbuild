package bulk_test

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/bulk"
)

// An entry with no name is refused, because it names the export root itself.
//
// `PackTree` never writes one - the walk skips the root - so an empty name is
// only ever a hand-made archive. It was refused already, by the check that a
// write lands inside the root once links are followed - whose message blamed a
// symlink the archive does not contain.
func TestAnExportEntryWithNoNameIsRefused(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "", Typeflag: tar.TypeDir, Mode: 0o700}); err != nil {
		t.Fatal(err)
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}

	err := bulk.UnpackTree(&buf, root)
	if err == nil {
		t.Fatal("an entry with no name was accepted; it names the export root")
	}

	// Refused for the right reason: there is no symlink here, and a message
	// blaming one sends the reader looking for something that does not exist.
	if !strings.Contains(err.Error(), `""`) || strings.Contains(err.Error(), "symlink") {
		t.Fatalf("refused, but the message does not name the entry: %v", err)
	}

	fi, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}

	if got := fi.Mode().Perm(); got != 0o750 {
		t.Fatalf("the export root's mode is %o, want 0750 untouched", got)
	}
}
