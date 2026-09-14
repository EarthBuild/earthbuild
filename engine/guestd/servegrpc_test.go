package guestd

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/remote"
)

// One address answers both protocols.
//
// **Because a client is told one address and speaks one of them.** buck2 and
// bazel speak REAPI over gRPC; this engine's own fleet speaks the HTTP cache.
// Serving them on separate ports would mean a second thing to plumb through
// the sandbox, a second environment variable, and a client that pointed at the
// wrong one getting a connection that succeeds and then says nothing useful.
//
// gRPC is HTTP/2 with a content type, so one handler can tell them apart -
// which is the only reason this is one listener rather than two.
func TestOneAddressAnswersGRPCAndHTTP(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	stop, err := serveCache(t.TempDir(), "127.0.0.1:0", func() func() { return func() {} })
	if err != nil {
		t.Fatal(err)
	}

	defer stop()

	at := listenedOn(t)

	// The HTTP half still answers, which is the half that already worked.
	resp, err := http.Get("http://" + at + "/cas/" + ir.NodeID{1}.String())
	if err != nil {
		t.Fatal(err)
	}

	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("the HTTP cache answered %s, want 404", resp.Status)
	}

	// And the gRPC half answers on the same port.
	conn, err := grpc.NewClient(at,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(remote.Codec())))
	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	in, out := []byte{}, []byte{}

	err = conn.Invoke(context.Background(),
		"/build.bazel.remote.execution.v2.Capabilities/GetCapabilities", &in, &out)
	if err != nil {
		t.Fatalf("GetCapabilities over gRPC: %v", err)
	}

	// **The digest function it names has to be the one the store was built
	// with.** A service advertising the other one hands a client a name space
	// this store holds nothing in, and every request misses while the cache
	// merely looks empty.
	//
	// The batch size is REAPI's conventional 4 MiB and is written out here
	// rather than imported: a client splits its requests by what this says, so
	// changing it is a change to what every peer does, and a test that followed
	// the constant would not notice.
	want := layer.EncodeCapabilities(layer.DigestFunctionSHA256, 4<<20)
	if !bytes.Equal(out, want) {
		t.Errorf("capabilities are %x\n  want            %x", out, want)
	}
}
