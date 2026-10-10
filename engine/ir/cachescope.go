package ir

import (
	"sort"
	"strings"
)

// scopeTag separates this digest from every other one this package takes, so a
// scope can never collide with a layer id or a node identity by construction.
const scopeTag = "cache-scope/1"

// Scope is the directory a cache mount's contents belong in, beneath its id.
//
// **The claim is made on a mount and the directory is named by an id**, which
// are not the same thing and have been treated as though they were. Two steps
// naming one id share one directory however differently they declared it, and
// Κ₁ does not help: it makes the two *steps* different and says nothing about
// the *directory*, which is what is actually shared.
//
// Three failures follow from that, and this removes all three:
//
//   - one target claims its cache portable and another says nothing, so a fetch
//     for the first fills the directory the second runs against - another
//     machine's bytes in a cache whose author made no claim;
//   - two targets claim different exclusions, so a machine serves paths it never
//     promised were stable, minus exclusions it never wrote;
//   - one target persists an id and another declares it portable, both legally,
//     and the persisted step publishes in its image bytes that were never any
//     step's output.
//
// **The domain is the other half, and §5.3 calls it load bearing.** An untrusted
// build - a pull request from a fork - reads the shared cache and writes only to
// an isolated namespace, because signing does not help when the attacker is a
// legitimate writer. `<mounts>/<id>` is one namespace for every build a machine
// has ever run, so there is no such isolation today.
//
// A domain therefore scopes **every** cache in the build and not only the
// offered ones. Scoping the claimed ones alone would close the hazard this
// transport introduces and leave the one that was already there, which is the
// wrong half: what a fork poisons on a shared worker is a directory, and whether
// its author happened to offer it to anybody is beside the point.
//
// **Empty where neither applies**, which is every cache in an ordinary build.
// That is the compatibility half: scoping everything unconditionally would move
// every cache directory on every machine at once, costing a slow build for
// everybody and buying nothing, because a cache nobody has offered and nobody
// distrusts has nothing to be confused with.
//
// Hex, because the id beside it is already used raw and unescaped, and one
// unescaped component per path is enough.
func (m Mount) Scope(domain string, p Platform) string {
	if !m.Portable && domain == "" {
		return ""
	}

	h := NewHasher()
	h.Str(scopeTag)
	h.Str(domain)
	// **π, because this scope is also the key a map is exchanged under.**
	// `cachemaps/<id>/<scope>` is what a worker files its map as and what a
	// driver looks one up by, so two machines agreeing on a scope agree to
	// exchange units. Without this an amd64 worker and an arm64 driver agreed,
	// and the driver imported a cache another architecture filled.
	//
	// Whether the units then collide is the *tool's* business - Go keys its
	// objects by GOARCH and would miss them harmlessly - and that is exactly
	// the reasoning this must not rest on. `--portable` is an author saying
	// these bytes are stable across machines; it is not an author saying they
	// are stable across instruction sets, and nothing asks which they meant.
	//
	// Whole, not only the architecture: a linux cache is not a darwin one, and
	// armv6 is not armv7 - which §4.7.1 already treats as different machines.
	h.Str(p.OS)
	h.Str(p.Arch)
	h.Str(p.Variant)
	h.Bool(m.Portable)
	h.Str(canonicalPatterns(m.PortableExcept))
	// Not because the two can be written together - they refuse each other on
	// one `CACHE` line - but because that refusal is per line and a directory is
	// per id.
	h.Bool(m.Persist)

	return h.Sum().String()
}

// canonicalPatterns is the exclusion list as the *matcher* reads it.
//
// **Deliberately unlike Κ₁**, which hashes the list as written so that two
// spellings are two caches. That is the conservative direction for a key, where
// being wrong means a false hit and the cost of being over-strict is a miss.
// A directory is the opposite case: `'a,b'` and `'b,a'` produce the same
// matcher, and filing them apart halves the cache while buying no safety at all.
//
// So this agrees with `ignore.Patterns` rather than with the key - split on
// commas, trim, drop empties - and sorts, because order is not meaning in a set
// of patterns. Restated here rather than imported: `engine/ignore` depends on
// this package.
func canonicalPatterns(list string) string {
	var out []string

	seen := map[string]bool{}

	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" && !seen[p] {
			seen[p] = true

			out = append(out, p)
		}
	}

	sort.Strings(out)

	return strings.Join(out, ",")
}
