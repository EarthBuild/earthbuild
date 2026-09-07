package cache_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The cache directory is not world-readable.
//
// It holds what this machine has built and the keys those results are filed
// under, which is a record of what a developer works on. A directory this
// engine owns should be as tight as it can be; one that becomes part of an
// image, or that a user asked for an artifact in, is a different question and
// keeps its conventional mode.
func TestTheCacheDirectoryIsNotWorldReadable(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "store")

	_, err := cache.Open(root)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(filepath.Join(root, "actions"))
	if err != nil {
		// The layout is the package's business; what matters is that whatever
		// it made is tight.
		fi, err = os.Stat(root)
		if err != nil {
			t.Fatal(err)
		}
	}

	if perm := fi.Mode().Perm(); perm&0o007 != 0 {
		t.Errorf("the cache directory is %o, which lets anyone on the machine read it", perm)
	}
}

// Everything this package writes is tight, not just the action cache.
//
// The test above named `actions` because that was the only directory when it
// was written. `OpenProfiles` adds another, holding the list of paths a
// developer's builds read - which is a more detailed description of what they
// work on than the action cache is, not a less detailed one.
//
// So the guard walks what the package actually created rather than naming a
// directory. That is the E106 shape handled the right way round: a rule stated
// once, applied wherever it holds, and it notices the third store without
// anybody remembering to come back here.
func TestNothingThisPackageWritesIsWorldReadable(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "store")

	c, err := cache.Open(root)
	if err != nil {
		t.Fatal(err)
	}

	p, err := cache.OpenProfiles(root)
	if err != nil {
		t.Fatal(err)
	}

	// Written to, because an empty directory proves only that MkdirAll took a
	// mode - the files are where the paths actually are.
	c.Put(classOf(9), core.Entry{Layer: ir.NodeID{9}, Writer: testKey})
	p.Put(classOf(9), sample())

	var checked int

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		fi, err := d.Info()
		if err != nil {
			return err
		}

		checked++

		if perm := fi.Mode().Perm(); perm&0o007 != 0 {
			t.Errorf("%s is %o, which lets anyone on the machine read it",
				strings.TrimPrefix(path, root), perm)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// A walk that found nothing would pass silently, which is how a guard
	// becomes decoration.
	if checked < 4 {
		t.Errorf("only %d entries were checked; the stores wrote less than expected", checked)
	}
}
