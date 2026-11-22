package exec

import "testing"

// Two different sets of settings are two different sandboxes.
//
// **Length-prefixed, because concatenation is not injective.** A sandbox is
// found and reused by name, so a name that collides is a build attaching to a
// machine configured for something else - which is the failure the Apple
// backend hit twice: a VM started before a flag existed answered the listing,
// got reused, and failed much later in a way that named neither the flag nor
// the reuse (E549, E555).
func TestSandboxDigestSeparatesSettingsThatDiffer(t *testing.T) {
	t.Parallel()

	if sandboxDigest("ab", "c") == sandboxDigest("a", "bc") {
		t.Error("two settings that differ hash the same, so a build would" +
			" attach to a machine configured for the other one")
	}

	if sandboxDigest("a", "b") != sandboxDigest("a", "b") {
		t.Error("the same settings hash differently, so no VM is ever reused")
	}

	// An added setting must change the name, or raising it changes nothing
	// until every running machine has been removed by hand.
	if sandboxDigest("a", "b") == sandboxDigest("a", "b", "") {
		t.Error("an added empty setting does not change the name")
	}
}

// A name is short enough to be a filename and a container name.
func TestASandboxDigestIsShortAndStable(t *testing.T) {
	t.Parallel()

	got := sandboxDigest("kernel", "initrd", "store")
	if len(got) != 16 {
		t.Errorf("digest %q is %d chars; it goes in names that have limits", got, len(got))
	}

	for _, c := range got {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("digest %q is not hex, so it is not safe in a path or a name", got)

			break
		}
	}
}
