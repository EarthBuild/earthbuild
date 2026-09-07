package exec

import "testing"

// TestWhetherTheContextIsPackedWhereItLies.
//
// **On by default, and the off spellings are the way back.** The switch exists
// because the change is not purely about speed: a context with hardlinks packs
// to a different archive, and an operator who has to ask whether that is what
// broke their cache needs a way to answer it without rebuilding the engine.
func TestWhetherTheContextIsPackedWhereItLies(t *testing.T) {
	for _, c := range []struct {
		set  string
		want bool
	}{
		{"", true},
		{"1", true},
		{"true", true},
		{"yes", true},
		{"0", false},
		{"false", false},
		{"no", false},
	} {
		t.Run(c.set, func(t *testing.T) {
			t.Setenv(EnvDirectContextPack, c.set)

			if got := directContextPack(); got != c.want {
				t.Errorf("%s=%q packs directly = %v, want %v",
					EnvDirectContextPack, c.set, got, c.want)
			}
		})
	}
}
