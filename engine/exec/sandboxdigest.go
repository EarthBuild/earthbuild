package exec

import (
	"encoding/hex"
	"fmt"

	"lukechampine.com/blake3"
)

// sandboxDigest names a sandbox after what it *is*, so the next build can find
// the machine the last one left running.
//
// **The digest is what makes reuse safe rather than merely fast.** A machine is
// found by name, so two configurations that hash alike are a build attaching to
// a machine set up for something else. Every setting that changes what the
// machine is has to be in here - not what it is asked to do, which is the
// request, but how it was built: its kernel, its store, its size, its network.
// The Apple backend learned this twice, and both times the symptom appeared far
// from the cause: a VM started before a flag existed answered the listing, got
// reused, and failed later complaining about something else entirely (E549,
// E555).
//
// **Length-prefixed, because concatenation is not injective.** ("ab", "c") and
// ("a", "bc") are two configurations and must be two names.
//
// Shared by the backends that reuse a machine. What differs between them is how
// a running one is *found* - Apple asks `container ls`, a microVM reads the
// register beside its store - and that is behaviour rather than a type, so it
// is an interface elsewhere and not a parameter here.
func sandboxDigest(parts ...string) string {
	h := blake3.New(32, nil)

	for _, part := range parts {
		fmt.Fprintf(h, "%d:%s", len(part), part)
	}

	return hex.EncodeToString(h.Sum(nil))[:16]
}
