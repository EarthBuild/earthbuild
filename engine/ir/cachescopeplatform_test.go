package ir

import "testing"

// A cache a machine offers is scoped by the architecture that filled it.
//
// **The gap the fleet made reachable.** `<mounts>/<id>/<scope>` is also the key
// a worker's cache map is filed and looked up under - `cachemaps/<id>/<scope>`
// - so two machines agreeing on that key agree to exchange units. The scope
// carries the trust domain and the portability claim and nothing about the
// machine, and `Scope(domain string)` could not carry one: the signature never
// saw a platform.
//
// So an amd64 worker and an arm64 driver, building one target with one `--id`,
// computed the same scope, and the driver was told the worker's map for a cache
// filled by another architecture. Whether the units then collide is the *tool's*
// business - Go keys its objects by GOARCH and would miss them harmlessly -
// which is exactly the reasoning the engine must not depend on. §5.3 keys the
// directory by trust domain for the same reason: not because every writer is
// hostile, but because the engine cannot audit what the writer assumed.
//
// `--portable` is the author saying these bytes are stable across *machines*.
// It is not the author saying they are stable across instruction sets, and
// nothing asks them which they meant.
//
// Scoped only where a scope already exists, which is the compatibility half:
// a cache nobody has offered and nobody distrusts keeps its directory.
func TestACacheIsScopedByTheArchitectureThatFilledIt(t *testing.T) {
	t.Parallel()

	m := Mount{Target: "/c", ID: "go-build", Portable: true}

	amd := m.Scope("", Platform{OS: "linux", Arch: "amd64"})
	arm := m.Scope("", Platform{OS: "linux", Arch: "arm64"})

	if amd == arm {
		t.Error("an amd64 machine and an arm64 machine scope one cache the same," +
			"\n  so each is told the other's map and imports its units")
	}

	// A variant is an architecture for this purpose: armv6 and armv7 are not
	// one machine, and the placement rules already treat them apart.
	v6 := m.Scope("", Platform{OS: "linux", Arch: "arm", Variant: "v6"})
	v7 := m.Scope("", Platform{OS: "linux", Arch: "arm", Variant: "v7"})

	if v6 == v7 {
		t.Error("two variants of one architecture scope a cache the same")
	}

	// And the OS, because a linux cache is not a darwin one whatever the
	// architecture says.
	if m.Scope("", Platform{OS: "linux", Arch: "amd64"}) ==
		m.Scope("", Platform{OS: "darwin", Arch: "amd64"}) {
		t.Error("two operating systems scope a cache the same")
	}
}

// A cache that is neither offered nor distrusted keeps its directory.
//
// The compatibility half, restated as a test because the cost of getting it
// wrong is every cache directory on every machine moving at once - a slow build
// for everybody, buying nothing for a cache nobody shares.
func TestAnUnofferedCacheIsNotMovedByItsPlatform(t *testing.T) {
	t.Parallel()

	m := Mount{Target: "/c", ID: "k"}

	if got := m.Scope("", Platform{OS: "linux", Arch: "amd64"}); got != "" {
		t.Errorf("a cache making no claim on an undistrusted machine scoped to %q,"+
			" so its directory moved for nothing", got)
	}
}
