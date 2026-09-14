package layer

import (
	"bytes"
	"io/fs"
	"os/exec"
	"strings"
	"testing"
)

func regular(path string, mode uint32, size int64) entry {
	return entry{path: path, mode: mode, size: size, content: contentFor(path)}
}

func contentFor(p string) (id [32]byte) {
	copy(id[:], p)

	return id
}

// A root-owned tree of ordinary modes emits no properties at all.
//
// **This is the whole point of putting the extras in NodeProperties.** For any
// tree Bazel or Buck2 would construct there is nothing to say: ownership is not
// modelled by REAPI at all (a worker materialises as itself), and 644/755 is
// exactly `is_executable`. So the field is absent, protobuf writes nothing, and
// our bytes are theirs. A tree that emitted `uid: 0` on every file would agree
// with nobody.
func TestAnOrdinaryTreeCarriesNoProperties(t *testing.T) {
	t.Parallel()

	f := NewFold()
	for _, e := range []entry{
		regular("src/main.go", 0o644, 12),
		regular("src/run.sh", 0o755, 30),
		regular("README.md", 0o644, 7),
	} {
		f.merged[e.path] = e
		f.resync(e.path)
	}

	tree := f.Tree()

	for id, b := range tree.Nodes() {
		// fieldFileProps and fieldLinkProps are the only places a property can
		// appear, and neither should have been written.
		if strings.Contains(string(b), "earthbuild.") {
			t.Errorf("directory %v carries a property for a tree that has"+
				"\n  nothing to say: %q", id, b)
		}
	}

	if len(tree.Nodes()) != 2 {
		t.Errorf("%d directories, want 2 (the root and src)", len(tree.Nodes()))
	}

	if _, ok := tree.Nodes()[tree.Root()]; !ok {
		t.Error("the root digest names no blob, so a peer given it cannot ask for it")
	}
}

// Ownership and an unusual mode are carried, because they are real.
func TestWhatReapiCannotModelIsCarriedAsProperties(t *testing.T) {
	t.Parallel()

	f := NewFold()

	e := regular("secret.pem", 0o600, 9)
	e.uid, e.gid = 501, 20
	f.merged[e.path] = e
	f.resync(e.path)

	tree := f.Tree()

	blob := string(tree.Nodes()[tree.Root()])
	for _, want := range []string{"earthbuild.uid", "501", "earthbuild.gid", "20", "earthbuild.mode"} {
		if !strings.Contains(blob, want) {
			t.Errorf("the root directory does not carry %q:\n  %q", want, blob)
		}
	}
}

// A child's digest is what the parent names it by.
func TestAParentNamesItsChildByDigest(t *testing.T) {
	t.Parallel()

	build := func(body string) Tree {
		t.Helper()

		f := NewFold()

		e := regular("sub/f.txt", 0o644, int64(len(body)))
		copy(e.content[:], body)
		f.merged[e.path] = e
		f.resync(e.path)

		return f.Tree()
	}

	one, two := build("one"), build("two")

	if one.Root() == two.Root() {
		t.Fatal("changing a file deep in the tree did not change the root")
	}

	// The root blob must literally contain the child's digest, or it is not a
	// Merkle tree - a peer walking down from the root would have nothing to ask
	// for next.
	var child string

	for id := range one.Nodes() {
		if id != one.Root() {
			child = id.String()
		}
	}

	if !strings.Contains(string(one.Nodes()[one.Root()]), child) {
		t.Error("the root does not name its subdirectory's digest")
	}
}

// Every list is emitted in name order.
//
// **Required by REAPI, and a digest difference if we get it wrong.** A peer
// serialising the same directory sorts, so an unsorted one is not merely
// invalid - it is a different Directory, naming nothing the other side holds.
// Map iteration in Go is deliberately unordered, so this is the property most
// likely to be broken by an edit that looks harmless.
func TestEveryListIsInNameOrder(t *testing.T) {
	t.Parallel()

	f := NewFold()

	// Inserted in an order that is neither sorted nor reverse-sorted.
	for _, p := range []string{
		"zeta.txt", "alpha.txt", "middle.txt", "beta.txt",
		"zdir/x.txt", "adir/x.txt", "mdir/x.txt",
	} {
		e := regular(p, 0o644, 1)
		f.merged[p] = e
		f.resync(p)
	}

	tree := f.Tree()

	root := string(tree.Nodes()[tree.Root()])

	for _, names := range [][]string{
		{"alpha.txt", "beta.txt", "middle.txt", "zeta.txt"}, // files
		{"adir", "mdir", "zdir"},                            // directories
	} {
		at := -1

		for _, n := range names {
			i := strings.Index(root, n)
			if i < 0 {
				t.Fatalf("%q is not in the root directory at all", n)
			}

			if i < at {
				t.Errorf("%q appears before the entry that should precede it"+
					"\n  REAPI requires each list in name order, and a peer that"+
					"\n  sorts would compute a different digest for this tree", n)
			}

			at = i
		}
	}
}

// Every Directory we emit is a Directory protoc will read back.
//
// **The encoder test checks one hand-written message; this checks the tree.**
// The walk assembles messages from a trie, and a field written into the wrong
// one - a property on a DirectoryNode, a target on a file - produces bytes that
// are still valid protobuf and still digest to something. protoc decoding them
// against the real schema is the check that the structure is what we think,
// made by something that is not us.
//
// Skipped where protoc is absent, because a missing tool is not a failing
// engine - but the skip says so rather than passing quietly.
func TestProtocReadsBackEveryDirectoryWeEmit(t *testing.T) {
	t.Parallel()

	protoc, err := exec.LookPath("protoc")
	if err != nil {
		t.Skip("protoc is not installed, so the emitted messages are unverified here")
	}

	f := NewFold()

	link := entry{path: "sub/link", mode: uint32(fs.ModeSymlink) | 0o777, link: "../top.txt"}
	odd := regular("sub/secret.pem", 0o600, 9)
	odd.uid, odd.gid = 501, 20

	for _, e := range []entry{
		regular("top.txt", 0o644, 4),
		regular("sub/run.sh", 0o755, 30),
		odd,
		link,
	} {
		f.merged[e.path] = e
		f.resync(e.path)
	}

	tree := f.Tree()

	if len(tree.Nodes()) != 2 {
		t.Fatalf("%d directories, want 2", len(tree.Nodes()))
	}

	for id, b := range tree.Nodes() {
		cmd := exec.Command(protoc,
			"--proto_path=testdata/reapi",
			"--decode=build.bazel.remote.execution.v2.Directory",
			"testdata/reapi/reapi_min.proto")
		cmd.Stdin = bytes.NewReader(b)

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("protoc could not read directory %v back:\n  %s\n  bytes: %x",
				id, out, b)

			continue
		}

		t.Logf("directory %v decodes to:\n%s", id.String()[:12], out)
	}
}
