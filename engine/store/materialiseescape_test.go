package store_test

import (
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A tree cannot write outside the directory it is materialised into.
//
// **The input root is the client's, and the client is not this engine.** A step
// running Buck2 uploads its own Directory messages; the store verifies that the
// bytes hash to the name they arrived under, which says nothing at all about
// what the names inside them mean. A member called `../escaped.txt` hashes
// correctly and lands one directory up.
//
// Asserted on the filesystem rather than on the error, because the error is
// only evidence: what this forbids is the write, and a refusal that arrives
// after one has landed is not a refusal.
func TestAnInputRootCannotWriteOutsideItself(t *testing.T) {
	// Not parallel: SelectHashForTest changes a process-wide choice.
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	for name, build := range map[string]func(*testing.T, store.DirStore, string) ir.NodeID{
		"a file named ../":    escapingFile,
		"a subdir named ../":  escapingDir,
		"a symlink named ../": escapingLink,
		"a name used twice":   collidingNames,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			st := store.DirStore(filepath.Join(root, "store"))

			// `into` is one level down, so an escape has somewhere to go and
			// leaves evidence to look for.
			outside := filepath.Join(root, "outside")
			if err := os.MkdirAll(outside, 0o755); err != nil {
				t.Fatal(err)
			}

			id := build(t, st, outside)

			if err := st.Materialise(id, filepath.Join(root, "into")); err == nil {
				t.Error("a tree that reaches outside itself was materialised")
			}

			// Everything under `root` bar the store and the input root itself
			// is evidence of an escape. Checked over the whole tree rather
			// than at the one place an escape was aimed, because where it
			// lands is the attacker's choice and not the test's.
			for _, at := range []string{outside, root} {
				left, err := os.ReadDir(at)
				if err != nil {
					t.Fatal(err)
				}

				for _, e := range left {
					if at == root && (e.Name() == "store" || e.Name() == "into" || e.Name() == "outside") {
						continue
					}

					t.Errorf("%s/%s was written outside the input root",
						filepath.Base(at), e.Name())
				}
			}
		})
	}
}

func escapingFile(t *testing.T, st store.DirStore, _ string) ir.NodeID {
	t.Helper()

	return keep(t, st, dirMsg(
		[]node{{name: "../escaped.txt", digest: keep(t, st, []byte("landed"))}}, nil, nil))
}

func escapingDir(t *testing.T, st store.DirStore, _ string) ir.NodeID {
	t.Helper()

	sub := keep(t, st, dirMsg(nil, nil, nil))

	return keep(t, st, dirMsg(nil, []node{{name: "../escaped", digest: sub}}, nil))
}

func escapingLink(t *testing.T, st store.DirStore, outside string) ir.NodeID {
	t.Helper()

	return keep(t, st, dirMsg(nil, nil,
		[]node{{name: "../escaped.link", target: outside}}))
}

// collidingNames plants a symlink pointing outside and then a directory of the
// same name, so the directory's contents are written through the symlink.
func collidingNames(t *testing.T, st store.DirStore, outside string) ir.NodeID {
	t.Helper()

	inner := keep(t, st, dirMsg(
		[]node{{name: "escaped.txt", digest: keep(t, st, []byte("landed"))}}, nil, nil))

	return keep(t, st, dirMsg(nil,
		[]node{{name: "x", digest: inner}},
		[]node{{name: "x", target: outside}}))
}

// keep files a blob under its own name, which is what makes it retrievable and
// is the only thing this store checks about it.
func keep(t *testing.T, st store.DirStore, b []byte) ir.NodeID {
	t.Helper()

	id := ir.DigestOf(b)
	if err := st.Accept(id, b); err != nil {
		t.Fatal(err)
	}

	return id
}

type node struct {
	name   string
	digest ir.NodeID
	target string
}

// dirMsg encodes a Directory by hand.
//
// **Not through this engine's encoder, on purpose.** That one walks a trie keyed
// on path segments, so it cannot produce a name with a separator in it - which
// is exactly the message this test is about. These are the bytes a peer sends.
func dirMsg(files, dirs, links []node) []byte {
	var out []byte

	for _, n := range files {
		out = field(out, 1, member(n)) // Directory.files
	}

	for _, n := range dirs {
		out = field(out, 2, member(n)) // Directory.directories
	}

	for _, n := range links {
		out = field(out, 3, member(n)) // Directory.symlinks
	}

	return out
}

func member(n node) []byte {
	b := field(nil, 1, []byte(n.name)) // *Node.name

	if n.target != "" {
		return field(b, 2, []byte(n.target)) // SymlinkNode.target
	}

	// FileNode.digest / DirectoryNode.digest, whose hash is lowercase hex.
	return field(b, 2, field(nil, 1, []byte(hex.EncodeToString(n.digest[:]))))
}

// field appends one length-delimited protobuf field.
func field(b []byte, num int, v []byte) []byte {
	b = binary.AppendUvarint(b, uint64(num)<<3|2)
	b = binary.AppendUvarint(b, uint64(len(v)))

	return append(b, v...)
}
