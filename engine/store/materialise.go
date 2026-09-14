package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// Materialise writes the tree a Directory names into a directory.
//
// **What an action runs in.** A client sends its input root as a tree of
// Directory messages and the file blobs they name; this turns that back into a
// filesystem. Every byte comes from this store, verified against the name it is
// filed under - so a tree materialised here is the tree the client described,
// not whatever happens to be on the disk.
//
// The base image is not part of this. Under the practice every client follows,
// an input root holds the action's own inputs and the environment comes from a
// `container-image` platform property, so this writes over a base rather than
// replacing one.
//
// Refuses rather than guessing at anything missing: an action run over an
// incomplete input root produces a result that is wrong in a way nothing
// downstream can detect, and the client is entitled to be told it should have
// uploaded more.
func (d DirStore) Materialise(root ir.NodeID, into string) error {
	b, err := d.Node(root)
	if err != nil {
		return fmt.Errorf("the input root %s is not in this store: %w", root, err)
	}

	dir, err := layer.DirectoryIn(b)
	if err != nil {
		return fmt.Errorf("read the input root %s: %w", root, err)
	}

	if err := os.MkdirAll(into, 0o755); err != nil { //nolint:gosec // a directory an action works in
		return fmt.Errorf("make %s: %w", into, err)
	}

	for _, f := range dir.Files {
		if err := d.writeFile(f, filepath.Join(into, f.Name)); err != nil {
			return err
		}
	}

	for _, sub := range dir.Dirs {
		if err := d.Materialise(sub.Digest, filepath.Join(into, sub.Name)); err != nil {
			return err
		}
	}

	// **Symlinks last, so nothing is ever written through one.** A member name
	// is one path segment and appears once in a directory (layer.DirectoryIn
	// refuses anything else), so a sibling cannot already hold the name a link
	// takes. Writing them last means that if it ever could, the link would not
	// be there yet - the ordering costs nothing and does not depend on the
	// check above being right.
	for _, l := range dir.Links {
		// The target as given, never resolved: a symlink's meaning is the
		// string it holds, and following it here would bake this machine's
		// filesystem into the action's.
		if err := os.Symlink(l.Target, filepath.Join(into, l.Name)); err != nil {
			return fmt.Errorf("link %s: %w", l.Name, err)
		}
	}

	return nil
}

// writeFile puts one file's contents down with the mode it was sent under.
func (d DirStore) writeFile(f layer.Member, at string) error {
	b, err := d.Node(f.Digest)
	if err != nil {
		return fmt.Errorf(
			"%s names %s, which this store does not hold"+
				"\n  an action cannot run over an input root with a hole in it, and a"+
				"\n  result produced over one would be wrong in a way nothing can detect"+
				"\n  ask FindMissingBlobs before Execute, and send what it names",
			f.Name, f.Digest)
	}

	// **REAPI carries one mode bit; this engine carries the rest.** A file sent
	// by another tool has `is_executable` and nothing else, so 0755 and 0644
	// are the only modes it can mean. One sent by this engine may carry its own
	// mode as a property, and then that is what it had.
	mode := os.FileMode(0o644)
	if f.Executable {
		mode = 0o755
	}

	if f.Mode != 0 {
		mode = os.FileMode(f.Mode) & os.ModePerm
	}

	// **O_EXCL, not truncate: the file must not be there already.** It creates
	// nothing through a symlink, which is what makes writing into a directory
	// an earlier action left behind safe rather than hopeful; and a name that
	// is already taken is a tree describing two things at one path, which is
	// a question for the sender and not something to resolve by writing last.
	w, err := os.OpenFile(at, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) //nolint:gosec // the mode the sender asked for
	if err != nil {
		return fmt.Errorf("write %s: %w", at, err)
	}

	if _, err := w.Write(b); err != nil {
		_ = w.Close()

		return fmt.Errorf("write %s: %w", at, err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("write %s: %w", at, err)
	}

	return nil
}
