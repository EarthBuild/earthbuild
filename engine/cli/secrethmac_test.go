package cli

import (
	"strings"
	"testing"
)

// A fleet key that is absent leaves the engine as it was: no digests, no error,
// and every secret step uncacheable. The feature is opt-in because the default
// has to be the safe one.
func TestSecretDigestsOffWithoutKey(t *testing.T) {
	t.Parallel()

	got, err := secretDigests("", map[string]string{"TOK": "s3cret"})
	if err != nil {
		t.Fatalf("no key should not be an error: %v", err)
	}

	if got != nil {
		t.Fatalf("no key should yield no digests, got %v", got)
	}
}

// A short key restores the very attack the keying exists to prevent: a fleet
// key of six characters is itself brute-forceable, so the digest becomes an
// oracle again. Refuse rather than pretend.
func TestSecretDigestsRefusesShortKey(t *testing.T) {
	t.Parallel()

	_, err := secretDigests("hunter2", map[string]string{"TOK": "s3cret"})
	if err == nil {
		t.Fatal("a seven-character fleet key was accepted")
	}

	for _, want := range []string{"EARTH_HMAC", "32"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not mention %q: %s", want, err)
		}
	}
}

// The digest is a function of the value, so two values differ; and it is a
// function of the name too, so the same value under two names does not collide
// into one cache entry.
func TestSecretDigestsSeparatesValuesAndNames(t *testing.T) {
	t.Parallel()

	key := strings.Repeat("k", 32)

	a, err := secretDigests(key, map[string]string{"TOK": "one", "OTHER": "one"})
	if err != nil {
		t.Fatal(err)
	}

	b, err := secretDigests(key, map[string]string{"TOK": "two"})
	if err != nil {
		t.Fatal(err)
	}

	if a["TOK"] == b["TOK"] {
		t.Error("two values produced one digest")
	}

	if a["TOK"] == a["OTHER"] {
		t.Error("one value under two names produced one digest")
	}
}

// Same key, same value, same digest - across processes and machines, or a
// fleet shares no cache at all.
func TestSecretDigestsDeterministic(t *testing.T) {
	t.Parallel()

	key := strings.Repeat("k", 32)
	in := map[string]string{"TOK": "s3cret"}

	a, err := secretDigests(key, in)
	if err != nil {
		t.Fatal(err)
	}

	b, err := secretDigests(key, in)
	if err != nil {
		t.Fatal(err)
	}

	if a["TOK"] != b["TOK"] {
		t.Errorf("digest is not stable: %s vs %s", a["TOK"], b["TOK"])
	}
}

// The value must not be recoverable from, or visible beside, the digest.
func TestSecretDigestsCarryNoValue(t *testing.T) {
	t.Parallel()

	got, err := secretDigests(strings.Repeat("k", 32), map[string]string{"TOK": "s3cret"})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(got["TOK"], "s3cret") {
		t.Fatalf("digest contains the value: %s", got["TOK"])
	}
}

// A different fleet key over the same secret is a different digest, so two
// fleets sharing a cache directory cannot read each other's entries - and
// neither can test a candidate against the other's.
func TestSecretDigestsKeySeparatesFleets(t *testing.T) {
	t.Parallel()

	in := map[string]string{"TOK": "s3cret"}

	a, err := secretDigests(strings.Repeat("a", 32), in)
	if err != nil {
		t.Fatal(err)
	}

	b, err := secretDigests(strings.Repeat("b", 32), in)
	if err != nil {
		t.Fatal(err)
	}

	if a["TOK"] == b["TOK"] {
		t.Error("two fleet keys produced one digest")
	}
}
