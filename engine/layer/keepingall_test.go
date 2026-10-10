package layer

import "testing"

// Nothing asked for is everything kept, however the absence is spelled.
//
// `keeping` is what a fragment carries. An empty request is the whole layer,
// which is what makes `Pack` the degenerate case of `PackFragment` rather than a
// second implementation of it.
//
// **This is not the test that guards that rule** - `newKeeper`'s `all` field is,
// and it was already covered. This one exists for the boundary above it: a nil
// want and an empty-but-non-nil want must mean the same thing, so the rule does
// not depend on how a caller spelled "nothing".
//
// Written while chasing a catalogue entry that pointed at `keeping`'s early
// return, which is an optimisation - `keeps` is `k.all || …` - and so could
// never be killed by any test. The entry now points at the line that decides.
// An equivalent mutant reported as a survivor costs an afternoon per reader.
func TestNothingAskedForKeepsEverythingHoweverSpelled(t *testing.T) {
	t.Parallel()

	entries := []entry{
		{path: "usr"},
		{path: "usr/bin/sh"},
		{path: "etc/hosts"},
	}

	for _, want := range [][]string{nil, {}} {
		got := keeping(entries, want)
		if len(got) != len(entries) {
			t.Errorf("want=%#v kept %d of %d entries", want, len(got), len(entries))
		}
	}
}
