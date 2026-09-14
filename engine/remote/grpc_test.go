package remote_test

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/remote"
	"github.com/EarthBuild/earthbuild/engine/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// dialService starts the service and returns a client speaking to it.
func dialService(t *testing.T, st store.DirStore) *grpc.ClientConn {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	g := grpc.NewServer(grpc.ForceServerCodec(remote.Codec()))
	(&remote.Service{Cache: &remote.Cache{Store: st}}).Register(g)

	go func() { _ = g.Serve(ln) }()
	t.Cleanup(g.Stop)

	conn, err := grpc.NewClient(ln.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(remote.Codec())))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// call sends one unary request and returns the bytes that came back.
func call(t *testing.T, conn *grpc.ClientConn, method string, in []byte) []byte {
	t.Helper()

	var out []byte
	if err := conn.Invoke(context.Background(), method, &in, &out); err != nil {
		t.Fatalf("%s: %v", method, err)
	}

	return out
}

// The service says which digest function this store was built with.
//
// **The first thing any client asks**, and the first chance to end the
// conversation by being wrong about the wire.
func TestAClientLearnsTheDigestFunction(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	conn := dialService(t, store.DirStore(t.TempDir()))

	got := call(t, conn,
		"/build.bazel.remote.execution.v2.Capabilities/GetCapabilities", nil)

	want := layer.EncodeCapabilities(layer.DigestFunctionSHA256, 4<<20)
	if string(got) != string(want) {
		t.Errorf("capabilities came back as %x, want %x", got, want)
	}
}

// A client asks which blobs to send and is told only those.
//
// **The question the whole tree exists to answer.** A peer holding all but one
// directory is told about the one, which is the difference between shipping a
// base and shipping a directory.
func TestAClientIsToldOnlyWhatItMustSend(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	root := t.TempDir()
	st := store.DirStore(root)

	f := layer.NewFold()
	if !f.Add(manifestOf(t, map[string]string{"a.txt": "one", "sub/b.txt": "two"})) {
		t.Fatal("the manifest did not fold")
	}

	tree := f.Tree()
	if err := st.NoteNodes(tree); err != nil {
		t.Fatal(err)
	}

	held := make([]ir.NodeID, 0, len(tree.Nodes()))
	for d := range tree.Nodes() {
		held = append(held, d)
	}

	absent := ir.NodeID{0xde, 0xad}

	conn := dialService(t, st)

	out := call(t, conn,
		"/build.bazel.remote.execution.v2.ContentAddressableStorage/FindMissingBlobs",
		askFor(append([]ir.NodeID{absent}, held...)))

	got, err := layer.DigestsInResponse(out)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0] != absent {
		t.Errorf("told to send %v, and the store holds everything but %v"+
			"\n  a peer told to send what it already has is a peer sending a base"+
			"\n  where a directory would do", got, absent)
	}
}

// askFor is a FindMissingBlobs request naming these digests.
func askFor(ids []ir.NodeID) []byte {
	return layer.EncodeFindMissingBlobs(ids)
}

// The codec calls itself what a client expects to negotiate.
//
// **A pin, and it says so.** A client sends protobuf and asks for "proto";
// these *are* protobuf bytes, so the codec is a no-op over them rather than a
// different format, and announcing anything else would have every conforming
// client refuse a service that speaks their language perfectly.
//
// It cannot be tested through a client here, because a client using the real
// proto codec needs generated messages - which is the dependency this whole
// approach exists to avoid. The mutation sweep found the gap: renaming it
// consistently on both sides of our own test passes, and would fail against
// anybody else.
func TestTheCodecAnnouncesProto(t *testing.T) {
	t.Parallel()

	if got := remote.Codec().Name(); got != "proto" {
		t.Errorf("the codec announces %q; a client negotiating \"proto\" would"+
			" refuse a service that sends exactly what it asked for", got)
	}
}

