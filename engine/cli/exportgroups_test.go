package cli

import (
	"path/filepath"
	"reflect"
	"testing"
)

// Artifacts may be written at once only when they are certainly different
// places.
//
// Grouping was by exact destination equality, which is not the same question.
// Two destinations that differ as strings can still be one place: an artifact
// saved to `out` and another to `out/sub/x` overlap, and writing them at once
// races over the same tree. What makes concurrency safe is that neither
// destination contains the other.
//
// **A path prefix, not a string prefix.** `out/ab` is a string prefix of
// `out/abc` and they are different directories; grouping them together would
// be merely slow, but the same mistake in the other direction - treating
// `out/sub` and `out/subdir/x` as related - is how a naive HasPrefix serialises
// half a build for nothing.
func TestOnlyCertainlyDifferentPlacesAreWrittenAtOnce(t *testing.T) {
	t.Parallel()

	j := func(parts ...string) string { return filepath.Join(parts...) }

	for _, c := range []struct {
		name  string
		dests []string
		want  [][]int
	}{
		{
			name:  "different places go in any order",
			dests: []string{j("out", "a"), j("out", "b"), j("other", "c")},
			want:  [][]int{{0}, {1}, {2}},
		},
		{
			name: "the same place keeps the Earthfile's order, because the second is meant to win",
			dests: []string{j("out", "a"), j("out", "b"), j("out", "a")},
			want:  [][]int{{0, 2}, {1}},
		},
		{
			name:  "a destination inside another is the same place",
			dests: []string{"out", j("out", "sub", "x"), j("other", "c")},
			want:  [][]int{{0, 1}, {2}},
		},
		{
			name:  "and it does not matter which way round they are given",
			dests: []string{j("out", "sub", "x"), "out"},
			want:  [][]int{{0, 1}},
		},
		{
			name:  "a string prefix that is not a path prefix is a different place",
			dests: []string{j("out", "ab"), j("out", "abc")},
			want:  [][]int{{0}, {1}},
		},
		{
			name:  "containment is transitive: three deep is one group",
			dests: []string{j("a", "b", "c"), "a", j("a", "b")},
			want:  [][]int{{0, 1, 2}},
		},
		{
			name:  "nothing to write is no groups",
			dests: nil,
			want:  nil,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := exportGroups(c.dests)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("exportGroups(%q) = %v, want %v", c.dests, got, c.want)
			}
		})
	}
}
