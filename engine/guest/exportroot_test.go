package guest

import (
	"path/filepath"
	"testing"
)

// An export is staged where the host can read it, wherever the layers live.
//
// The host copies an artifact out of the staging directory by a path it computes
// itself, off the mount it shares with the guest. Staging lived under the layer
// store because for a long time the store *was* that mount - and when the layers
// moved to the guest's own block device the staging went with them, onto a
// filesystem the host cannot open. Every `SAVE ARTIFACT` then failed with "the
// guest did not stage", naming a host path that was never going to exist.
//
// Two things are asserted because either alone would have passed while the bug
// was live: that the staging follows the export directory when it is set, and
// that it is the layer directory when it is not, which is what every build did
// before the store could move.
func TestExportsAreStagedWhereTheHostCanReadThem(t *testing.T) {
	layers := t.TempDir()
	shared := t.TempDir()

	s := &Server{LayerDir: layers}

	if got := s.exportRoot(); got != layers {
		t.Errorf("with no export directory the staging is %q, want the layer"+
			" directory %q - which is where it has always been", got, layers)
	}

	t.Setenv(EnvExportDir, shared)

	if got := s.exportRoot(); got != shared {
		t.Errorf("the staging is %q and not the shared mount %q: the host"+
			" reads an artifact off that mount, so an export written anywhere"+
			" else cannot be collected", got, shared)
	}

	// And it is the directory itself, not a path under the store that merely
	// happens to be named alike.
	if filepath.Dir(filepath.Join(s.exportRoot(), "exports")) == layers {
		t.Error("the staging is still under the layer directory")
	}
}
