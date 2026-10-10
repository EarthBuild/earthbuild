package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An anchor that has been replaced by its own mutant says so.
//
// A sweep restores every file it touches - on the ordinary path, on a panic, and
// on SIGINT or SIGTERM, which the tool's own comments set out. **SIGKILL is
// none of those**, and a harness that times a sweep out sends one: the file is
// left mutated and the process is gone before it can put anything back (E495).
//
// `TestEveryAnchorStillMatchesItsSource` catches it, because a mutated file no
// longer contains its anchor - and reports "the code moved; fix the entry",
// which sends the reader to the catalogue. The source is what moved, and it
// moved because the tool put a mutant in it.
//
// The two are one grep apart: if the replacement is sitting where the anchor
// should be, a sweep was interrupted. **A diagnosis one word short of the cause
// is a diagnosis that sends people to the wrong file** - the third time in this
// session, after E478 and E479.
func TestNoMutantIsStillApplied(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	for _, m := range Mutants {
		if m.Replacement == "" {
			// A deletion leaves nothing to recognise, so this cannot speak for
			// it. Said rather than skipped silently: the guard covers the
			// mutants that put something in, and that is most of them.
			continue
		}

		src, err := os.ReadFile(filepath.Join(root, m.File))
		if err != nil {
			continue
		}

		text := string(src)
		if strings.Contains(text, m.Anchor) || !strings.Contains(text, m.Replacement) {
			continue
		}

		t.Errorf("%s is still applied to %s"+
			"\n  the anchor is gone and its replacement is there, which is a"+
			" sweep that was killed before it could restore the file"+
			"\n  git checkout %s, or put the anchor back by hand",
			m.Name, m.File, m.File)
	}
}
