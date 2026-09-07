package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A missing shell says what the step's filesystem *does* hold.
//
// "the image does not have this program" is true of both cases it covers and
// distinguishes neither: an image that genuinely ships no shell, and a root
// that was assembled wrongly and holds nothing at all. Fourteen CI jobs failed
// on this message and it named the one thing already known - the binary that
// was not there (E642).
//
// The deepest directory that does exist, and how much is in it, separates them
// in one line.
func TestAnAbsentBinarySaysWhatIsThere(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		build func(root string) error
		want  []string
	}{
		"a root with a populated /bin but no shell": {
			build: func(root string) error {
				err := os.MkdirAll(filepath.Join(root, "bin"), 0o750)
				if err != nil {
					return err
				}

				for _, n := range []string{"ls", "cat", "echo"} {
					err = os.WriteFile(filepath.Join(root, "bin", n), nil, 0o600)
					if err != nil {
						return err
					}
				}

				return nil
			},
			want: []string{"/bin", "3"},
		},

		"a root with no /bin at all": {
			build: func(root string) error {
				return os.MkdirAll(filepath.Join(root, "etc"), 0o750)
			},
			want: []string{"/bin", "not there", "/"},
		},

		// The case worth telling apart: nothing was assembled.
		"an empty root": {
			build: func(string) error { return nil },
			want:  []string{"/", "empty"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()

			err := tc.build(root)
			if err != nil {
				t.Fatal(err)
			}

			got := neighbours(root, "/bin/sh")
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("the hint is %q, and does not mention %q", got, want)
				}
			}
		})
	}
}
