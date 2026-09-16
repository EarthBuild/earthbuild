package ir

import "testing"

// A cache nobody has made a claim about keeps the directory it always had.
//
// **The compatibility half of the gate.** Scoping every cache would move every
// cache directory on every machine at once, which costs a slow build for
// everybody and buys nothing: a cache with no claim is never served to anybody,
// so there is nothing for it to be confused with.
func TestAnUnclaimedCacheHasNoScope(t *testing.T) {
	t.Parallel()

	for _, m := range []Mount{
		{Target: "/c", ID: "k"},
		{Target: "/c", ID: "k", Exclusive: true},
		{Target: "/c", ID: "k", Persist: true},
	} {
		if got := m.Scope(""); got != "" {
			t.Errorf("a cache making no claim is scoped to %q, which moves every"+
				" existing cache directory for nothing", got)
		}
	}
}

// A claimed cache never shares a directory with an unclaimed one of the same id.
//
// **The hazard this exists to remove.** `--portable-except` is declared on a
// *mount* and the directory is named by an *id*, so two steps naming one id
// share one directory however differently they declared it. Target A claims its
// cache is portable, target B says nothing, and a fetch for A fills the
// directory B then runs against - bytes from another machine in a cache whose
// author made no claim at all (F1a).
//
// Κ₁ does not help: it makes the two *steps* different, and says nothing about
// the *directory*, which is what is actually shared.
func TestAClaimedCacheIsNeverInAnUnclaimedDirectory(t *testing.T) {
	t.Parallel()

	plain := Mount{Target: "/c", ID: "k"}
	claimed := Mount{Target: "/c", ID: "k", Portable: true}

	if plain.Scope("") == claimed.Scope("") {
		t.Error("a cache claimed portable shares a directory with one making no" +
			" claim, so a fetch fills a cache whose author never offered it")
	}
}

// Two claims that exclude different paths never share a directory.
//
// A machine whose only build declared `metadata-*/**` local would otherwise
// serve those paths to a requester that declared nothing - exclusions the
// holder never wrote, over a directory it did not describe (F1b).
func TestDifferentExclusionsAreDifferentDirectories(t *testing.T) {
	t.Parallel()

	one := Mount{Target: "/c", ID: "k", Portable: true, PortableExcept: "tmp/**"}
	two := Mount{Target: "/c", ID: "k", Portable: true, PortableExcept: "lock"}

	if one.Scope("") == two.Scope("") {
		t.Error("two different exclusion lists share a directory, so one" +
			" machine serves paths another never promised were stable")
	}
}

// Two spellings of one list are one directory.
//
// **Deliberately unlike Κ₁**, which hashes the list *as written* so that two
// spellings are two caches - the conservative direction for a key, where being
// wrong means a false hit. A directory is the opposite case: `'a,b'` and `'b,a'`
// produce the same matcher, and putting them in different directories halves the
// cache with no safety bought. `ignore.Patterns` already trims and drops empties,
// so the scope must agree with the matcher rather than with the key.
func TestOneListSpelledFourWaysIsOneDirectory(t *testing.T) {
	t.Parallel()

	want := Mount{Target: "/c", ID: "k", Portable: true, PortableExcept: "a,b"}.Scope("")

	for _, spelling := range []string{"b,a", " a , b ", "a,,b", ",a,b,"} {
		got := Mount{Target: "/c", ID: "k", Portable: true, PortableExcept: spelling}.Scope("")
		if got != want {
			t.Errorf("%q scopes to %s, want %s\n  two spellings of one matcher"+
				" halve the cache and buy nothing", spelling, got, want)
		}
	}
}

// A persisted cache is not in a portable cache's directory.
//
// `--persist` and `--portable-except` refuse each other on one `CACHE` line, and
// that refusal is per line while the directory is per id. One target may persist
// id `k` and another declare it portable, both legally, and the persisted step
// then copies into its image bytes that came from another machine and were never
// any step's output (F4b).
func TestAPersistedCacheIsNotAPortableOne(t *testing.T) {
	t.Parallel()

	portable := Mount{Target: "/c", ID: "k", Portable: true}
	persisted := Mount{Target: "/c", ID: "k", Persist: true}

	if portable.Scope("") == persisted.Scope("") {
		t.Error("a persisted cache shares a directory with a portable one, so" +
			" another machine's bytes are published in an image")
	}
}

// The scope is a name a directory can have.
//
// Hex, so it cannot contain a separator, a dot-dot or anything a filesystem
// treats specially - the id beside it is already used raw and unescaped, and one
// unescaped component per path is quite enough.
func TestAScopeIsASafePathComponent(t *testing.T) {
	t.Parallel()

	got := Mount{Target: "/c", ID: "k", Portable: true, PortableExcept: "../../etc,a/b"}.Scope("")

	if got == "" {
		t.Fatal("a claimed cache has no scope")
	}

	for _, r := range got {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			t.Fatalf("scope %q is not hex, so it is not safely a path component", got)
		}
	}
}

// A trust domain isolates every cache in a build, not only the offered ones.
//
// **§5.3 states write-scoping as load bearing**: an untrusted build - a pull
// request from a fork - reads the shared cache and writes only to an isolated
// namespace, because signing does not help when the attacker is a legitimate
// writer. `<mounts>/<id>` is one namespace for every build a machine has ever
// run, so today there is no such isolation for a cache mount at all.
//
// Scoping only the *portable* ones would close the hazard this transport
// creates and leave the one that was already there, which is the wrong half:
// what a fork poisons on a shared worker is a directory, and whether its author
// happened to offer it to anybody is beside the point.
func TestATrustDomainScopesEveryCache(t *testing.T) {
	t.Parallel()

	plain := Mount{Target: "/c", ID: "k"}

	if plain.Scope("") != "" {
		t.Fatal("the no-domain case has stopped being free")
	}

	if plain.Scope("fork-pr-412") == "" {
		t.Error("a cache in an untrusted domain is unscoped, so a fork's build" +
			" writes into the namespace every other build reads")
	}
}

// Two domains never share a directory, claimed or not.
func TestTwoDomainsNeverShareADirectory(t *testing.T) {
	t.Parallel()

	for _, m := range []Mount{
		{Target: "/c", ID: "k"},
		{Target: "/c", ID: "k", Portable: true},
		{Target: "/c", ID: "k", Portable: true, PortableExcept: "tmp/**"},
	} {
		if m.Scope("trusted") == m.Scope("fork-pr-412") {
			t.Errorf("%+v shares a directory across trust domains, so a fork's"+
				" entry is installed wherever the trusted build reads", m)
		}
	}
}

// The domain is not a substitute for the claim, nor the claim for the domain.
//
// Both are in the hash and neither shadows the other: an offered cache in one
// domain must not land where an unoffered cache in the same domain does, and a
// claim identical in two domains must still be two directories.
func TestTheDomainAndTheClaimAreIndependent(t *testing.T) {
	t.Parallel()

	seen := map[string]string{}

	for _, c := range []struct {
		name   string
		domain string
		m      Mount
	}{
		{"untrusted, unclaimed", "fork", Mount{ID: "k"}},
		{"untrusted, claimed", "fork", Mount{ID: "k", Portable: true}},
		{"trusted, unclaimed", "main", Mount{ID: "k"}},
		{"trusted, claimed", "main", Mount{ID: "k", Portable: true}},
	} {
		got := c.m.Scope(c.domain)
		if was, clash := seen[got]; clash {
			t.Errorf("%q and %q resolve to one directory", c.name, was)
		}

		seen[got] = c.name
	}
}
