//go:build linux

package exec

import (
	"errors"
	"strings"
	"testing"
)

// A store the guest cannot mount is recognised as a store, not as a mystery.
//
// **This is the price of a fast cache, and it has to be paid at startup.** The
// drives do not pass the guest's flushes through, which is right for a cache
// and safe so long as the guest unmounts before the VMM stops. When it does not
// - a kill, an OOM, a host that lost power - the image keeps metadata that is
// half old and half new, and the next guest says so and halts:
//
//	earth-vmboot: mount the layer store from /dev/vda: structure needs cleaning
//
// Every build after that failed the same way, because nothing recognised the
// state. Sixteen targets in one sweep died on it and were read as backend
// defects. A cache that cannot be rebuilt without a person noticing is not a
// cache.
func TestAStoreThatWillNotMountIsRecognised(t *testing.T) {
	t.Parallel()

	for _, said := range []string{
		"the guest said: earth-vmboot: mount the layer store from /dev/vda: structure needs cleaning",
		"XFS (vda): Corruption of in-memory data detected. Shutting down filesystem",
		"mount the layer store from /dev/vda: input/output error",
	} {
		if !storeUnmountable(errors.New(said)) {
			t.Errorf("not recognised as a store that will not mount: %s", said)
		}
	}
}

// Anything else is left alone.
//
// The recovery remakes the device and discards every layer on it, so a false
// positive costs a full rebuild. A failure that is not the filesystem - the
// guest never booted, the agent is the wrong architecture - must not trigger it.
func TestOnlyAMountFailureIsRecognised(t *testing.T) {
	t.Parallel()

	for _, said := range []string{
		"the guest did not answer the handshake within 30s",
		"/tmp/earth-guestd is built for amd64, but the sandbox runs arm64",
		"start firecracker: permission denied",
		"write /store/layers/.abc.partial/usr/bin/git: no space left on device",
	} {
		if storeUnmountable(errors.New(said)) {
			t.Errorf("wrongly treated as a broken store, which would discard the cache: %s", said)
		}
	}
}

// The remedy names the device and the command that remakes it.
func TestTheRemedyIsActionable(t *testing.T) {
	t.Parallel()

	hint := brokenStoreHint("/srv/store.img")
	for _, want := range []string{"/srv/store.img", "mkfs.xfs", "reflink=1"} {
		if !strings.Contains(hint, want) {
			t.Errorf("the hint does not mention %q:\n%s", want, hint)
		}
	}
}
