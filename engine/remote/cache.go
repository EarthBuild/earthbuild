// Package remote serves this engine's store over the protocols a remote
// execution client speaks.
//
// **Read-only, and deliberately the smallest thing that is useful.** Bazel's
// HTTP remote cache is `GET`, `HEAD` and `PUT` under `/ac/<hex>` and
// `/cas/<hex>` over HTTP/1.1 - no gRPC, no generated code, and for `/cas` no
// protobuf at all. It is therefore where the claim this engine has been making
// stops being about encodings and becomes a cache hit: 𝜏 is an REAPI
// input-root digest rather than a translation of one, so a Directory this
// engine named is one another tool asks for under the same number.
//
// The full gRPC surface - Capabilities, FindMissingBlobs, BatchReadBlobs,
// GetTree - is the next thing and needs request *decoding*, where this needs
// none.
package remote

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// Cache serves a store over Bazel's HTTP remote-cache protocol.
type Cache struct {
	// Store is what is served. Only what it already holds: this never writes.
	Store store.DirStore

	// Prefix is the path the protocol lives under, if any. Bazel is happy with
	// `--remote_cache=http://host:port/cache`, and then every path arrives
	// under `/cache`.
	Prefix string
}

// ServeHTTP answers the two paths the protocol defines.
func (c *Cache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// **A store hashed with BLAKE3 cannot answer questions asked in SHA-256.**
	// Bazel asks for a SHA-256 digest, a BLAKE3 store never holds one, and so
	// every request would be a miss - a cache that appears to work and does
	// nothing. Said once, loudly, rather than ten thousand times quietly.
	if ir.Hash() != ir.HashSHA256 {
		http.Error(w, fmt.Sprintf(
			"this store is hashed with %v and the HTTP remote cache protocol"+
				" names blobs by SHA-256\n  every request would miss, and the cache"+
				" would look empty rather than misconfigured\n  build the store with"+
				" %s=sha256 to serve it here",
			ir.Hash(), ir.EnvDigest), http.StatusServiceUnavailable)

		return
	}

	kind, digest, ok := c.route(r.URL.Path)
	if !ok {
		http.NotFound(w, r)

		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
	default:
		// **Read-only on purpose.** This store is filled by builds, not by
		// clients; accepting a PUT would mean taking a blob on a peer's word
		// about what it is called. Bazel treats the refusal as "upload is not
		// available" and carries on reading.
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "this cache is filled by builds and does not accept uploads",
			http.StatusMethodNotAllowed)

		return
	}

	if kind == "ac" {
		// Distinguished from a miss on purpose: Bazel reads 404 as "run the
		// action", which is also what a working, empty cache says - so a front
		// end that has not implemented this at all would be indistinguishable
		// from one that has.
		http.Error(w, "the action cache is not served yet; the CAS is",
			http.StatusNotImplemented)

		return
	}

	b, err := c.Store.Node(digest)
	if err != nil {
		http.NotFound(w, r)

		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", fmt.Sprint(len(b)))
		w.WriteHeader(http.StatusOK)

		return
	}

	_, _ = w.Write(b)
}

// route reads the protocol's two-segment path, or reports that this is not one.
func (c *Cache) route(p string) (kind string, digest ir.NodeID, ok bool) {
	p = strings.TrimPrefix(p, c.Prefix)
	p = strings.TrimPrefix(p, "/")

	kind, rest, found := strings.Cut(p, "/")
	if !found || (kind != "ac" && kind != "cas") {
		return "", ir.NodeID{}, false
	}

	id, err := ir.ParseNodeID(rest)
	if err != nil {
		return "", ir.NodeID{}, false
	}

	return kind, id, true
}
