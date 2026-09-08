//go:build linux

package exec

import (
	"fmt"
	"strings"
)

// mountFailures are the ways a guest says its store device will not mount.
//
// Matched on the guest's own words rather than an error type, because the
// failure crosses the console and the protocol as text - the same reason
// outOfSpace has to read a message. Anchored on the mount so that a step
// printing "input/output error" is not mistaken for the store.
var mountFailures = []string{
	"mount the layer store",
	"structure needs cleaning",
	"Corruption of in-memory data detected",
}

// storeUnmountable reports whether a failure is the guest refusing its store.
//
// **The price of a fast cache, recognised at startup.** The drives do not pass
// the guest's flushes to the host, which is right for a cache and safe so long
// as the guest unmounts before the VMM stops. When it does not - a kill, an
// OOM, a host that lost power - the image keeps metadata half old and half new,
// and every guest afterwards halts on it. Sixteen targets in one sweep died
// this way and were read as backend defects.
//
// Narrow deliberately. The recovery discards every layer on the device, so a
// false positive costs a full rebuild: a guest that never booted, or an agent
// built for the wrong architecture, must not reach it.
func storeUnmountable(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()

	for _, s := range mountFailures {
		if strings.Contains(msg, s) {
			return true
		}
	}

	return false
}

// brokenStoreHint says how to put a store device back.
//
// The device is a cache, so remaking it costs a cold build and nothing else -
// which is the whole reason the fast cache mode is the right default. It cannot
// be repaired in place: `xfs_repair` is not in the initramfs, and the device is
// not mountable on the host.
func brokenStoreHint(image string) string {
	if image == "" {
		return ""
	}

	return fmt.Sprintf("\n  the store device did not come back from an unclean stop, and holds"+
		"\n  metadata a guest will not mount. It is a cache: remaking it costs one"+
		"\n  cold build and nothing else"+
		"\n    truncate -s 150G %s && mkfs.xfs -m reflink=1,crc=1 -i nrext64=0 -n ftype=1 -f %s"+
		"\n  set %s=1 to trade write speed for a store that survives a kill",
		image, image, EnvDurableStore)
}
