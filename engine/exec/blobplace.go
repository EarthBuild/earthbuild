package exec

import (
	"context"
	"fmt"
	"path/filepath"
)

// blobSeer is a sandbox whose guest can read the host's own files, and says
// where it sees them. A shared mount: nothing is copied.
type blobSeer interface {
	GuestPath(string) (string, bool)
}

// blobPlacer is a sandbox with no filesystem in common with its host, which
// takes the bytes and says where it put them.
type blobPlacer interface {
	PlaceBlob(ctx context.Context, host string) (string, error)
}

// placeBlob makes a blob on this machine readable by the guest and says where
// the guest will find it.
//
// **Two ways because there are two kinds of sandbox**, and the difference is
// not a detail of the transport: a guest reached over virtio-fs opens the
// host's own file, and a Firecracker guest has no virtio-fs at all, so the
// bytes have to travel. Which one this is decides whether a 45 MB layer is
// copied or not, so it is asked rather than assumed.
//
// **Placing wins where a sandbox offers both.** Sharing is cheaper and it is
// also the one that fails invisibly: told to share a path its guest cannot
// open, the failure surfaces in the guest as a layer the store does not hold,
// which names neither the blob nor the sandbox.
func placeBlob(ctx context.Context, sb Sandbox, host string) (string, error) {
	if p, ok := sb.(blobPlacer); ok {
		return p.PlaceBlob(ctx, host)
	}

	if s, ok := sb.(blobSeer); ok {
		at, visible := s.GuestPath(host)
		if !visible {
			return "", fmt.Errorf("the guest cannot see %s, so it cannot unpack it"+
				"\n  a blob has to be under a directory shared into the sandbox,"+
				" and this one is not", filepath.Base(host))
		}

		return at, nil
	}

	return "", fmt.Errorf("this sandbox can neither share %s with its guest nor"+
		" place it there, so the guest cannot be handed a blob", filepath.Base(host))
}

// sharesBlobs reports whether this sandbox's guest reads the host's own files.
//
// The question streaming turns on: a layer can be unpacked as it is fetched
// only where both sides are looking at one file. See placeBlob.
func sharesBlobs(sb Sandbox) bool {
	if _, ok := sb.(blobPlacer); ok {
		return false
	}

	_, ok := sb.(blobSeer)

	return ok
}
