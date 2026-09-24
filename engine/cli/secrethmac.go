package cli

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// EnvSecretHMAC names the fleet key that makes secret steps cacheable.
//
// A step given a secret is uncacheable by default, because the only honest key
// for it would describe the credential, and a key is written to disk. That is
// the whole of I19: a secret enters the graph by identity and never by value.
//
// The key here buys back the cache without giving that up. What enters the key
// is HMAC(fleet key, name ‖ value) - a digest, not the value. The digest is
// worthless to anyone without the fleet key, which is the point: a bare
// SHA-256 of a credential is an *oracle*, because credentials are drawn from a
// small guessable space. Anyone who can read a shared cache directory could
// hash a candidate and look for the key; a hit confirms the value, and nothing
// was ever decrypted. Keying the hash removes the ability to compute a
// candidate's digest at all, which is the attack HMAC exists to answer.
//
// Set once for a fleet - a repository secret in CI, exported into the
// environment - and never per build. It is not a secret this engine protects
// in the way it protects the build's own: it is not scanned for, not redacted,
// and a step never sees it.
const EnvSecretHMAC = "EARTH_HMAC"

// minHMACKeyLen is where a fleet key stops being a key and becomes a formality.
//
// A short key restores the oracle by a different route: rather than guessing
// the credential, an attacker guesses the *fleet key* and is then free to test
// credentials at will. Thirty-two characters is the width of the digest the key
// feeds, so anything less is the weaker half of the construction.
//
// Length is a crude proxy for entropy and known to be one - `aaaa...` passes.
// It is kept because the failure it catches is the one that actually happens
// (a placeholder committed as a fleet key), and because the alternative is an
// entropy estimator that would reject legitimate keys nobody could argue with.
const minHMACKeyLen = 32

// secretDigests maps each supplied secret to a value-derived cache key
// contribution, or to nothing at all when no fleet key is configured.
//
// The name is hashed alongside the value so that one credential supplied under
// two names does not collapse two steps into one cache entry - they run
// different commands and must key differently.
//
// Returning `nil` rather than an error for an absent key is deliberate: the
// engine's behaviour without a fleet key is the behaviour it has always had,
// and a build that never wanted this feature should not have to know it exists.
func secretDigests(key string, secrets map[string]string) (map[string]string, error) {
	if key == "" {
		return nil, nil
	}

	if len(key) < minHMACKeyLen {
		return nil, fmt.Errorf(
			"%s is %d characters, and a fleet key shorter than %d is not one"+
				"\n  it keys the digest that lets a step holding a secret be cached,"+
				" and a guessable key lets a reader of the cache test credentials against it"+
				"\n  generate one with `openssl rand -hex 32` and set it once for the fleet"+
				"\n  unset %s to leave secret steps uncacheable, which is the default",
			EnvSecretHMAC, len(key), minHMACKeyLen, EnvSecretHMAC)
	}

	if len(secrets) == 0 {
		return nil, nil
	}

	out := make(map[string]string, len(secrets))

	// Sorted so the walk is deterministic. Nothing here depends on the order -
	// each entry is hashed alone - but a map walk that could have been ordered
	// and was not is how a difference appears later under a change that looks
	// unrelated.
	names := make([]string, 0, len(secrets))
	for name := range secrets {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		mac := hmac.New(sha256.New, []byte(key))
		// The separator keeps `AB` + `C` apart from `A` + `BC`; a NUL cannot
		// appear in an environment variable's name.
		mac.Write([]byte(name))
		mac.Write([]byte{0})
		mac.Write([]byte(secrets[name]))
		out[name] = hex.EncodeToString(mac.Sum(nil))
	}

	return out, nil
}
