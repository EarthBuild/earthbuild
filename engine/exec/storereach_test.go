package exec

import "testing"

// saysReach is a sandbox that decides at run time whether the host can open its
// store, as the Apple backend does.
type saysReach struct {
	Sandbox

	away bool
}

func (s saysReach) StoreOutOfReach() bool { return s.away }

// carriesGuestStore is a sandbox whose store is always the guest's, as the
// microVM backend's is.
type carriesGuestStore struct{ Sandbox }

func (carriesGuestStore) GuestStore() string { return "/store" }

// Whether the host can open a sandbox's store is a question about this run, not
// about the type of sandbox.
//
// **The Apple backend answers it both ways.** Its store is a bind-mounted host
// directory or a volume in the guest kernel depending on EARTH_STORE_IN_VM,
// which defaults to *on* for a VM - so the common case on macOS is a store this
// process cannot open. A compile-time type assertion cannot see that, and said
// "the host can reach it" for every Apple sandbox: `earth prune` then collected
// the host's directory, reported success, and left the store the guest was
// actually using untouched.
//
// That is the same defect the microVM backend had, surviving in the backend
// where it is the default rather than the exception.
func TestReachIsAskedOfTheRunNotTheType(t *testing.T) {
	t.Parallel()

	if !StoreIsInGuest(saysReach{away: true}) {
		t.Error("a sandbox saying its store is out of reach was treated as reachable")
	}

	if StoreIsInGuest(saysReach{away: false}) {
		t.Error("a sandbox sharing its store with the host was treated as out of reach")
	}

	// A backend whose store is always the guest's still answers without
	// needing to say so twice.
	if !StoreIsInGuest(carriesGuestStore{}) {
		t.Error("a sandbox that keeps its store in the guest was treated as reachable")
	}

	// Anything that says nothing shares its store, which is every backend that
	// runs on this machine's own filesystem.
	if StoreIsInGuest(nil) {
		t.Error("a sandbox that says nothing was treated as out of reach")
	}
}
