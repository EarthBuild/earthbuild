package exec

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ignore"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// packContextInto writes the build context straight into a tarball.
//
// **One pass over the tree instead of two.** The route it replaces copies the
// context into a staging directory and then reads all of it back to pack it;
// measured on 2000 files that is 350ms of copying in front of 154ms of packing,
// against 152ms to pack alone (E829c). The copy is the whole of the difference,
// and on macOS it is worse still - the same file-creation cost that made moving
// the store onto the guest's ext4 worth a third of a cold build (E829b).
//
// **The names are what the guest receives**, so they are built to match what
// staging produced: the content of `<root>/<sub>` appears as `<sub>/...`, with
// an entry for `<sub>` itself and for each directory inside it. `packOne` reads
// `<root>/<name>` for an entry named `<name>`, so passing the context root and
// prefixed names gives both without rewriting anything.
//
// Sorted for the same reason `Pack` sorts: a layer's digest is over the archive,
// so the same tree has to produce the same bytes.
func packContextInto(root, sub string, ex excluder, at string) error {
	src := filepath.Join(root, filepath.FromSlash(sub))

	names, err := selectedUnder(root, src, sub, ex)
	if err != nil {
		return err
	}

	f, err := os.Create(at) //nolint:gosec // a path this engine derived
	if err != nil {
		return fmt.Errorf("make room for the packed context: %w", err)
	}

	defer f.Close()

	_, _, err = image.PackSelected(root, names, f)
	if err != nil {
		return fmt.Errorf("pack the build context: %w", err)
	}

	return f.Close()
}

// selectedUnder is every entry the context contributes, named as the archive
// will name it.
//
// The excluder is asked about the *absolute* path, which is what `ignore.For`
// builds its matcher against, while the name that goes into the archive is
// relative to the context root. A directory the excluder refuses is not
// descended into: an ignore rule naming a directory means the tree under it, and
// walking it to reject each child would be the cost this function exists to
// avoid.
func selectedUnder(root, src, sub string, ex excluder) ([]string, error) {
	var names []string

	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if ex != nil && ex.Excludes(p) {
			if d.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}

		names = append(names, filepath.ToSlash(rel))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the build context at %s: %w", src, err)
	}

	// The directories above the context's own root are entries too: staging
	// created them with MkdirAll and packing the staging directory carried them.
	for dir := filepath.Dir(filepath.FromSlash(sub)); ; dir = filepath.Dir(dir) {
		if dir == "." || dir == string(filepath.Separator) {
			break
		}

		names = append(names, filepath.ToSlash(dir))
	}

	sort.Strings(names)

	return names, nil
}

// EnvDirectContextPack packs the build context where it lies instead of copying
// it into a staging directory first.
//
// **On, because the copy was the whole of the cost.** Staging reads the tree
// into a directory and then reads that directory back to pack it; packing where
// it lies does the work once. A 2000-file context goes from 2404ms to 1415ms and
// a nested one with an ignore file from 1611ms to 1034ms, five pairs and three,
// ranges disjoint, with the guest receiving an identical filesystem both ways -
// same file count, same digest of the listing (E829d).
//
// **What it changes besides the time is hardlinks.** Staging copies file
// contents, so two names sharing an inode arrive as two independent files;
// packing the context sees the inode twice and writes the second as a link,
// which is what `packOne` has always done for layers. More faithful, and a
// different archive - so a context containing hardlinks gets a different layer
// digest and misses the cache once, after which it is cheaper to carry.
//
// Everything else is byte-identical, entry by entry, name, type, mode and link
// target: `TestPackingStraightFromTheContextCarriesTheSameThing`.
//
// This path is only taken when the store is in the VM, so on Linux the switch
// does nothing - measured at 1138ms against 1118ms, which is noise.
//
// `EARTH_DIRECT_CONTEXT_PACK=0` goes back to staging.
const EnvDirectContextPack = "EARTH_DIRECT_CONTEXT_PACK"

func directContextPack() bool {
	switch os.Getenv(EnvDirectContextPack) {
	case "", "1", "true", "yes":
		return true
	default:
		return false
	}
}

// packContextDirect writes the node's context into the tarball without staging.
func (e *Executor) packContextDirect(n *ir.Node, at string) error {
	root := n.Meta.ContextRoot
	if root == "" {
		root = e.Context
	}

	sub := filepath.Clean("/" + n.Op.Args[0])
	src := filepath.Join(root, sub)

	fi, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("build context %s (%s): %w", n.Op.Args[0], n.Meta.Source, err)
	}

	// A single file has no tree to walk and no ignore file to consult: staging
	// copied it and packed the one entry, and this packs the one entry.
	if !fi.IsDir() {
		return packOneContextFile(root, sub, at)
	}

	return packContextInto(root, sub, ignore.For(root, src), at)
}

// packOneContextFile packs a context that is a single file rather than a tree.
func packOneContextFile(root, sub, at string) error {
	f, err := os.Create(at) //nolint:gosec // a path this engine derived
	if err != nil {
		return fmt.Errorf("make room for the packed context: %w", err)
	}

	defer f.Close()

	names := []string{filepath.ToSlash(strings.TrimPrefix(sub, string(filepath.Separator)))}

	for dir := filepath.Dir(filepath.FromSlash(sub)); ; dir = filepath.Dir(dir) {
		if dir == "." || dir == string(filepath.Separator) {
			break
		}

		names = append(names, filepath.ToSlash(strings.TrimPrefix(dir, string(filepath.Separator))))
	}

	sort.Strings(names)

	_, _, err = image.PackSelected(root, names, f)
	if err != nil {
		return fmt.Errorf("pack the build context: %w", err)
	}

	return f.Close()
}
