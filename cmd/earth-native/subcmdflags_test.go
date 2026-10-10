package main

import (
	"slices"
	"testing"
)

// TestASubcommandTakesItsFlagsAfterItsName.
//
// `earthly doc --long` is how the reference is written and how the corpus
// drives it: `tests/Earthfile` runs `doc-recipe-block.earth` with
// `--extra_args="doc --long"`. Go's flag package stops at the first non-flag
// argument, so `--long` arrived as an *argument to doc* and was reported as a
// build argument that is not one - a diagnostic about the wrong thing entirely.
//
// Only for the subcommands, and that is the whole of the rule: after a
// *target*, `--NAME=value` is a build argument and must stay where it is.
// `doc` and `ls` take no build arguments, so anything dash-prefixed after them
// is a flag and nothing else it could be.
func TestASubcommandTakesItsFlagsAfterItsName(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		in   []string
		want []string
	}{
		{"a flag after doc", []string{"doc", "--long"}, []string{"--long", "doc"}},
		{"a flag after ls", []string{"ls", "-long"}, []string{"-long", "ls"}},
		{
			"flags either side",
			[]string{"-dir", "x", "doc", "--long"},
			[]string{"-dir", "x", "--long", "doc"},
		},
		// Left alone: a build argument after a target is not a flag, and
		// hoisting it would make `+build --VERSION=2` set a flag named VERSION.
		{
			"a build argument after a target",
			[]string{"+build", "--VERSION=2"},
			[]string{"+build", "--VERSION=2"},
		},
		{"nothing to move", []string{"doc"}, []string{"doc"}},
		{"no subcommand", []string{"+build"}, []string{"+build"}},
	} {
		got := hoistSubcommandFlags(c.in)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %q became %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
