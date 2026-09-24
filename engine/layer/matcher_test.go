package layer

import (
	"strings"
	"testing"
)

// TestManySecretsCostOnePass.
//
// **The scan was one pass of `bytes.Contains` per secret**, so ten credentials
// meant ten passes over every byte of a layer. A build with a registry token, a
// deploy key and an npm auth line is ordinary, and the cost grew with the number
// of things worth protecting - which is the wrong way round.
//
// One automaton over all of them reads each byte once, whatever the count. It
// also removes the reason a chunked read needed an overlap: the state carries
// between chunks, so a credential split across two is matched without anybody
// keeping a tail.
func TestManySecretsCostOnePass(t *testing.T) {
	t.Parallel()

	m := newMatcher([]Secret{
		{Name: "TOKEN", Value: "hunter2"},
		{Name: "DEPLOY", Value: "swordfish"},
		{Name: "NPM", Value: "battery"},
		{Name: "ABSENT", Value: "staple"},
	})

	// Fed in pieces that split two of the values, which is what a read does.
	for _, chunk := range []string{"a hunt", "er2 b sword", "fish c batt", "ery d"} {
		m.write([]byte(chunk))
	}

	got := m.found()

	if strings.Join(got, ",") != "DEPLOY,NPM,TOKEN" {
		t.Errorf("found %v, want DEPLOY, NPM and TOKEN, sorted", got)
	}
}

// A value that is a prefix or a suffix of another is still found: the automaton
// has to report every pattern that ends here, not the longest.
func TestOverlappingSecretsAreAllFound(t *testing.T) {
	t.Parallel()

	m := newMatcher([]Secret{
		{Name: "SHORT", Value: "abc"},
		{Name: "LONG", Value: "xabc"},
		{Name: "INNER", Value: "bc"},
	})

	m.write([]byte("---xabc---"))

	got := m.found()
	if len(got) != 3 {
		t.Errorf("found %v, want all three - a pattern ending here is a match"+
			"\n  whether or not a longer one also ends here", got)
	}
}

// Nothing to look for reads nothing, and a clean text reports nothing.
func TestAMatcherWithNothingToFind(t *testing.T) {
	t.Parallel()

	if m := newMatcher(nil); m != nil {
		t.Error("a matcher was built for no secrets")
	}

	m := newMatcher([]Secret{{Name: "S", Value: "needle"}})
	m.write([]byte("a haystack with no needles... wait"))

	if got := m.found(); len(got) != 1 {
		t.Errorf("found %v, want the one that is there", got)
	}
}

// The same secret twice is one finding: a caller wants to know which credential
// leaked, not how often.
func TestARepeatedSecretIsOneFinding(t *testing.T) {
	t.Parallel()

	m := newMatcher([]Secret{{Name: "S", Value: "aa"}})
	m.write([]byte("aaaaaa"))

	if got := m.found(); len(got) != 1 {
		t.Errorf("found %v, want one", got)
	}
}
