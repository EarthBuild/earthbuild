package guest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The guest fills and offers a cache mount, because the guest owns the store.
//
// **The same argument `KindPrune` makes.** On a microVM the store is a device
// nothing outside has mounted, so a host that wants a cache shared cannot read
// the mount, cannot write a unit, and cannot run the helper that knows what a
// unit is. Every one of those is a thing only the guest can do, so the host asks
// rather than does.
//
// Until now the host tried, found `<store>/mounts/<id>/<scope>` missing on its
// own filesystem, read that as a mount no step had used, and shared nothing -
// which is why a Mac says so out loud rather than appearing to share.
func TestTheGuestStocksACacheItWasAskedTo(t *testing.T) {
	t.Parallel()

	var got struct {
		mount ir.Mount
		dir   string
	}

	s := &Server{LayerDir: "/store", Caches: shareFunc{
		stock: func(_ context.Context, m ir.Mount, dir string) error {
			got.mount, got.dir = m, dir

			return nil
		},
	}}

	resp := s.handle(context.Background(), Request{
		Kind: KindStockCache, CacheMap: "feedface",
		Mounts: []Mount{{
			ID: "go-build", Scope: "abc", Helper: "./h.wasm", HelperID: "deadbeef",
		}},
	}, nil)

	if resp.Err != "" {
		t.Fatalf("stocking a cache: %s", resp.Err)
	}

	if want := filepath.Join(MountStore("/store"), "go-build", "abc"); got.dir != want {
		t.Errorf("stocked %q, want %q\n  the guest resolves the mount against its"+
			" own store, which is the whole reason it is asked", got.dir, want)
	}

	if got.mount.ID != "go-build" || got.mount.Helper != "./h.wasm" ||
		got.mount.HelperID != "deadbeef" {
		t.Errorf("the declaration did not survive the request: %#v", got.mount)
	}
}

// And offers one, reporting the map so the host can hint it to a peer.
//
// The pointer from a cache to its latest map lives beside the store, which is
// now the guest's - so the host learns the digest by being told rather than by
// reading a file it cannot reach.
func TestTheGuestOffersACacheAndSaysWhichMap(t *testing.T) {
	t.Parallel()

	s := &Server{LayerDir: "/store", Caches: shareFunc{
		offer: func(context.Context, ir.Mount, string, string) error { return nil },
		known: map[string]string{"npm/abc": "0f0f"},
	}}

	resp := s.handle(context.Background(), Request{
		Kind: KindShareCache, Mounts: []Mount{{ID: "npm", Scope: "abc"}},
	}, nil)

	if resp.Err != "" {
		t.Fatalf("offering a cache: %s", resp.Err)
	}

	if resp.CacheMap != "0f0f" {
		t.Errorf("the guest answered with map %q, want the one it just filed"+
			"\n  the host cannot read the pointer, so an unanswered share is a"+
			" cache no peer will ever be told about", resp.CacheMap)
	}
}

// A guest with no sharing configured says so rather than pretending.
//
// The third silent degrade this design has grown would be a guest that accepts
// the request and does nothing; a host told "not configured" can report it.
func TestAGuestThatCannotShareSaysSo(t *testing.T) {
	t.Parallel()

	resp := (&Server{LayerDir: "/store"}).handle(context.Background(), Request{
		Kind: KindStockCache, Mounts: []Mount{{ID: "k", Scope: "s"}},
	}, nil)

	if resp.Err == "" {
		t.Fatal("a guest with no cache sharing accepted the request silently")
	}
}

// A request with no mount is refused rather than resolved against nothing.
func TestACacheRequestWithoutAMountIsRefused(t *testing.T) {
	t.Parallel()

	resp := (&Server{LayerDir: "/store", Caches: shareFunc{}}).handle(
		context.Background(), Request{Kind: KindShareCache}, nil)

	if resp.Err == "" {
		t.Fatal("a cache request naming no mount was accepted")
	}
}

// shareFunc is a CacheSharing made of the functions a test cares about.
type shareFunc struct {
	stock func(context.Context, ir.Mount, string) error
	offer func(context.Context, ir.Mount, string, string) error
	known map[string]string
}

func (f shareFunc) Stock(ctx context.Context, m ir.Mount, dir string) error {
	if f.stock == nil {
		return nil
	}

	return f.stock(ctx, m, dir)
}

func (f shareFunc) Offer(ctx context.Context, m ir.Mount, dir, withheld string) error {
	if f.offer == nil {
		return nil
	}

	return f.offer(ctx, m, dir, withheld)
}

func (f shareFunc) Known() map[string]string { return f.known }
