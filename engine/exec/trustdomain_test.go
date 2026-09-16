package exec

import "testing"

// An untold trust domain is the single implicit one every build has shared.
//
// The default has to be free: a value here scopes every cache directory in the
// build, so a non-empty default would move all of them on every machine at once
// and every build in the world would start from cold.
func TestAnUntoldTrustDomainIsEmpty(t *testing.T) {
	t.Setenv(EnvTrustDomain, "")

	if got := trustDomain(); got != "" {
		t.Errorf("trustDomain() = %q with nothing set, so an ordinary build is"+
			" isolated from the caches it filled yesterday", got)
	}
}

// Whitespace does not make a second domain.
//
// A value arriving from a CI template commonly carries some, and `"fork "`
// isolating differently from `"fork"` is an isolation nobody asked for and
// nobody can see - a build that misses every cache for a reason invisible in
// the configuration that caused it.
func TestSurroundingSpaceIsNotADomain(t *testing.T) {
	for _, raw := range []string{"fork", " fork", "fork ", "\tfork\n"} {
		t.Setenv(EnvTrustDomain, raw)

		if got := trustDomain(); got != "fork" {
			t.Errorf("trustDomain() = %q for %q, want %q", got, raw, "fork")
		}
	}
}
