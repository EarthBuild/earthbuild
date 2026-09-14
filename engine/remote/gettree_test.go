package remote_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A client can walk a whole tree in one call.
//
// **The alternative is a round trip per level.** Asking for a directory by
// digest and discovering the next level from what comes back costs one call for
// each level of nesting; this costs one. Bazel reaches for it where buck2 reads
// the inline Tree, and a service that wants both clients answers both.
func TestGetTreeWalksEveryDirectoryOnce(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	dir := t.TempDir()
	writeUnder(t, dir, map[string]string{
		"top.txt":            "a",
		"one/x.txt":          "b",
		"one/deep/y.txt":     "c",
		"two/z.txt":          "d",
		"two/deeper/w/q.txt": "e",
	})

	m, err := layer.Manifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	f := layer.NewFold()
	if !f.Add(m) {
		t.Fatal("the manifest did not fold")
	}

	tree := f.Tree()

	st := store.DirStore(t.TempDir())
	if err := st.NoteNodes(tree); err != nil {
		t.Fatal(err)
	}

	got := walkTree(t, dialService(t, st), tree.Root())

	// Every directory of the tree, and each of them once: a tree may name one
	// directory from two places - that is what naming by content means - and
	// sending it twice would have a client assemble it twice.
	if len(got) != len(tree.Nodes()) {
		t.Errorf("walked %d directories and the tree holds %d", len(got), len(tree.Nodes()))
	}

	for id := range tree.Nodes() {
		if !got[id] {
			t.Errorf("%v is in the tree and was not walked", id)
		}
	}
}

// A tree this store does not hold is a miss, not a broken service.
func TestGetTreeOfAnAbsentRootIsNotFound(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	conn := dialService(t, store.DirStore(t.TempDir()))

	stream, err := conn.NewStream(context.Background(),
		&grpc.StreamDesc{ServerStreams: true},
		"/build.bazel.remote.execution.v2.ContentAddressableStorage/GetTree")
	if err != nil {
		t.Fatal(err)
	}

	ask := layer.EncodeGetTreeForTest(ir.DigestOf([]byte("a tree nobody sent")))
	if err := stream.SendMsg(&ask); err != nil {
		t.Fatal(err)
	}

	_ = stream.CloseSend()

	var out []byte
	if err := stream.RecvMsg(&out); status.Code(err) != codes.NotFound {
		t.Errorf("walking an absent tree answered %v", status.Code(err))
	}
}

// walkTree asks for a tree and reports which directories came back.
func walkTree(t *testing.T, conn *grpc.ClientConn, root ir.NodeID) map[ir.NodeID]bool {
	t.Helper()

	stream, err := conn.NewStream(context.Background(),
		&grpc.StreamDesc{ServerStreams: true},
		"/build.bazel.remote.execution.v2.ContentAddressableStorage/GetTree")
	if err != nil {
		t.Fatal(err)
	}

	ask := layer.EncodeGetTreeForTest(root)
	if err := stream.SendMsg(&ask); err != nil {
		t.Fatal(err)
	}

	_ = stream.CloseSend()

	out := map[ir.NodeID]bool{}

	for {
		var page []byte

		err := stream.RecvMsg(&page)
		if errors.Is(err, io.EOF) {
			return out
		}

		if err != nil {
			t.Fatalf("GetTree: %v", err)
		}

		dirs, err := layer.DirsInGetTreeResponse(page)
		if err != nil {
			t.Fatal(err)
		}

		for _, d := range dirs {
			id := ir.DigestOf(d)
			if out[id] {
				t.Errorf("%v was sent twice", id)
			}

			out[id] = true
		}
	}
}

// writeUnder puts a set of files down, making the directories they need.
func writeUnder(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for p, content := range files {
		at := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(at, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
