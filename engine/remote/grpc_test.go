package remote_test

import (
	"context"
	"net"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/remote"
	"github.com/EarthBuild/earthbuild/engine/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
