package exec

import "testing"

// `WITH DOCKER --load` puts an image into a daemon in one step and the body
// looks for it in another. Both steps get their own daemon on purpose, and their
// own storage on purpose; what makes the pair work is storage shared by the
// block and surviving nothing beyond it (E886).
//
// Three cases, and the third is the one that did not exist: per-step and gone,
// named and kept, and named and gone.
func TestWhatADaemonsStorageIsScopedTo(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name          string
		cache, scope  string
		wantID        string
		wantEphemeral bool
	}{
		{"no block storage at all", "", "", "", true},
		{"a named cache outlives the build", "mine", "", "docker-cache/mine", false},
		{"a block's own storage is shared and temporary", "", "b7", "docker-scope/b7", true},
		{"a named cache wins, because the author asked for it", "mine", "b7", "docker-cache/mine", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, root := ownDaemonMounts(c.cache, c.scope)
			if len(got) != 1 {
				t.Fatalf("got %d mounts, want 1", len(got))
			}

			if got[0].ID != c.wantID {
				t.Errorf("ID = %q, want %q", got[0].ID, c.wantID)
			}

			if got[0].Ephemeral != c.wantEphemeral {
				t.Errorf("Ephemeral = %v, want %v", got[0].Ephemeral, c.wantEphemeral)
			}

			if root != daemonRoot {
				t.Errorf("root = %q, want %q", root, daemonRoot)
			}
		})
	}
}
