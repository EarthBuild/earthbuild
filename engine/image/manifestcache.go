package image

import (
	"strings"
	"sync"
)

// A manifest fetched for a digest is remembered so the pull that follows a warm
// does not fetch it again. `registry:manifest` is 0.135s of a cold build, and
// like the token exchange beside it, it is host-side work that has no reason to
// wait behind a sandbox boot (E907).
//
// **Only a digest, never a tag.** The bytes behind a digest cannot change, so a
// remembered body is the same answer rather than a stale one - and answering
// "what does this tag mean today" from a cache is the thing `Resolve` refuses
// to do for exactly this reason. A tag is never put here and never looked up.
//
// The saving is a request as much as a wait: Docker Hub allows an anonymous
// puller a hundred manifest requests an hour, which a loop of builds exhausts.

// manifestLimit bounds what one process will remember.
//
// A build names a handful of images, so this is not a capacity so much as a
// refusal to grow without bound in a process that turns out to be long-lived.
const manifestLimit = 64

// manifestCache remembers manifest bodies by the URL they came from.
//
// Keyed by URL rather than by digest alone, because a mirror and an origin are
// different hosts that may answer differently and the pull asks a specific one.
type manifestCache struct {
	mu   sync.Mutex
	held map[string][]byte
}

var manifests = &manifestCache{held: map[string][]byte{}}

// pinned reports whether a target names a digest, which is the only kind of
// target this cache will touch.
func pinned(target string) bool { return strings.HasPrefix(target, "sha256:") }

// get returns a remembered manifest, or nil.
//
// **Verified against the digest on the way out.** The check costs a hash of a
// few kilobytes and makes a wrong entry unusable rather than dangerous: a bug
// in this cache can then only cost a fetch, never substitute one image for
// another. Nothing else in the pull re-checks the manifest against the digest
// that was asked for.
func (c *manifestCache) get(url, target string) []byte {
	if !pinned(target) {
		return nil
	}

	c.mu.Lock()
	body, ok := c.held[url]
	c.mu.Unlock()

	if !ok || verify(body, target) != nil {
		return nil
	}

	return body
}

// put remembers a manifest, if it is what it claims to be.
func (c *manifestCache) put(url, target string, body []byte) {
	if !pinned(target) || verify(body, target) != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.held) >= manifestLimit {
		return
	}

	c.held[url] = body
}
