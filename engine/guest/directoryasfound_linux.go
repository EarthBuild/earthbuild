//go:build linux

package guest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fstime"
)

// directoryAsFound is a directory's contents and time, from before this engine
// made a mount point inside it.
//
// **A directory's mtime is a record of entries arriving and leaving.** So the
// question of whether the engine owes it a time back is exact rather than a
// guess: if it holds the same names afterwards as it held before, nothing that
// outlived the step happened to it, and the time it carries is the engine's
// doing rather than the step's. If the names differ, the step changed what the
// directory contains and the change is the step's - restoring the time then
// would hide what the build did.
//
// The comparison is by name and not by count. A step that adds one file and
// removes another leaves the count alone and has genuinely changed the
// directory, and this must not put the clock back on it.
type directoryAsFound struct {
	mtime time.Time
	path  string
	// watch is where the entry names are read: the step's delta when there is
	// one, and the directory itself when there is not.
	//
	// **The delta answers the same question for a fraction of the work.** The
	// question is whether the *step* changed this directory, and a step's
	// writes land in the delta - so an unchanged delta is an unchanged
	// directory, whatever the layers beneath it hold. Reading the directory
	// itself means merging it across every lower layer, twice per step, which
	// was 14.2ms of a 39.5ms step (E639).
	watch string
	names string
}

// deltaOf is where a directory's own writes land, or "" when that is not known.
//
// Empty for a root that is not an overlay - a prepared root, or a test with a
// plain directory - and the caller then watches the directory itself, which is
// what it did before this existed.
func deltaOf(root, delta, dir string) string {
	if delta == "" {
		return ""
	}

	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}

	return filepath.Join(delta, rel)
}

// findDirectory reads a directory as it stands, or reports that it cannot.
//
// Not an error: this is only ever an improvement to a layer's identity, and a
// directory that cannot be read here is one whose time is left alone - which is
// the behaviour there was before any of this.
func findDirectory(path, watch string) (directoryAsFound, bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() {
		return directoryAsFound{}, false
	}

	where := watch
	if where == "" {
		where = path
	}

	names, ok := watchedNames(where, where != path)
	if !ok {
		return directoryAsFound{}, false
	}

	return directoryAsFound{path: path, watch: watch, names: names, mtime: fi.ModTime()}, true
}

// watchedNames is entryNames, treating a missing delta as an empty one.
//
// A delta has no entry for a directory the step has not written in, and that
// absence is the answer rather than a failure. A missing directory on the
// other path is a directory that has gone, which is not.
func watchedNames(where string, isDelta bool) (string, bool) {
	names, ok := entryNames(where)
	if ok {
		return names, true
	}

	if isDelta {
		if _, err := os.Stat(where); errors.Is(err, fs.ErrNotExist) {
			return "", true
		}
	}

	return "", false
}

// restore puts the directory's time back, if it ends holding what it began with.
func (d directoryAsFound) restore() {
	where := d.watch
	if where == "" {
		where = d.path
	}

	names, ok := watchedNames(where, where != d.path)
	if !ok || names != d.names {
		return
	}

	_ = fstime.Lchtimes(d.path, d.mtime, d.mtime)
}

// entryNames is a directory's entry names, in one comparable string.
//
// Sorted, because readdir order is the filesystem's business and comparing two
// unsorted listings would restore a time or not depending on how the kernel
// felt - which is exactly the kind of thing this mechanism exists to keep out
// of a layer's identity (I12).
func entryNames(path string) (string, bool) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", false
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	sort.Strings(names)

	// A separator no filename can contain, so two listings cannot be confused
	// by one holding a name that spells another pair.
	return strings.Join(names, "\x00"), true
}
