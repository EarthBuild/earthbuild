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

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// Cache serves a store over Bazel's HTTP remote-cache protocol.
type Cache struct {
	// Store is what is served.
	Store store.DirStore

	// Actions answers what a step produced, by the key it produced it under.
	//
	// An interface rather than the cache itself, so this package does not
	// depend on how entries are stored - and so a test can serve one entry
	// without a store behind it.
	Actions Actions

	// Hold keeps the machine this serves on from stopping while a request is
	// in flight, and is released when it finishes.
	//
	// **A machine with work in flight is not idle.** A sandbox stops itself
	// when nobody has wanted it for a while, and idleness is measured by when a
	// host last spoke - which a client inside a step is not. A service without
	// this would have its own machine stopped underneath it, mid-request, and
	// the client would see a connection close with nothing to say why.
	//
	// Nil where there is nothing to hold open, which is every caller outside a
	// sandbox.
	Hold func() (release func())

	// Elsewhere is asked for a blob this machine does not hold, and nil where
	// there is nobody to ask.
	//
	// **A read-through and not a mirror.** A blob this store has is answered
	// without consulting anybody: a worker holds most of what its steps ask
	// for, and a hit that paid a round trip first would make the common case
	// expensive to make the rare one cheap.
	Elsewhere Elsewhere

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

	if c.Hold != nil {
		defer c.Hold()()
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
		c.serveAction(w, r, digest)

		return
	}

	b, err := c.Store.Node(digest)
	if err != nil {
		// **Not here, but perhaps somebody knows.** A worker's store is cold
		// for everything the driver built, and the bytes it wants are already
		// content-addressed and already reachable - what was missing was this
		// machine being willing to say so on somebody else's behalf.
		var found bool

		if b, found = fromElsewhere(c.Elsewhere, digest); !found {
			http.NotFound(w, r)

			return
		}
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

// Actions is what a cache of results answers.
type Actions interface {
	// Get is the result recorded under a key, if there is one.
	Get(k core.Key) (core.Entry, bool)
}

// serveAction answers with what the step under this key produced.
//
// **The key is the Action digest** (green paper 4.5a), so the number a client
// asks under is the number this engine derived - no index, no translation, no
// second place for the two to disagree.
func (c *Cache) serveAction(w http.ResponseWriter, r *http.Request, key ir.NodeID) {
	if c.Actions == nil {
		http.Error(w, "this cache serves content and not results", http.StatusNotImplemented)

		return
	}

	e, ok := c.Actions.Get(core.Key(key))
	if !ok {
		http.NotFound(w, r)

		return
	}

	// **A result whose tree this store cannot name is a miss, not an error.**
	// An entry written before the content digest existed has none, and one
	// whose layer has been collected cannot be described - in both cases the
	// honest answer is that there is nothing here to hand over.
	size, err := c.rootSize(e)
	if err != nil {
		http.NotFound(w, r)

		return
	}

	treeID, treeSize := c.treeOf(e)
	b := resultOf(e, size, treeID, treeSize, c.declaredBy(key, e))

	w.Header().Set("Content-Type", "application/octet-stream")

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", fmt.Sprint(len(b)))
		w.WriteHeader(http.StatusOK)

		return
	}

	_, _ = w.Write(b)
}

// rootSize is the serialised length of the Directory a result materialises to,
// writing that Directory into the store if it is not already there.
//
// **Filled on being asked rather than on every capture.** Noting a tree's nodes
// at capture time was measured at ninety times the cost of noting the manifest
// - 54.3ms against 0.6ms on a 4,000-entry layer - and paid by every build
// whether or not anything ever asked. Deriving them here costs one fold, once,
// for a result somebody actually wants.
func (c *Cache) rootSize(e core.Entry) (int64, error) {
	return c.Store.TreeNodes(e.Layer, e.Content)
}

// treeOf is the inline Tree for a cached result, or nothing.
//
// **Best effort, because a result is still a result without one.** A peer that
// reads `root_directory_digest` needs nothing here; one that reads only
// `tree_digest` needs it and would refuse the hit. Failing the whole lookup
// because the inline form could not be built would deny both.
func (c *Cache) treeOf(e core.Entry) (ir.NodeID, int64) {
	id, size, err := c.Store.TreeMessage(e.Layer, e.Content)
	if err != nil {
		return ir.NodeID{}, 0
	}

	return id, size
}

// resultOf is a cache entry as an ActionResult.
//
// **One conversion, two transports.** HTTP and gRPC answer the same question,
// and a second place that turned an entry into a result would be a second
// answer to what a step produced.
func resultOf(
	e core.Entry, rootSize int64, tree ir.NodeID, treeSize int64, declared layer.Declared,
) []byte {
	return layer.EncodeActionResult(layer.Result{
		Root:     e.Content,
		RootSize: rootSize,
		Tree:     tree,
		TreeSize: treeSize,
		Declared: declared,
		ExitCode: int32(e.Exit), //nolint:gosec // a process exit status
		// What the step printed, which a client displays. Empty where it
		// printed nothing or printed more than was kept - a caller needing to
		// tell those apart needs the entry, not the message.
		Stdout: []byte(e.Stdout),
	})
}

// ActionResult is what the step under this key produced, as a message.
//
// False where nothing is recorded, or where the result names a tree this store
// cannot describe - an entry written before content digests existed, or one
// whose layer has been collected. In both cases there is nothing to hand over,
// which is a miss and not an error.
func (c *Cache) ActionResult(key ir.NodeID) ([]byte, bool) {
	if c.Actions == nil {
		return nil, false
	}

	e, ok := c.Actions.Get(core.Key(key))
	if !ok {
		return nil, false
	}

	size, err := c.rootSize(e)
	if err != nil {
		return nil, false
	}

	treeID, treeSize := c.treeOf(e)

	return resultOf(e, size, treeID, treeSize, c.declaredBy(key, e)), true
}

// declaredBy is the outputs the action under this key said it produces.
//
// **Derived on the way out, because a hit has to answer what a run answers.**
// A client asks about paths and is answered about paths whether or not the work
// happened just now; a result that named the whole delta instead would be
// refused by the client that had just asked for it ("Path is empty"), which is
// a cache that cannot be used rather than a cache that is empty.
//
// Everything needed is in this store already: the key *is* the Action digest
// (green paper 4.5a), the Action names its Command, and the Command lists the
// paths. Nothing is kept in the entry that could go stale against them.
//
// Empty where any of that is missing - an action whose blobs have been
// collected, or a step that declared nothing - and then the whole delta is
// named as before, which is what a step's result is.
func (c *Cache) declaredBy(key ir.NodeID, e core.Entry) layer.Declared {
	paths, ok := c.outputPaths(key)
	if !ok || len(paths) == 0 {
		return layer.Declared{}
	}

	m, held, err := store.ReadManifest(string(c.Store), e.Layer)
	if err != nil || !held {
		return layer.Declared{}
	}

	declared, err := layer.Outputs(m, paths)
	if err != nil {
		return layer.Declared{}
	}

	// Fetchable, at the price of a link - see DirStore.LinkBlob. A client
	// materialises what it is told about, and a hit it cannot read is worse
	// than a miss.
	for _, f := range declared.Files {
		_ = c.Store.LinkBlob(e.Layer, f.Path, f.Digest)
	}

	for _, d := range declared.Dirs {
		for id, b := range d.Nodes {
			_ = c.Store.Accept(id, b)
		}
	}

	return declared
}

// outputPaths reads an Action's Command to find what it declares.
func (c *Cache) outputPaths(key ir.NodeID) ([]string, bool) {
	ab, err := c.Store.Node(key)
	if err != nil {
		return nil, false
	}

	a, err := layer.ActionIn(ab)
	if err != nil {
		return nil, false
	}

	cb, err := c.Store.Node(a.Command)
	if err != nil {
		return nil, false
	}

	cmd, err := layer.CommandIn(cb)
	if err != nil {
		return nil, false
	}

	return cmd.OutputPaths, true
}
