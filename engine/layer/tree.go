package layer

import (
	"path"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// dir is a directory being assembled from paths.
type dir struct {
	// own is the directory's own entry, where the layer recorded one. A tar
	// need not carry an entry for a directory it only implies, so a node says
	// whether it had one rather than inventing default metadata - which would
	// make two layers agree that differed.
	own     *entry
	files   map[string]entry
	subdirs map[string]*dir

	// digest is what this directory was last named, and clean whether that is
	// still true. A fold carried across Add dirties only the path it moved and
	// the directories above it, so an untouched subtree is never named twice.
	digest ir.NodeID
	size   int64 // the serialised length, which a parent's DirectoryNode carries
	clean  bool
}

func newDir() *dir {
	return &dir{files: map[string]entry{}, subdirs: map[string]*dir{}}
}

// insert places an entry at a path, dirtying every directory above it.
func (d *dir) insert(parts []string, e entry) {
	d.clean = false

	name := parts[0]

	if len(parts) > 1 {
		next, ok := d.subdirs[name]
		if !ok {
			next = newDir()
			d.subdirs[name] = next
		}

		next.insert(parts[1:], e)

		return
	}

	if kindOf(e.mode) == 'd' {
		next, ok := d.subdirs[name]
		if !ok {
			next = newDir()
			d.subdirs[name] = next
		}

		own := e
		next.own = &own
		next.clean = false

		// A name cannot be a file and a directory at once, and a later layer
		// replacing one with the other must not leave the old behind.
		delete(d.files, name)

		return
	}

	delete(d.subdirs, name)

	d.files[name] = e
}

// remove takes a path out, prunes what it empties, and dirties what is above.
//
// A directory that loses its own entry but keeps children stays: the fold's
// merged set can hold `a/b.txt` with nothing recorded for `a`, and the node then
// says it had no entry rather than inventing one.
func (d *dir) remove(parts []string) {
	d.clean = false

	name := parts[0]

	if len(parts) > 1 {
		next, ok := d.subdirs[name]
		if !ok {
			return
		}

		next.remove(parts[1:])

		if next.empty() {
			delete(d.subdirs, name)
		}

		return
	}

	delete(d.files, name)

	if next, ok := d.subdirs[name]; ok {
		next.own = nil
		next.clean = false

		if next.empty() {
			delete(d.subdirs, name)
		}
	}
}

// empty reports a directory nothing records and nothing lives under.
func (d *dir) empty() bool {
	return d.own == nil && len(d.files) == 0 && len(d.subdirs) == 0
}

// rootOf assembles the directory trie of a merged set.
//
// Used where there is no carried fold to extend - a capture, which walks a tree
// once and has nothing to reuse.
func rootOf(merged map[string]entry) *dir {
	root := newDir()

	for p, e := range merged {
		root.insert(strings.Split(path.Clean(p), "/"), e)
	}

	return root
}
