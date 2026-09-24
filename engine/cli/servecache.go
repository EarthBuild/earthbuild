package cli

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/remote"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// ServeCache serves this machine's store over the remote cache protocol.
//
// **Read-only, and filled by builds rather than by clients.** A client fetches
// what a build here already produced; nothing it sends is stored, because
// accepting an upload would mean taking a blob on a peer's word about what it is
// called.
//
// **A host-side reader, on purpose and not for ever.** This opens the layer
// store from the host, which works while the store is a directory the host can
// see and answers nothing once it is a device the guest owns. The service
// belongs in the guest - beside the store, and beside the thing that is already
// long-lived across builds (plan-remote-execution R5). This is how to try it by
// hand in the meantime, not where it ends up.
//
// The address is whatever `net.Listen` accepts - `:8080` for every interface,
// `127.0.0.1:8080` for this machine alone, which is the one to prefer since
// this speaks no authentication at all.
func ServeCache(o Options, addr string) error {
	// **The store, not the project.** Options.Dir is where the Earthfile is;
	// what is served is the layer store, which storeDir resolves the way every
	// other command that touches it does - an explicit EARTH_CACHE_DIR, then
	// XDG, then the conventional place. Reading o.Dir here served the working
	// directory, which is not a store and holds nothing a client could want.
	dir, err := storeDir()
	if err != nil {
		return err
	}

	// **Said before a client discovers it by getting nothing.** The protocol
	// names blobs by SHA-256; a BLAKE3 store holds none of those, so every
	// request would miss and the cache would look empty rather than
	// misconfigured.
	if ir.Hash() != ir.HashSHA256 {
		return fmt.Errorf(
			"this store is hashed with %v and the remote cache protocol names"+
				" blobs by SHA-256"+
				"\n  every request would miss, and the cache would look empty rather"+
				" than wrongly built"+
				"\n  build the store with %s=sha256 and run the build again first",
			ir.Hash(), ir.EnvDigest)
	}

	c, openErr := cache.Open(dir)
	if openErr != nil {
		return fmt.Errorf("open the cache at %s: %w", dir, openErr)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	fmt.Fprintf(o.Out, "serving %s over the remote cache protocol on http://%s\n", dir, ln.Addr())
	fmt.Fprintf(o.Out, "  point a client at it with --remote_cache=http://%s\n", ln.Addr())

	srv := &http.Server{
		Handler: &remote.Cache{Store: store.DirStore(dir), Actions: c},
		// A cache request is a fetch, not a conversation: a client that has
		// stopped talking is not one to hold a connection open for.
		ReadHeaderTimeout: 10 * time.Second,
	}

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}

	return nil
}
