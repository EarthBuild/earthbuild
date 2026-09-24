package layer

import (
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// OutputFile is one file an action declared it produces.
type OutputFile struct {
	Path   string
	Digest ir.NodeID
	Size   int64
	// Executable is REAPI's single mode bit, which is all a conforming peer
	// carries. A script materialised without it cannot be run.
	Executable bool
}

// OutputDir is one directory an action declared it produces.
//
// Both digests, because peers differ about which they read: `root_directory_digest`
// names the tree by reference, `tree_digest` holds every child inline.
type OutputDir struct {
	Path     string
	Root     ir.NodeID
	RootSize int64
	Tree     ir.NodeID
	TreeSize int64
	// Nodes is the Directory messages beneath this path, for a caller that has
	// to keep the Tree blob somewhere a client can fetch it.
	Nodes map[ir.NodeID][]byte
}

// Declared is what an action produced, named the way REAPI names it.
type Declared struct {
	Files []OutputFile
	Dirs  []OutputDir
}

// Outputs names each path an action declared it produces.
//
// **A client asks about paths and is answered about paths.** This engine
// captures a filesystem, and handing the whole of it back as one unnamed
// directory gives a client everything it wanted and no way to tell which part
// is which - which buck2 reports as "Path is empty" while holding the answer.
//
// Derived from the manifest rather than recorded while capturing, because the
// same answer has to come out of a cache hit: nothing was captured there, and
// what is stored beside the layer is all there is. Recording it at capture
// would be faster and would only answer half the times it is asked.
//
// A declared path the action did not produce is omitted rather than named
// empty, which is REAPI's rule and the honest one - an entry for it would claim
// an artefact that is not there.
//
// Nothing declared names nothing, which is every ordinary step: its output is
// the filesystem it produced and there is no list to enumerate.
func Outputs(manifest []byte, declared []string) (Declared, error) {
	if len(declared) == 0 {
		return Declared{}, nil
	}

	entries, err := decodeManifest(manifest)
	if err != nil {
		return Declared{}, fmt.Errorf("read the manifest to name what was produced: %w", err)
	}

	files := make(map[string]entry, len(entries))
	for _, e := range entries {
		files[path.Clean(e.path)] = e
	}

	var out Declared

	// The tree, folded once and only where a directory was declared: a file
	// output needs nothing but the manifest, and most declared outputs are
	// files.
	var tree *Tree

	for _, raw := range declared {
		want := path.Clean(strings.TrimPrefix(strings.TrimSpace(raw), "/"))
		if want == "" || want == "." {
			continue
		}

		e, ok := files[want]

		switch {
		case ok && e.mode&uint32(os.ModeType) == 0 && !isDir(e.mode):
			out.Files = append(out.Files, OutputFile{
				Path:   raw,
				Digest: e.content,
				Size:   e.size,
				// 0o111 rather than any one bit: a file executable by its owner
				// and not its group is still an executable file, and REAPI has
				// one bit to say so.
				Executable: e.mode&0o111 != 0,
			})
		case ok:
			if tree == nil {
				t, ferr := foldOne(manifest)
				if ferr != nil {
					return Declared{}, ferr
				}

				tree = &t
			}

			dir, derr := dirOutput(*tree, raw, want)
			if derr != nil {
				return Declared{}, derr
			}

			out.Dirs = append(out.Dirs, dir)
		}
	}

	return out, nil
}

// isDir reports whether a manifest entry's mode is a directory's.
func isDir(mode uint32) bool { return os.FileMode(mode)&os.ModeDir != 0 }

// foldOne folds a manifest into the tree it describes.
func foldOne(manifest []byte) (Tree, error) {
	f := NewFold()
	if !f.Add(manifest) {
		return Tree{}, errors.New("the manifest could not be folded to name a directory")
	}

	return f.Tree(), nil
}

// dirOutput names one declared directory, and collects the nodes beneath it.
func dirOutput(t Tree, as, want string) (OutputDir, error) {
	node := t.Root()
	nodes := t.Nodes()

	// Descended rather than looked up: a tree is named by its root, and the
	// digest of a subdirectory is only knowable by reading the directory above
	// it. Two or three steps for any path a build declares.
	for seg := range strings.SplitSeq(want, "/") {
		d, err := DirectoryIn(nodes[node])
		if err != nil {
			return OutputDir{}, fmt.Errorf("read %s while naming %s: %w", node, as, err)
		}

		found := false

		for _, sub := range d.Dirs {
			if sub.Name == seg {
				node, found = sub.Digest, true

				break
			}
		}

		if !found {
			return OutputDir{}, fmt.Errorf(
				"%s is in the manifest and %q is not in the tree beneath it", as, seg)
		}
	}

	sub := map[ir.NodeID][]byte{}
	collect(nodes, node, sub)

	children := make([][]byte, 0, len(sub))

	for id, b := range sub {
		if id != node {
			children = append(children, b)
		}
	}

	// Sorted, because a map is not: two runs producing the same directory must
	// produce the same Tree message, or its digest is a different name for one
	// filesystem on every build.
	sort.Slice(children, func(i, j int) bool { return string(children[i]) < string(children[j]) })

	msg := EncodeTree(nodes[node], children)

	return OutputDir{
		Path:     as,
		Root:     node,
		RootSize: int64(len(nodes[node])),
		Tree:     ir.DigestOf(msg),
		TreeSize: int64(len(msg)),
		Nodes:    map[ir.NodeID][]byte{ir.DigestOf(msg): msg},
	}, nil
}

// collect gathers a node and everything beneath it.
func collect(all map[ir.NodeID][]byte, from ir.NodeID, into map[ir.NodeID][]byte) {
	if _, seen := into[from]; seen {
		return
	}

	b, ok := all[from]
	if !ok {
		return
	}

	into[from] = b

	kids, err := ChildDigests(b)
	if err != nil {
		return
	}

	for _, k := range kids {
		collect(all, k, into)
	}
}
