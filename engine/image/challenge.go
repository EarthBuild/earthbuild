package image

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// A registry answers an unauthenticated request with a challenge naming where to
// get a token. Collecting it is a whole round trip that fetches no data, and on
// docker.io it is 0.465s - most of a build that has nothing to do (E534).
//
// What the challenge names is stable, public metadata: a realm, a service and a
// scope, the same for every build against that repository. So it is remembered.
// The *token* is not: it is a credential, it expires, and putting one in a cache
// directory is a decision about credentials rather than an optimisation (E535).

// challengePath names the file remembering one repository's challenge.
//
// A file each rather than one document, because two builds resolving different
// images at once would otherwise rewrite the same JSON over each other, and the
// prize for that race is a corrupt cache of something not worth locking for.
//
// Hashed because a key contains a registry host, a port and a repository path -
// slashes, colons, and on some registries characters this filesystem would
// rather not see.
func challengePath(dir, key string) string {
	sum := sha256.Sum256([]byte(key))

	return filepath.Join(dir, "challenges", hex.EncodeToString(sum[:])[:32])
}

// rememberedChallenge is where this repository's token was fetched from last
// time, or empty.
func rememberedChallenge(dir, key string) string {
	if dir == "" {
		return ""
	}

	b, err := os.ReadFile(challengePath(dir, key))
	if err != nil {
		return ""
	}

	at := strings.TrimSpace(string(b))

	// Only ever a URL this engine wrote, but it is read back from a file that
	// anything with the user's permissions can edit, and it is about to be
	// fetched with a bearer request. A scheme check is the cheap half of not
	// being redirected somewhere odd by a scribbled-on cache.
	u, err := url.Parse(at)
	if err != nil || (u.Scheme != schemeHTTPS && u.Scheme != schemePlain) {
		return ""
	}

	return at
}

// rememberChallenge records where a token came from. Best effort: a cache that
// cannot be written costs a probe, which is what happened before it existed.
func rememberChallenge(dir, key, at string) {
	if dir == "" || at == "" {
		return
	}

	p := challengePath(dir, key)

	err := os.MkdirAll(filepath.Dir(p), 0o700)
	if err != nil {
		return
	}

	// Written beside and renamed in, so a reader never sees half a URL.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".challenge-")
	if err != nil {
		return
	}

	_, err = tmp.WriteString(at)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())

		return
	}

	err = tmp.Close()
	if err != nil {
		_ = os.Remove(tmp.Name())

		return
	}

	err = os.Rename(tmp.Name(), p)
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
}

// challengeKey names a repository on a registry, which is what a challenge is
// issued for.
func challengeKey(r Ref) string { return registryHost(r.Registry) + "/" + r.Repository }
