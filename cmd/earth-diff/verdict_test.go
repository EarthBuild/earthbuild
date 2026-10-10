package main

import "testing"

// The whole tool reduces to this: two exit codes and what they mean together.
// Written first because the rest is process plumbing, and because "they
// disagree" and "they both failed" are the two answers that must never be
// confused - the first is a defect in this engine, the second is a fact about
// the build.
func TestWhatTwoExitCodesMean(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name             string
		native, buildkit int
		want             Verdict
	}{
		{"both build", 0, 0, Agree},
		{"both refuse", 1, 1, Agree},
		{"both fail differently is still agreement on the outcome", 1, 2, Agree},
		{"native fails where the reference builds", 1, 0, NativeGap},
		{"native builds where the reference fails", 0, 1, NativeAhead},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := Compare(c.native, c.buildkit); got != c.want {
				t.Errorf("Compare(%d, %d) = %v, want %v", c.native, c.buildkit, got, c.want)
			}
		})
	}
}

// A verdict has to render as something a person and a script can both read.
func TestAVerdictSaysWhatItIs(t *testing.T) {
	t.Parallel()

	for v, want := range map[Verdict]string{
		Agree:       "agree",
		NativeGap:   "native-gap",
		NativeAhead: "native-ahead",
	} {
		if got := v.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(v), got, want)
		}
	}
}
