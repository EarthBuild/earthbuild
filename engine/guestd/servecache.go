package guestd

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/remote"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// serving is where the last listener bound, for a test that has to reach it.
//
// A package variable rather than a return value, because every caller but a
// test passes an address it already knows and a second return value would be
// discarded at the one real call site.
var serving atomic.Value

// EnvCacheAddr is where this agent serves the remote cache protocol.
//
// Empty means it does not, which is every build today. An address rather than a
// flag because a guest is configured by its environment - its command line
// comes from a kernel, and a setting absent from the host's list is silently
// ignored inside the VM.
const EnvCacheAddr = "EARTH_GUEST_CACHE_ADDR"

// serveCache answers the remote cache protocol from this agent's store.
//
// **Here rather than on the host, because the store is here.** A store on the
// guest's device is not on the host's filesystem, so a host-side service would
// be reading a directory that answers nothing - and the guest is already the
// long-lived thing, already what a sandbox can reach.
//
// Read-only: this store is filled by builds, and accepting an upload would mean
// taking a blob on a client's word about what it is called.
func serveCache(root, at string, hold func() func()) (stop func(), err error) {
	if ir.Hash() != ir.HashSHA256 {
		return nil, fmt.Errorf(
			"%s is set and this store is hashed with %v, where the protocol names"+
				" blobs by SHA-256"+
				"\n  every request would miss, and the cache would look empty rather"+
				" than wrongly built"+
				"\n  build the store with %s=sha256",
			EnvCacheAddr, ir.Hash(), ir.EnvDigest)
	}

	// Best effort: an agent that cannot open the action cache still serves
	// content, which is the half a client asks for first.
	ac, _ := cache.Open(root)

	ln, err := net.Listen("tcp", at)
	if err != nil {
		return nil, fmt.Errorf("%s: listen on %s: %w", label(), at, err)
	}

	c := &remote.Cache{
		Store:   store.DirStore(root),
		Actions: ac,
		// **A machine with a request in flight is not idle.** Idleness is
		// measured by when a host last spoke, and a client inside a step is
		// not the host - so without this the agent stops itself while it is
		// busiest, and the client sees a connection close saying nothing.
		Hold: hold,
	}

	// **Both protocols on one address, because a client is told one address.**
	// buck2 and bazel speak REAPI over gRPC; this engine's own fleet speaks the
	// HTTP cache. Two ports would be a second thing to plumb through the
	// sandbox and a second variable to set, and a client pointed at the wrong
	// one gets a connection that succeeds and then says nothing useful.
	//
	// They are distinguishable without guessing: gRPC is HTTP/2 carrying
	// `application/grpc`, which nothing else here sends.
	g := grpc.NewServer(grpc.ForceServerCodec(remote.Codec()))
	(&remote.Service{Cache: c}).Register(g)

	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 &&
			strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			g.ServeHTTP(w, r)

			return
		}

		c.ServeHTTP(w, r)
	})

	// **Unencrypted HTTP/2 as well as HTTP/1.1.** There is no TLS here and
	// nothing for it to protect: the listener is reachable only from inside a
	// sandbox this engine started, which is the whole of the authorisation
	// model (plan-remote-execution R5). Without HTTP/2 a gRPC client's
	// prior-knowledge preface is read as a malformed HTTP/1.1 request, and the
	// error names a frame size rather than anything a reader could act on.
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	srv := &http.Server{
		Handler:           mux,
		Protocols:         protocols,
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Fprintf(os.Stderr, "%s: remote cache and execution on %s\n", label(), ln.Addr())

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "%s: remote cache stopped: %v\n", label(), err)
		}
	}()

	serving.Store(ln.Addr().String())

	return func() { g.Stop(); _ = srv.Close() }, nil
}