// A client sends a blob and the store keeps it; a wrong one is refused by name.
//
// **Writes are accepted here and refused over HTTP, and that is not
// inconsistency.** The HTTP cache is filled by builds, where an upload is a
// stranger's claim about what a name means. This service exists for a client
// inside a step this engine started, which must send its input root before
// anything can run over it - and the sandbox is the only boundary there is.
func TestAClientSendsBlobsAndTheWrongOneIsRefused(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	st := store.DirStore(t.TempDir())
	conn := dialService(t, st)

	good := []byte("an input file")
	goodID := ir.DigestOf(good)
	liar := ir.NodeID{0xba, 0xd0}

	out := call(t, conn,
		"/build.bazel.remote.execution.v2.ContentAddressableStorage/BatchUpdateBlobs",
		layer.EncodeBatchUpdateBlobsForTest([]layer.Upload{
			{Digest: goodID, Data: good},
			{Digest: liar, Data: good},
		}))

	if len(out) == 0 {
		t.Fatal("no per-blob results came back, so a client cannot tell which landed")
	}

	// The honest one is there and readable.
	back, err := st.Node(goodID)
	if err != nil {
		t.Fatalf("the blob that named itself was not kept: %v", err)
	}

	if string(back) != string(good) {
		t.Errorf("kept %q, sent %q", back, good)
	}

	// The liar is not.
	if missing := st.MissingNodes([]ir.NodeID{liar}); len(missing) != 1 {
		t.Error("a blob whose bytes do not name it was filed under the name" +
			" its sender chose, so every later reader is told these are the" +
			" bytes it asked for")
	}
}

// A client reads back a blob it sent, and is told plainly about one that is not
// there.
func TestAClientReadsBlobsAndIsToldAboutAMiss(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	st := store.DirStore(t.TempDir())
	conn := dialService(t, st)

	body := []byte("a directory message")
	id := ir.DigestOf(body)

	if err := st.Accept(id, body); err != nil {
		t.Fatal(err)
	}

	absent := ir.NodeID{0xab, 0x5e}

	out := call(t, conn,
		"/build.bazel.remote.execution.v2.ContentAddressableStorage/BatchReadBlobs",
		layer.EncodeBatchReadBlobsForTest([]ir.NodeID{id, absent}))

	reads, err := layer.ReadsInResponse(out)
	if err != nil {
		t.Fatal(err)
	}

	if len(reads) != 2 {
		t.Fatalf("%d results for two requests", len(reads))
	}

	if string(reads[0].Data) != string(body) || reads[0].Code != 0 {
		t.Errorf("the stored blob came back as %q code %d", reads[0].Data, reads[0].Code)
	}

	// **An absent blob must say so, not merely arrive empty.** Both carry the
	// digest and neither carries data, so without the status a client takes "I
	// do not have it" for "it is zero bytes long" - which is a valid file, and
	// the difference between rebuilding and using nothing.
	if reads[1].Code != layer.StatusNotFound {
		t.Errorf("a blob this store does not hold came back with status %d and"+
			" %d bytes, which a client cannot tell from an empty file",
			reads[1].Code, len(reads[1].Data))
	}
}

// An action this engine recorded is a result a client can fetch.
//
// **The key is the Action digest**, so the number a client asks under is the
// number this engine derived.
func TestAClientFetchesAnActionResult(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	root := t.TempDir()
	st := store.DirStore(root)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.txt"), []byte("built"), 0o600); err != nil {
		t.Fatal(err)
	}

	took, err := layer.Take(dir)
	if err != nil {
		t.Fatal(err)
	}

	m, err := layer.Manifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(store.LayerStore(root).Path(took.ID), 0o750); err != nil {
		t.Fatal(err)
	}

	store.NoteManifest(root, took.ID, m)

	key := core.Key{0x1a, 0x2b}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	g := grpc.NewServer(grpc.ForceServerCodec(remote.Codec()))
	(&remote.Service{Cache: &remote.Cache{
		Store: st,
		Actions: oneEntry{key: key, e: core.Entry{
			Layer: took.ID, Content: took.Content,
			Stdout: "three files\n", StdoutWhole: true,
		}},
	}}).Register(g)

	go func() { _ = g.Serve(ln) }()
	t.Cleanup(g.Stop)

	conn, err := grpc.NewClient(ln.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(remote.Codec())))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	out := call(t, conn,
		"/build.bazel.remote.execution.v2.ActionCache/GetActionResult",
		layer.EncodeGetActionResultForTest(ir.NodeID(key)))

	if !bytes.Contains(out, []byte(took.Content.String())) {
		t.Errorf("the result does not name the tree the step produced: %x", out)
	}

	// **And what the step printed travels with it.** An ActionResult carries
	// stdout and a client displays it, which is why R4's output capture is a
	// dependency of this service and not a convenience.
	if !bytes.Contains(out, []byte("three files")) {
		t.Errorf("the result does not carry what the step printed, so a client"+
			"\n  served from cache sees a step that said nothing: %x", out)
	}

	// And an action nobody ran is a miss, which this API says with NOT_FOUND.
	var reply []byte

	err = conn.Invoke(context.Background(),
		"/build.bazel.remote.execution.v2.ActionCache/GetActionResult",
		&[]byte{}, &reply)

	if status.Code(err) != codes.NotFound {
		t.Errorf("an unknown action answered %v, want NOT_FOUND - which is how"+
			" this API says \"run it\"", err)
	}
}
