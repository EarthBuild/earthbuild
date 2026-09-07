package ir_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// An id survives being written down and read back.
func TestANodeIDRoundTrips(t *testing.T) {
	t.Parallel()

	want := ir.NodeID{1, 2, 3, 250}

	got, err := ir.ParseNodeID(want.String())
	if err != nil {
		t.Fatalf("parse %q: %v", want, err)
	}

	if got != want {
		t.Errorf("wrote %v and read %v", want, got)
	}
}

// Anything that is not one is refused, not truncated.
//
// A short digest that parsed to a padded id would name a *different* layer, and
// name it confidently: the store would then answer for content nobody asked for.
func TestSomethingThatIsNotAnIDIsRefused(t *testing.T) {
	t.Parallel()

	for _, s := range []string{
		"",
		"nothex",
		strings.Repeat("a", 2*ir.HashSize-2), // one byte short
		strings.Repeat("a", 2*ir.HashSize+2), // one byte long
	} {
		_, err := ir.ParseNodeID(s)
		if err == nil {
			t.Errorf("%q parsed as an id", s)
		}
	}
}
