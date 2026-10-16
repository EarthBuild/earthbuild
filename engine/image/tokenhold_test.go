package image

import (
	"testing"
	"time"
)

// TestAHeldTokenIsLetGoBeforeItCouldExpire.
//
// The hold is what makes reusing a credential safe, so it is asserted rather
// than assumed. A token kept past its life would turn a working build into a
// 401 - a failure the old behaviour, fetching one every time, could not have.
func TestAHeldTokenIsLetGoBeforeItCouldExpire(t *testing.T) {
	t.Parallel()

	at := "https://example.test/token?scope=repository:library/x:pull"
	now := time.Unix(1_700_000_000, 0)

	c := newTokenCacheAt(func() time.Time { return now })

	c.put(at, "issued")

	if got, ok := c.get(at); !ok || got != "issued" {
		t.Fatalf("a token just held came back as (%q, %v)", got, ok)
	}

	// A whisker before the hold ends it is still good...
	now = now.Add(tokenHold - time.Second)

	if _, ok := c.get(at); !ok {
		t.Error("the token was dropped before its hold was up")
	}

	// ...and after it, gone, without waiting for a registry to say so.
	now = now.Add(2 * time.Second)

	if _, ok := c.get(at); ok {
		t.Error("the token outlived its hold, so a build could present a" +
			"\n  credential the registry has forgotten")
	}

	// And a scope this process has not asked about is never guessed at.
	if _, ok := c.get(at + "-other"); ok {
		t.Error("a token was handed to a scope that never fetched one")
	}
}
