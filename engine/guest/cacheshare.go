package guest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// CacheSharing fills a portable cache mount and files what is in one.
//
// **An interface because the implementation belongs outside.** What a unit is,
// how two of them merge and where they are filed are `engine/cacheshare`'s
// business and a wasm runtime's; this package's business is that the guest owns
// the store and is therefore the only party that can do any of it. Keeping the
// two apart is also what lets a test ask whether the request arrived without
// building a wasm module to answer it.
//
// Nil where this guest was not given one, which is every guest before a build
// that shares a cache - and then a request says so rather than succeeding
// quietly.
type CacheSharing interface {
	// Stock fills the cache directory from what this machine can reach.
	Stock(ctx context.Context, m ir.Mount, dir string) error
	// Offer files the cache's units, withheld saying why it must not.
	Offer(ctx context.Context, m ir.Mount, dir, withheld string) error
	// Known is what this machine has filed, keyed `<id>/<scope>`.
	Known() map[string]string
}

// errNoCacheSharing is what a guest says when it was not given a sharer.
var errNoCacheSharing = errors.New(
	"this guest cannot share caches: it was started without a helper runtime")

// keepHelper files the module a request staged for this guest.
func (s *Server) keepHelper(req Request) error {
	keeper, ok := s.Caches.(interface {
		Accept(hex string, body []byte) error
	})
	if !ok {
		return nil
	}

	body, err := os.ReadFile(req.Blob) //nolint:gosec // a path this engine staged
	if err != nil {
		return fmt.Errorf("read the helper staged at %s: %w", req.Blob, err)
	}

	return keeper.Accept(req.Mounts[0].HelperID, body)
}

// stockCache fills one cache mount and says how it went.
func (s *Server) stockCache(ctx context.Context, req Request) Response {
	m, dir, err := s.cacheMount(req)
	if err != nil {
		return Response{Err: err.Error()}
	}

	if err := s.Caches.Stock(ctx, m, dir); err != nil {
		return Response{Err: err.Error()}
	}

	return Response{}
}

// shareCache files one cache mount's units and answers with its map.
//
// The map digest goes back because the pointer naming it lives beside the store,
// and the store is this guest's: a host that cannot learn it has a cache no peer
// will ever be told about.
func (s *Server) shareCache(ctx context.Context, req Request) Response {
	m, dir, err := s.cacheMount(req)
	if err != nil {
		return Response{Err: err.Error()}
	}

	if err := s.Caches.Offer(ctx, m, dir, req.Withheld); err != nil {
		return Response{Err: err.Error()}
	}

	// Keyed exactly as the pointer is, so a trust domain that separates two
	// caches separates their maps too.
	return Response{CacheMap: s.Caches.Known()[m.ID+"/"+req.Mounts[0].Scope]}
}

// cacheMount is the declaration a cache request carries, and where its contents
// live *in this guest*.
//
// Resolved here rather than sent, for `mountStore`'s reason: the host and the
// guest see the store at different paths, and a host path built into a request
// made the guest create that path in its own filesystem - so the first build's
// cache went somewhere that vanished with the VM.
func (s *Server) cacheMount(req Request) (ir.Mount, string, error) {
	if s.Caches == nil {
		return ir.Mount{}, "", errNoCacheSharing
	}

	// **The helper's module, staged where this side can read it.** It is filed
	// in 𝔅 on the machine that resolved the reference, and on a VM backend that
	// is the host, whose store is a different device. Kept on arrival, so the
	// next step finds it by digest and the host stages it once.
	if req.Blob != "" && req.Mounts != nil {
		if err := s.keepHelper(req); err != nil {
			return ir.Mount{}, "", err
		}
	}

	if len(req.Mounts) != 1 {
		return ir.Mount{}, "", fmt.Errorf(
			"a cache request names %d mounts and must name exactly one", len(req.Mounts))
	}

	in := req.Mounts[0]
	if in.ID == "" {
		return ir.Mount{}, "", errors.New("a cache request names a mount with no id")
	}

	return ir.Mount{
		ID: in.ID, Target: in.Target, Helper: in.Helper, HelperID: in.HelperID,
		// Portable is implied: the host only asks about a mount whose author
		// offered it, and this end interprets no claim of its own.
		Portable: true,
	}, filepath.Join(MountStore(s.LayerDir), in.ID, in.Scope), nil
}
