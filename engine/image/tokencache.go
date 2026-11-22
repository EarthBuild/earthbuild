package image

import (
	"sync"
	"time"
)

// tokenHold is how long a bearer token is reused before it is fetched again.
//
// **Well inside what a registry issues.** Docker Hub and GCR hand out tokens
// good for around five minutes; holding one for sixty seconds means a build
// never presents a credential the registry has forgotten, while collapsing the
// eleven exchanges a cold `+earthly` was making into one or two.
//
// The margin is the point. Reusing a token that has expired would turn a
// working build into a 401 - a failure the previous behaviour, fetching one
// every time, could not have - so this is deliberately far more conservative
// than the lifetime it is protecting.
const tokenHold = 60 * time.Second

// tokens remembers a bearer token for as long as it is certainly good.
//
// **Keyed by the token endpoint, which already carries the scope.** A challenge
// names realm, service and `scope=repository:library/golang:pull`, so two
// repositories ask two different URLs and can never be handed each other's
// credential.
//
// Per process rather than on disk: a token is a credential and the *challenge*
// - where to get one - is the part worth remembering across builds, which is
// what `rememberedChallenge` already does.
var tokens = &tokenCache{held: map[string]heldToken{}}

type heldToken struct {
	token string
	until time.Time
}

// newTokenCacheAt is a cache with the clock supplied, for tests that must move
// time rather than wait for it.
func newTokenCacheAt(now func() time.Time) *tokenCache {
	return &tokenCache{held: map[string]heldToken{}, now: now}
}

type tokenCache struct {
	mu   sync.Mutex
	held map[string]heldToken
	// now is time.Now, named so a test can move it rather than sleep.
	now func() time.Time
}

func (c *tokenCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}

	return time.Now()
}

// get is the token held for this endpoint, if one is and it is still good.
func (c *tokenCache) get(at string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	h, ok := c.held[at]
	if !ok || c.clock().After(h.until) {
		return "", false
	}

	return h.token, true
}

// put remembers a token, and is a no-op for an empty one: a registry that does
// not challenge returns "" and there is nothing to hold.
func (c *tokenCache) put(at, token string) {
	if token == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.held[at] = heldToken{token: token, until: c.clock().Add(tokenHold)}
}

// **There is deliberately no way to drop one early.** A caller the registry
// refuses would want it, but nothing here inspects a 401 yet - and an
// invalidation path with no caller is a claim that the failure is handled.
// The hold is short enough that expiry is not the reason a token is refused.
