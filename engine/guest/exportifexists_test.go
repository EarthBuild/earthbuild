package guest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// `SAVE ARTIFACT --if-exists` skips an absent path and saves a present one.
//
// The flag never saved anything. The host decided absence for itself, with an
// os.Stat of the materialised root - but that root is a path inside the guest's
// mount namespace, so the stat failed however the build had gone and every
// flagged save was skipped. A differential against earthly found it: the same
// Earthfile produced the file under earthly and nothing under this engine.
//
// It survived because the only test of the flag applied it to a path that was
// absent, where "skips correctly" and "never saves anything" are one
// observation. Both halves are asserted here, and here rather than end-to-end
// because this is the layer the question is now answered at.
func TestExportIfExistsSavesWhatIsThereAndSkipsWhatIsNot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.WriteFile(filepath.Join(root, "present.txt"), []byte("made\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Present, flagged: the flag tolerates an absent path, it does not license
	// dropping one that is there.
	dir := t.TempDir()
	s := &Server{LayerDir: dir}

	err = s.export(fixedHandle{root: root}, "present.txt", "out.txt", nil, true)
	if err != nil {
		t.Fatalf("a flagged save of a file that exists was skipped: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "exports", "out.txt"))
	if err != nil || string(body) != "made\n" {
		t.Errorf("the exported file is %q (%v), want the one on disk", body, err)
	}

	// Absent, flagged: reported as absence, distinctly enough for the host to
	// tell it from a failure and write nothing at all.
	dir = t.TempDir()
	s = &Server{LayerDir: dir}

	err = s.export(fixedHandle{root: root}, "absent.txt", "out.txt", nil, true)
	if !errors.Is(err, errArtifactAbsent) {
		t.Errorf("an absent path answered %v, want it reported as absent", err)
	}

	_, err = os.Lstat(filepath.Join(dir, "exports", "out.txt"))
	if err == nil {
		t.Error("an absent path still wrote its destination")
	}

	// A pattern matching nothing is the same answer, since a pattern's count is
	// the build's to decide and zero is one of the counts.
	err = s.export(fixedHandle{root: root}, "none-*", "out/", nil, true)
	if !errors.Is(err, errArtifactAbsent) {
		t.Errorf("a pattern matching nothing answered %v, want absent", err)
	}
}

// Without the flag an absent path is still the failure it has always been.
//
// The two answers travel the same return, so the sentinel must not leak into
// the unflagged path: that would turn every typo'd SAVE ARTIFACT into a build
// that quietly produces nothing.
func TestExportWithoutIfExistsStillRefusesAnAbsentPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := &Server{LayerDir: t.TempDir()}

	for _, path := range []string{"absent.txt", "none-*"} {
		err := s.export(fixedHandle{root: root}, path, "out.txt", nil, false)
		if err == nil {
			t.Errorf("%s was accepted without --if-exists", path)

			continue
		}

		if errors.Is(err, errArtifactAbsent) {
			t.Errorf("%s reported absence without --if-exists, so the skip is"+
				" available to a build that never asked for it", path)
		}
	}
}
