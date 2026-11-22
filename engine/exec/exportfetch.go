package exec

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
)

// exportFetcher is a sandbox whose guest stages an artifact somewhere this
// machine cannot read, and which will bring it out.
type exportFetcher interface {
	FetchExport(ctx context.Context, guestPath, into string) error
}

// guestStorer is a sandbox whose guest keeps its store somewhere other than
// where the host keeps its own.
//
// **The other half of E971.** `Sandbox.StoreDir` is one method and, on every
// backend that shares a filesystem, one directory - so nothing forced the two
// meanings apart until a backend arrived with no share at all.
type guestStorer interface {
	GuestStore() string
}

// guestStoreDir is where the guest keeps what the host asks it about.
//
// The host's own store unless the sandbox says otherwise, which is every
// backend but the microVM one. Distinct from the `guestStore` constant, which
// is where a *shared* store appears inside a sandbox: this is the answer for a
// sandbox whose store is not shared at all.
func guestStoreDir(sb Sandbox) string {
	if g, ok := sb.(guestStorer); ok {
		return g.GuestStore()
	}

	return sb.StoreDir()
}

// stagedOnHost makes a staged artifact readable on this machine and says where.
//
// **Read in place where the filesystem is shared, fetched where it is not**,
// which is the same split as placeBlob and for the same reason: an export is
// staged inside the sandbox, and whether this machine can open that path is a
// property of the sandbox rather than of the export.
//
// The returned cleanup removes anything fetched. It is always safe to call, and
// removes nothing when nothing was fetched - a shared export is the guest's own
// staging and deleting it here would be deleting the store's copy.
func stagedOnHost(
	ctx context.Context, sb Sandbox, guestPath, into string,
) (at string, done func(), err error) {
	f, ok := sb.(exportFetcher)
	if !ok {
		return guestPath, func() {}, nil
	}

	// **The destination's parent first, because it usually does not exist yet.**
	// `SAVE ARTIFACT … AS LOCAL build/linux/amd64/earthly` names a directory the
	// build is about to create, and staging beside the destination - which is
	// deliberate, so the copy stays on one filesystem - has to create it rather
	// than assume it. `copyOut` made it later, which was late enough to work
	// only when nothing staged there first.
	err = os.MkdirAll(into, 0o750)
	if err != nil {
		return "", nil, fmt.Errorf("make room for %s: %w", guestPath, err)
	}

	tmp, err := os.MkdirTemp(into, "export-")
	if err != nil {
		return "", nil, fmt.Errorf("make room for %s: %w", guestPath, err)
	}

	done = func() { _ = os.RemoveAll(tmp) }

	err = f.FetchExport(ctx, guestPath, tmp)
	if err != nil {
		done()

		return "", nil, err
	}

	// Under its own name, because the archive carries it: a directory arrives
	// as `out/...` and a file as `out.txt`, so the caller copies one thing to
	// the destination either way. See bulk.PackTree.
	return filepath.Join(tmp, path.Base(guestPath)), done, nil
}

// storeReacher is a sandbox that decides per run whether the host can open its
// store.
//
// **Because for one backend it is a setting, not a fact about the type.** The
// Apple backend's store is a bind-mounted host directory or a volume in the
// guest kernel depending on EARTH_STORE_IN_VM, which defaults to on for a VM -
// so the usual case on macOS is a store this process cannot open, and a
// compile-time type assertion cannot see that.
type storeReacher interface {
	StoreOutOfReach() bool
}

// StoreIsInGuest reports whether a sandbox keeps its store somewhere this
// machine cannot open.
//
// Exported because the front end has to route `earth prune` on the answer: a
// prune of the host's directory is right for a shared store and collects a
// different store entirely where the guest owns it, reporting success either
// way. See guest.KindPrune.
//
// Asked of the sandbox first and inferred from its type only if it does not
// say. A backend that answers both ways has to be asked; one whose store is
// always the guest's need not repeat itself.
func StoreIsInGuest(sb Sandbox) bool {
	if r, ok := sb.(storeReacher); ok {
		return r.StoreOutOfReach()
	}

	_, ok := sb.(guestStorer)

	return ok
}
