package main

import "testing"

// A kill only counts if the package passed before the mutant went in.
//
// `go test` failing is the whole evidence for "a test noticed", so a package
// that was already failing makes every mutant in it read as killed - four
// hundred confident verdicts resting on a suite nobody checked. It is not
// hypothetical: `engine/mat/overlay`'s deep-stack test cannot mount inside a
// container whose root is overlay, and the linux sweep duly reported
// `overlay: reversing the stack for lowerdir` as killed by a test that fails
// with or without it (E642).
//
// The mirror of the skip problem, and the worse half: a skip reports a
// mechanism as unguarded when it is guarded, and this reports one as guarded
// when nothing checked.
func TestAKillCountsOnlyAgainstAGreenPackage(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		green bool
		want  string
	}{
		"the package passed, so the mutant is what broke it": {green: true, want: verdictKilled},
		"the package was already failing":                    {green: false, want: verdictDirty},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := judgeKill(tc.green); got != tc.want {
				t.Errorf("a failing test run against a green=%v package is %q, want %q",
					tc.green, got, tc.want)
			}
		})
	}
}
